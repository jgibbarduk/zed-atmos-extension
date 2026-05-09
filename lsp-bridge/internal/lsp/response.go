package lsp

import (
	"encoding/json"
)

// BuildErrorResponse creates a JSON-RPC error response from the original request content.
func BuildErrorResponse(content []byte, code int, message string) []byte {
	var req struct {
		ID json.RawMessage `json:"id"`
	}
	if err := json.Unmarshal(content, &req); err != nil {
		// Malformed JSON — return a generic error with no ID.
		b, _ := json.Marshal(map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      nil,
			"error": map[string]interface{}{
				"code":    code,
				"message": message,
			},
		})
		return b
	}

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
