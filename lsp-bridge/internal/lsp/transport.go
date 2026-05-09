package lsp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

type Message struct {
	Content []byte
}

func ReadMessage(r *bufio.Reader) (*Message, error) {
	var length int
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("read header: %w", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if strings.HasPrefix(line, "Content-Length: ") {
			length, err = strconv.Atoi(strings.TrimPrefix(line, "Content-Length: "))
			if err != nil {
				return nil, fmt.Errorf("parse Content-Length: %w", err)
			}
		}
	}
	if length == 0 {
		return nil, fmt.Errorf("missing Content-Length header")
	}
	content := make([]byte, length)
	if _, err := io.ReadFull(r, content); err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	return &Message{Content: content}, nil
}

func WriteMessage(w io.Writer, content []byte) error {
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(content))
	if _, err := w.Write([]byte(header)); err != nil {
		return err
	}
	if _, err := w.Write(content); err != nil {
		return err
	}
	return nil
}

func IsNotification(content []byte) bool {
	var msg struct {
		ID json.RawMessage `json:"id"`
	}
	if err := json.Unmarshal(content, &msg); err != nil {
		return false
	}
	return msg.ID == nil
}

func ParseMethod(content []byte) string {
	var msg struct {
		Method string `json:"method"`
	}
	if err := json.Unmarshal(content, &msg); err != nil {
		return ""
	}
	return msg.Method
}

// IsRequest returns true if the message is a request from server to client.
// Requests have both an "id" and a "method" field. Notifications have "method"
// but no "id". Responses have "id" but no "method".
func IsRequest(content []byte) bool {
	var msg struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
	}
	if err := json.Unmarshal(content, &msg); err != nil {
		return false
	}
	return msg.ID != nil && msg.Method != ""
}
