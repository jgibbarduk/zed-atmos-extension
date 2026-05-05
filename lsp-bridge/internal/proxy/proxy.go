package proxy

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os/exec"
	"sync"

	"github.com/jgibbarduk/zed-atmos-extension/lsp-bridge/internal/lsp"
)

type Handler interface {
	HandleMethod(method string, content []byte) (handled bool, response []byte, notifications [][]byte, err error)
}

type Proxy struct {
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	stdout   io.ReadCloser
	stdoutBuf *bufio.Reader
	mu       sync.Mutex
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

func (p *Proxy) CallDownstream(content []byte) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.stdin == nil {
		return nil, fmt.Errorf("downstream LSP not available")
	}

	if err := lsp.WriteMessage(p.stdin, content); err != nil {
		return nil, fmt.Errorf("write to atmos: %w", err)
	}

	if lsp.IsNotification(content) {
		return nil, nil
	}

	// The downstream atmos LSP may have sent unsolicited notifications
	// (e.g. publishDiagnostics) or server-to-client requests since the last
	// CallDownstream. We must drain them so we don't misalign request/response
	// pairs.
	for {
		msg, err := lsp.ReadMessage(p.stdoutBuf)
		if err != nil {
			return nil, fmt.Errorf("read from atmos: %w", err)
		}
		if lsp.IsNotification(msg.Content) {
			log.Printf("dropped unsolicited atmos notification: %s", string(msg.Content))
			continue
		}
		if lsp.IsRequest(msg.Content) {
			log.Printf("dropped server-to-client request from atmos: %s", string(msg.Content))
			continue
		}
		return msg.Content, nil
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

func buildErrorResponse(content []byte, code int, message string) []byte {
	var req struct {
		ID json.RawMessage `json:"id"`
	}
	json.Unmarshal(content, &req)
	resp := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      req.ID,
		"error": map[string]interface{}{
			"code":    code,
			"message": message,
		},
	}
	b, _ := json.Marshal(resp)
	return b
}

func (p *Proxy) Run(stdin io.Reader, stdout io.Writer, handler Handler) error {
	reader := bufio.NewReader(stdin)

	for {
		msg, err := lsp.ReadMessage(reader)
		if err != nil {
			if err == io.EOF {
				log.Printf("proxy: stdin EOF, shutting down")
				return nil
			}
			return fmt.Errorf("read stdin: %w", err)
		}

		method := lsp.ParseMethod(msg.Content)
		isNotif := lsp.IsNotification(msg.Content)
		log.Printf("proxy: recv %s (notification=%v, %d bytes)", method, isNotif, len(msg.Content))

		handled, handledResp, notifications, err := handler.HandleMethod(method, msg.Content)
		if err != nil {
			log.Printf("handler error for %s: %v", method, err)
		}

		var response []byte
		if handled {
			response = handledResp
			for i, notif := range notifications {
				log.Printf("proxy: sending notification %d (%d bytes)", i, len(notif))
				if err := lsp.WriteMessage(stdout, notif); err != nil {
					log.Printf("write notification: %v", err)
				}
			}
		} else {
			response, err = p.CallDownstream(msg.Content)
			if err != nil {
				log.Printf("forward error: %v", err)
				response = buildErrorResponse(msg.Content, -32603, fmt.Sprintf("Downstream LSP error: %v", err))
			}
		}

		if response != nil {
			log.Printf("proxy: sending response (%d bytes)", len(response))
			if err := lsp.WriteMessage(stdout, response); err != nil {
				return fmt.Errorf("write stdout: %w", err)
			}
		}
	}
}

func (p *Proxy) Close() error {
	if p.stdin != nil {
		p.stdin.Close()
	}
	if p.cmd != nil {
		return p.cmd.Wait()
	}
	return nil
}
