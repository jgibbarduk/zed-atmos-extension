package proxy

import (
	"bytes"
	"io"
	"strings"
	"testing"
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
