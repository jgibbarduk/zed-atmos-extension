package lsp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

// errorWriter is an io.Writer that always returns a fixed error.
type errorWriter struct{ err error }

func (w *errorWriter) Write([]byte) (int, error) { return 0, w.err }

func TestReadMessage(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantBody  string
		wantErr   bool
		errSubstr string
	}{
		{
			name:     "valid message with Content-Length",
			input:    "Content-Length: 12\r\n\r\n{\"a\":\"body\"}",
			wantBody: `{"a":"body"}`,
		},
		{
			name:     "multiple headers",
			input:    "Content-Type: application/vscode-jsonrpc; charset=utf-8\r\nContent-Length: 5\r\n\r\nhello",
			wantBody: "hello",
		},
		{
			name:      "missing Content-Length",
			input:     "Content-Type: text/plain\r\n\r\nbody",
			wantErr:   true,
			errSubstr: "missing Content-Length",
		},
		{
			name:      "EOF during header",
			input:     "Content-Length: 5",
			wantErr:   true,
			errSubstr: "read header",
		},
		{
			name:      "EOF during body",
			input:     "Content-Length: 10\r\n\r\nshort",
			wantErr:   true,
			errSubstr: "read body",
		},
		{
			name:     "LF-only headers",
			input:    "Content-Length: 5\n\nhello",
			wantBody: "hello",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			br := bufio.NewReader(strings.NewReader(tt.input))
			msg, err := ReadMessage(br)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if tt.errSubstr != "" && !strings.Contains(err.Error(), tt.errSubstr) {
					t.Fatalf("error %q does not contain %q", err.Error(), tt.errSubstr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if string(msg.Content) != tt.wantBody {
				t.Fatalf("body = %q, want %q", msg.Content, tt.wantBody)
			}
		})
	}
}

func TestWriteMessage(t *testing.T) {
	t.Run("writes correct header and body", func(t *testing.T) {
		var buf bytes.Buffer
		body := []byte(`{"jsonrpc":"2.0"}`)
		if err := WriteMessage(&buf, body); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		got := buf.String()
		want := "Content-Length: 17\r\n\r\n{\"jsonrpc\":\"2.0\"}"
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	t.Run("write error propagation", func(t *testing.T) {
		wantErr := errors.New("boom")
		w := &errorWriter{err: wantErr}
		if err := WriteMessage(w, []byte("x")); !errors.Is(err, wantErr) {
			t.Fatalf("expected error %v, got %v", wantErr, err)
		}
	})
}

func TestIsNotification(t *testing.T) {
	tests := []struct {
		name    string
		content []byte
		want    bool
	}{
		{"no id", []byte(`{"method":"initialized"}`), true},
		{"id present", []byte(`{"id":1,"method":"initialize"}`), false},
		{"invalid JSON", []byte(`{`), false},
		{"id null", []byte(`{"id":null,"method":"x"}`), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsNotification(tt.content); got != tt.want {
				t.Fatalf("IsNotification(%q) = %v, want %v", tt.content, got, tt.want)
			}
		})
	}
}

func TestParseMethod(t *testing.T) {
	tests := []struct {
		name    string
		content []byte
		want    string
	}{
		{"method present", []byte(`{"method":"textDocument/hover"}`), "textDocument/hover"},
		{"missing method", []byte(`{"id":1}`), ""},
		{"invalid JSON", []byte(`{`), ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ParseMethod(tt.content); got != tt.want {
				t.Fatalf("ParseMethod(%q) = %q, want %q", tt.content, got, tt.want)
			}
		})
	}
}

func TestIsRequest(t *testing.T) {
	tests := []struct {
		name    string
		content []byte
		want    bool
	}{
		{"id and method", []byte(`{"id":1,"method":"initialize"}`), true},
		{"method only", []byte(`{"method":"initialized"}`), false},
		{"id only", []byte(`{"id":1,"result":true}`), false},
		{"invalid JSON", []byte(`{`), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsRequest(tt.content); got != tt.want {
				t.Fatalf("IsRequest(%q) = %v, want %v", tt.content, got, tt.want)
			}
		})
	}
}

func TestBuildErrorResponse(t *testing.T) {
	tests := []struct {
		name       string
		content    []byte
		code       int
		msg        string
		wantID     interface{}
		wantCode   int
		wantMsg    string
		wantJSONRP string
	}{
		{
			name:       "valid JSON with id",
			content:    []byte(`{"jsonrpc":"2.0","id":42,"method":"x"}`),
			code:       -32600,
			msg:        "Invalid Request",
			wantID:     float64(42),
			wantCode:   -32600,
			wantMsg:    "Invalid Request",
			wantJSONRP: "2.0",
		},
		{
			name:       "invalid JSON (no id)",
			content:    []byte(`{`),
			code:       -32700,
			msg:        "Parse error",
			wantID:     nil,
			wantCode:   -32700,
			wantMsg:    "Parse error",
			wantJSONRP: "2.0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := BuildErrorResponse(tt.content, tt.code, tt.msg)

			var result map[string]interface{}
			if err := json.Unmarshal(resp, &result); err != nil {
				t.Fatalf("invalid JSON response: %v", err)
			}

			if result["jsonrpc"] != tt.wantJSONRP {
				t.Fatalf("jsonrpc = %v, want %v", result["jsonrpc"], tt.wantJSONRP)
			}
			if result["id"] != tt.wantID {
				t.Fatalf("id = %v, want %v", result["id"], tt.wantID)
			}

			errObj, ok := result["error"].(map[string]interface{})
			if !ok {
				t.Fatalf("error field missing or not object")
			}
			if errObj["code"] != float64(tt.wantCode) {
				t.Fatalf("error.code = %v, want %v", errObj["code"], tt.wantCode)
			}
			if errObj["message"] != tt.wantMsg {
				t.Fatalf("error.message = %v, want %v", errObj["message"], tt.wantMsg)
			}
		})
	}
}

// TestReadMessage_pipe verifies ReadMessage works with an io.Pipe, simulating
// a real streaming transport.
func TestReadMessage_pipe(t *testing.T) {
	pr, pw := io.Pipe()
	br := bufio.NewReader(pr)

	done := make(chan struct{})
	var msg *Message
	var err error
	go func() {
		msg, err = ReadMessage(br)
		close(done)
	}()

	go func() {
		pw.Write([]byte("Content-Length: 4\r\n\r\nbody"))
		pw.Close()
	}()

	<-done
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(msg.Content) != "body" {
		t.Fatalf("body = %q, want %q", msg.Content, "body")
	}
}
