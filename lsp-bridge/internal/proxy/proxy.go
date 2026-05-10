package proxy

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log"
	"os/exec"
	"sync"
	"time"

	"github.com/jgibbarduk/zed-atmos-extension/lsp-bridge/internal/lsp"
)

const (
	// shutdownTimeout is how long we wait for the downstream process to exit
	// gracefully before force-killing it.
	shutdownTimeout = 5 * time.Second
)

type Handler interface {
	HandleMethod(method string, content []byte) (handled bool, response []byte, notifications [][]byte, err error)
	Notifications() <-chan []byte
	Close()
}

type Proxy struct {
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	stdout    io.ReadCloser
	stdoutBuf *bufio.Reader
	mu        sync.Mutex
	// stdoutMu protects all writes to the client stdout to prevent
	// interleaving of Content-Length headers and bodies between goroutines.
	stdoutMu sync.Mutex
}

func New(atmosPath string) (*Proxy, error) {
	cmd := exec.Command(atmosPath, "lsp", "start", "--transport", "stdio")
	cmd.Stderr = log.Writer()

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		stdin.Close()
		stdout.Close()
		return nil, fmt.Errorf("start atmos lsp: %w", err)
	}

	return &Proxy{
		cmd:       cmd,
		stdin:     stdin,
		stdout:    stdout,
		stdoutBuf: bufio.NewReader(stdout),
	}, nil
}

// NewNop creates a proxy with no downstream process. All downstream calls
// return errors, but the stdin/stdout event loop in Run still works.
func NewNop() *Proxy {
	return &Proxy{}
}

func (p *Proxy) CallDownstream(content []byte) ([]byte, [][]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.stdin == nil {
		return nil, nil, fmt.Errorf("downstream LSP not available")
	}

	if err := lsp.WriteMessage(p.stdin, content); err != nil {
		return nil, nil, fmt.Errorf("write to atmos: %w", err)
	}

	if lsp.IsNotification(content) {
		return nil, nil, nil
	}

	// The downstream atmos LSP may have sent unsolicited notifications
	// (e.g. publishDiagnostics) or server-to-client requests since the last
	// CallDownstream. We must drain them so we don't misalign request/response
	// pairs, but we also need to preserve them to forward to the client.
	var notifications [][]byte
	for {
		msg, err := lsp.ReadMessage(p.stdoutBuf)
		if err != nil {
			return nil, notifications, fmt.Errorf("read from atmos: %w", err)
		}
		if lsp.IsNotification(msg.Content) || lsp.IsRequest(msg.Content) {
			notifications = append(notifications, msg.Content)
			continue
		}
		return msg.Content, notifications, nil
	}
}

func (p *Proxy) SendNotification(content []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stdin == nil {
		return fmt.Errorf("downstream LSP not available")
	}
	return lsp.WriteMessage(p.stdin, content)
}

func (p *Proxy) Run(stdin io.Reader, stdout io.Writer, handler Handler) error {
	reader := bufio.NewReader(stdin)

	// Forward async notifications from the handler to the client.
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("proxy: panic in notification forwarder: %v", r)
			}
		}()
		for notif := range handler.Notifications() {
			log.Printf("proxy: forwarding async notification (%d bytes)", len(notif))
			if err := p.writeClient(stdout, notif); err != nil {
				log.Printf("write async notification: %v", err)
			}
		}
	}()

	for {
		msg, err := lsp.ReadMessage(reader)
		if err != nil {
			if errors.Is(err, io.EOF) {
				log.Printf("proxy: stdin EOF, shutting down")
				handler.Close()
				return nil
			}
			handler.Close()
			return fmt.Errorf("read stdin: %w", err)
		}

		method := lsp.ParseMethod(msg.Content)
		isNotif := lsp.IsNotification(msg.Content)
		log.Printf("proxy: recv %s (notification=%v, %d bytes)", method, isNotif, len(msg.Content))
		if !isNotif {
			log.Printf("proxy: recv content: %s", string(msg.Content))
		}

		handled, handledResp, notifications, err := handler.HandleMethod(method, msg.Content)
		if err != nil {
			log.Printf("handler error for %s: %v", method, err)
		}

		var response []byte
		if handled {
			response = handledResp
			for i, notif := range notifications {
				log.Printf("proxy: sending notification %d (%d bytes)", i, len(notif))
				if err := p.writeClient(stdout, notif); err != nil {
					log.Printf("write notification: %v", err)
				}
			}
		} else {
			var downstreamNotifs [][]byte
			response, downstreamNotifs, err = p.CallDownstream(msg.Content)
			if err != nil {
				log.Printf("forward error: %v", err)
				response = lsp.BuildErrorResponse(msg.Content, -32603, fmt.Sprintf("Downstream LSP error: %v", err))
			}
			for i, notif := range downstreamNotifs {
				log.Printf("proxy: forwarding downstream notification %d (%d bytes)", i, len(notif))
				if werr := p.writeClient(stdout, notif); werr != nil {
					log.Printf("write downstream notification: %v", werr)
				}
			}
		}

		if response != nil {
			log.Printf("proxy: sending response (%d bytes)", len(response))
			if err := p.writeClient(stdout, response); err != nil {
				return fmt.Errorf("write stdout: %w", err)
			}
		}
	}
}

func (p *Proxy) writeClient(stdout io.Writer, content []byte) error {
	p.stdoutMu.Lock()
	defer p.stdoutMu.Unlock()
	return lsp.WriteMessage(stdout, content)
}

func (p *Proxy) Close() error {
	if p.stdin != nil {
		p.stdin.Close()
	}
	if p.stdout != nil {
		p.stdout.Close()
	}
	if p.cmd != nil && p.cmd.Process != nil {
		done := make(chan error, 1)
		go func() { done <- p.cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(shutdownTimeout):
			_ = p.cmd.Process.Kill()
		}
	}
	return nil
}
