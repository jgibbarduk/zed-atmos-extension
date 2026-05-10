package proxy

import (
	"bufio"
	"bytes"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/jgibbarduk/zed-atmos-extension/lsp-bridge/internal/lsp"
)

func TestNewNop(t *testing.T) {
	p := NewNop()
	if p == nil {
		t.Fatal("expected non-nil Proxy")
	}
	_, _, err := p.CallDownstream([]byte(`{"jsonrpc":"2.0","id":1}`))
	if err == nil {
		t.Fatal("expected error for CallDownstream on nop proxy")
	}
	err = p.SendNotification([]byte(`{}`))
	if err == nil {
		t.Fatal("expected error for SendNotification on nop proxy")
	}
}

func TestCallDownstream_notification(t *testing.T) {
	pr, pw := io.Pipe()
	defer pr.Close()

	// Drain the pipe so the write in CallDownstream doesn't block.
	go func() {
		buf := make([]byte, 1024)
		for {
			_, err := pr.Read(buf)
			if err != nil {
				return
			}
		}
	}()

	p := &Proxy{stdin: pw}
	resp, notifs, err := p.CallDownstream([]byte(`{"jsonrpc":"2.0","method":"initialized"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != nil {
		t.Fatal("expected nil response for notification")
	}
	if len(notifs) != 0 {
		t.Fatalf("expected 0 notifications, got %d", len(notifs))
	}
}

func TestCallDownstream_nilStdin(t *testing.T) {
	p := &Proxy{}
	_, _, err := p.CallDownstream([]byte(`{"jsonrpc":"2.0","id":1}`))
	if err == nil {
		t.Fatal("expected error when stdin is nil")
	}
}

func TestSendNotification_nilStdin(t *testing.T) {
	p := &Proxy{}
	err := p.SendNotification([]byte(`{}`))
	if err == nil {
		t.Fatal("expected error when stdin is nil")
	}
}

func TestWriteClient(t *testing.T) {
	p := NewNop()
	var buf bytes.Buffer
	msg := []byte(`{"jsonrpc":"2.0","method":"test"}`)
	if err := p.writeClient(&buf, msg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Verify Content-Length header format
	content := buf.String()
	if !strings.HasPrefix(content, "Content-Length: ") {
		t.Fatalf("expected Content-Length header, got %q", content)
	}
}

func TestClose_nilFields(t *testing.T) {
	p := NewNop()
	if err := p.Close(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCallDownstream_requestResponse(t *testing.T) {
	// Simulate a downstream process that echoes back a response.
	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()
	defer stdinR.Close()
	defer stdoutW.Close()

	p := &Proxy{stdin: stdinW, stdout: stdoutR, stdoutBuf: bufio.NewReader(stdoutR)}

	// Downstream goroutine: read request, write response.
	done := make(chan struct{})
	go func() {
		defer close(done)
		// Read the request
		msg, err := lsp.ReadMessage(bufio.NewReader(stdinR))
		if err != nil {
			return
		}
		// Write a JSON-RPC response back
		resp := []byte(`{"jsonrpc":"2.0","id":1,"result":{}}`)
		lsp.WriteMessage(stdoutW, resp)
		_ = msg
	}()

	req := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`)
	resp, notifs, err := p.CallDownstream(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if len(notifs) != 0 {
		t.Fatalf("expected 0 notifications, got %d", len(notifs))
	}

	// Wait for goroutine to finish
	<-done
}

func TestCallDownstream_notificationThenResponse(t *testing.T) {
	// Simulate downstream sending a notification before the response.
	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()
	defer stdinR.Close()
	defer stdoutW.Close()

	p := &Proxy{stdin: stdinW, stdout: stdoutR, stdoutBuf: bufio.NewReader(stdoutR)}

	done := make(chan struct{})
	go func() {
		defer close(done)
		msg, err := lsp.ReadMessage(bufio.NewReader(stdinR))
		if err != nil {
			return
		}
		// Write a notification first, then the response
		notif := []byte(`{"jsonrpc":"2.0","method":"window/logMessage","params":{}}`)
		lsp.WriteMessage(stdoutW, notif)
		resp := []byte(`{"jsonrpc":"2.0","id":1,"result":{}}`)
		lsp.WriteMessage(stdoutW, resp)
		_ = msg
	}()

	req := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`)
	resp, notifs, err := p.CallDownstream(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if len(notifs) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(notifs))
	}

	<-done
}

// mockHandler implements proxy.Handler for tests.
type mockHandler struct {
	handled   bool
	response  []byte
	notifs    [][]byte
	notifCh   chan []byte
	closeCh   chan struct{}
}

func (m *mockHandler) HandleMethod(method string, content []byte) (bool, []byte, [][]byte, error) {
	return m.handled, m.response, m.notifs, nil
}

func (m *mockHandler) Notifications() <-chan []byte {
	return m.notifCh
}

func (m *mockHandler) Close() {
	close(m.closeCh)
}

func TestRun_handlerHandled(t *testing.T) {
	// Simulate client sending a message that the handler handles.
	clientInR, clientInW := io.Pipe()
	clientOutR, clientOutW := io.Pipe()
	defer clientInR.Close()
	defer clientOutW.Close()

	notifCh := make(chan []byte)
	closeCh := make(chan struct{})
	handler := &mockHandler{
		handled:  true,
		response: []byte(`{"jsonrpc":"2.0","id":1,"result":{}}`),
		notifCh:  notifCh,
		closeCh:  closeCh,
	}

	p := NewNop()

	done := make(chan error, 1)
	go func() {
		done <- p.Run(clientInR, clientOutW, handler)
	}()

	// Send a request
	req := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`)
	if err := lsp.WriteMessage(clientInW, req); err != nil {
		t.Fatalf("write request: %v", err)
	}

	// Read the response from client stdout
	resp, err := lsp.ReadMessage(bufio.NewReader(clientOutR))
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}

	// Close the input to stop Run
	clientInW.Close()

	// Wait for Run to finish
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not exit")
	}
}

func TestRun_handlerForward(t *testing.T) {
	// Simulate client sending a message that gets forwarded to downstream.
	clientInR, clientInW := io.Pipe()
	clientOutR, clientOutW := io.Pipe()
	defer clientInR.Close()
	defer clientOutW.Close()

	// Create downstream pipes
	downstreamInR, downstreamInW := io.Pipe()
	downstreamOutR, downstreamOutW := io.Pipe()
	defer downstreamInR.Close()
	defer downstreamOutW.Close()

	notifCh := make(chan []byte)
	closeCh := make(chan struct{})
	handler := &mockHandler{
		handled: false,
		notifCh: notifCh,
		closeCh: closeCh,
	}

	p := &Proxy{stdin: downstreamInW, stdout: downstreamOutR, stdoutBuf: bufio.NewReader(downstreamOutR)}

	done := make(chan error, 1)
	go func() {
		done <- p.Run(clientInR, clientOutW, handler)
	}()

	// Downstream goroutine: read request, write response
	go func() {
		msg, err := lsp.ReadMessage(bufio.NewReader(downstreamInR))
		if err != nil {
			return
		}
		resp := []byte(`{"jsonrpc":"2.0","id":1,"result":{"capabilities":{}}}`)
		lsp.WriteMessage(downstreamOutW, resp)
		_ = msg
	}()

	// Send a request
	req := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`)
	if err := lsp.WriteMessage(clientInW, req); err != nil {
		t.Fatalf("write request: %v", err)
	}

	// Read the response from client stdout
	resp, err := lsp.ReadMessage(bufio.NewReader(clientOutR))
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}

	// Close the input to stop Run
	clientInW.Close()

	// Wait for Run to finish
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not exit")
	}
}

func TestRun_eof(t *testing.T) {
	clientInR, clientInW := io.Pipe()
	_, clientOutW := io.Pipe()
	defer clientInR.Close()
	defer clientOutW.Close()

	notifCh := make(chan []byte)
	closeCh := make(chan struct{})
	handler := &mockHandler{
		notifCh: notifCh,
		closeCh: closeCh,
	}

	p := NewNop()

	done := make(chan error, 1)
	go func() {
		done <- p.Run(clientInR, clientOutW, handler)
	}()

	// Close input immediately to trigger EOF
	clientInW.Close()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not exit on EOF")
	}
}
