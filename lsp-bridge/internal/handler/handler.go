package handler

import (
	"encoding/json"
	"log"
	"strings"

	"github.com/jamesgibbard/zed-atmos-language/lsp-bridge/internal/index"
)

type LSPHandler struct {
	idx         *index.Index
	initialized bool
}

func New(idx *index.Index) *LSPHandler {
	return &LSPHandler{idx: idx}
}

func (h *LSPHandler) HandleMethod(method string, content []byte) (bool, []byte, error) {
	if method == "initialize" {
		return h.handleInitialize(content)
	}

	if method == "initialized" {
		go h.idx.Reindex()
		h.initialized = true
		return true, nil, nil
	}

	if method == "shutdown" {
		h.initialized = false
		return false, nil, nil
	}

	interceptedMethods := map[string]bool{
		"textDocument/definition":          true,
		"textDocument/references":          true,
		"textDocument/semanticTokens/full": true,
	}

	if interceptedMethods[method] {
		return false, nil, nil
	}

	return false, nil, nil
}

func (h *LSPHandler) handleInitialize(content []byte) (bool, []byte, error) {
	var req struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Params  json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(content, &req); err != nil {
		return true, nil, err
	}

	var params struct {
		RootPath string `json:"rootPath"`
		RootURI  string `json:"rootUri"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		log.Printf("initialize unmarshal: %v", err)
	}

	if params.RootPath != "" {
		h.idx.SetBasePath(params.RootPath + "/stacks")
	} else if params.RootURI != "" {
		path := strings.TrimPrefix(params.RootURI, "file://")
		if path != "" {
			h.idx.SetBasePath(path + "/stacks")
		}
	}

	result := map[string]interface{}{
		"capabilities": map[string]interface{}{
			"definitionProvider": true,
			"referencesProvider": true,
			"hoverProvider":      true,
			"completionProvider": map[string]interface{}{
				"resolveProvider":   false,
				"triggerCharacters": []string{".", ":", "/"},
			},
			"semanticTokensProvider": map[string]interface{}{
				"full":  true,
				"range": false,
				"legend": map[string]interface{}{
					"tokenTypes": []string{
						"keyword", "function", "type", "macro", "string", "variable", "comment",
					},
					"tokenModifiers": []string{},
				},
			},
			"textDocumentSync": map[string]interface{}{
				"openClose": true,
				"change":    1,
			},
		},
	}

	resultBytes, _ := json.Marshal(result)

	resp := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      req.ID,
		"result":  json.RawMessage(resultBytes),
	}

	respBytes, _ := json.Marshal(resp)
	return true, respBytes, nil
}
