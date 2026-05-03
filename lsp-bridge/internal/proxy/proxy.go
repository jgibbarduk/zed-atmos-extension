package proxy

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"os/exec"
	"sync"

	"github.com/jamesgibbard/zed-atmos-language/lsp-bridge/internal/lsp"
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
		cmd:      cmd,
		stdin:    stdin,
		stdout:   stdout,
		stdoutBuf: bufio.NewReader(stdout),
	}, nil
}

func (p *Proxy) CallDownstream(content []byte) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if err := lsp.WriteMessage(p.stdin, content); err != nil {
		return nil, fmt.Errorf("write to atmos: %w", err)
	}

	if lsp.IsNotification(content) {
		return nil, nil
	}

	msg, err := lsp.ReadMessage(p.stdoutBuf)
	if err != nil {
		return nil, fmt.Errorf("read from atmos: %w", err)
	}
	return msg.Content, nil
}

func (p *Proxy) SendNotification(content []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return lsp.WriteMessage(p.stdin, content)
}

func (p *Proxy) Run(stdin io.Reader, stdout io.Writer, handler Handler) error {
	reader := bufio.NewReader(stdin)

	for {
		msg, err := lsp.ReadMessage(reader)
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("read stdin: %w", err)
		}

		method := lsp.ParseMethod(msg.Content)

		handled, handledResp, notifications, err := handler.HandleMethod(method, msg.Content)
		if err != nil {
			log.Printf("handler error for %s: %v", method, err)
		}

		var response []byte
		if handled {
			response = handledResp
			for _, notif := range notifications {
				if err := lsp.WriteMessage(stdout, notif); err != nil {
					log.Printf("write notification: %v", err)
				}
			}
		} else {
			response, err = p.CallDownstream(msg.Content)
			if err != nil {
				log.Printf("forward error: %v", err)
				continue
			}
		}

		if response != nil {
			if err := lsp.WriteMessage(stdout, response); err != nil {
				return fmt.Errorf("write stdout: %w", err)
			}
		}
	}
}

func (p *Proxy) Close() error {
	p.stdin.Close()
	return p.cmd.Wait()
}
