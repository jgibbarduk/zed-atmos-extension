package handler

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/jamesgibbard/zed-atmos-language/lsp-bridge/internal/index"
)

type DownstreamCaller interface {
	CallDownstream(content []byte) ([]byte, error)
	SendNotification(content []byte) error
}

type textDocumentPositionParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
	Position struct {
		Line      uint32 `json:"line"`
		Character uint32 `json:"character"`
	} `json:"position"`
}

type textDocumentParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
}

type LSPHandler struct {
	idx                *index.Index
	downstream         DownstreamCaller
	initialized         bool
	diagnosticsDisabled bool
}

func New(idx *index.Index, downstream DownstreamCaller) *LSPHandler {
	return &LSPHandler{
		idx:        idx,
		downstream: downstream,
	}
}

func (h *LSPHandler) HandleMethod(method string, content []byte) (bool, []byte, [][]byte, error) {
	if method == "initialize" {
		return h.handleInitialize(content)
	}

	if method == "initialized" {
		return h.handleInitialized(content)
	}

	if method == "shutdown" {
		h.initialized = false
		downstreamResp, err := h.downstream.CallDownstream(content)
		if err != nil {
			log.Printf("shutdown: forward to atmos failed: %v", err)
		}
		if downstreamResp != nil {
			return true, downstreamResp, nil, nil
		}
		return true, nil, nil, nil
	}

	if method == "exit" {
		h.downstream.SendNotification(content)
		return true, nil, nil, nil
	}

	if method == "textDocument/definition" {
		return h.handleDefinition(content)
	}

	if method == "textDocument/references" {
		return h.handleReferences(content)
	}

	if method == "textDocument/hover" {
		return h.handleHover(content)
	}

	if method == "textDocument/didOpen" || method == "textDocument/didChange" || method == "textDocument/didSave" {
		return h.handleDiagnostics(content)
	}

	return false, nil, nil, nil
}

func (h *LSPHandler) handleInitialize(content []byte) (bool, []byte, [][]byte, error) {
	var req struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Params  json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(content, &req); err != nil {
		return true, errorResponse(content, -32602, "Invalid params"), nil, nil
	}

	var params struct {
		RootPath string `json:"rootPath"`
		RootURI  string `json:"rootUri"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		log.Printf("initialize unmarshal: %v", err)
	}

	var rootPath string
	if params.RootPath != "" {
		rootPath = params.RootPath
	} else if params.RootURI != "" {
		rootPath = strings.TrimPrefix(params.RootURI, "file://")
	}

	if rootPath != "" {
		stacksPath := resolveStacksPath(rootPath)
		h.idx.SetBasePath(stacksPath)
	}

	downstreamResp, err := h.downstream.CallDownstream(content)
	if err != nil {
		log.Printf("initialize: downstream atmos lsp failed: %v — returning bridge-only capabilities", err)
	}

	bridgeCaps := map[string]interface{}{
		"definitionProvider": true,
		"referencesProvider": true,
		"hoverProvider":      true,
		"completionProvider": map[string]interface{}{
			"resolveProvider":   false,
			"triggerCharacters": []string{".", ":", "/"},
		},
		"textDocumentSync": map[string]interface{}{
			"openClose": true,
			"change":    1,
		},
	}

	if downstreamResp != nil {
		var downstreamResult struct {
			Result struct {
				Capabilities map[string]interface{} `json:"capabilities"`
			} `json:"result"`
		}
		if err := json.Unmarshal(downstreamResp, &downstreamResult); err == nil && downstreamResult.Result.Capabilities != nil {
			for k, v := range downstreamResult.Result.Capabilities {
				bridgeCaps[k] = v
			}
		}
	}

	result := map[string]interface{}{
		"capabilities": bridgeCaps,
		"serverInfo": map[string]interface{}{
			"name":    "atmos-lsp-bridge",
			"version": "0.1.0",
		},
	}

	resultBytes, _ := json.Marshal(result)
	resp := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      req.ID,
		"result":  json.RawMessage(resultBytes),
	}
	respBytes, _ := json.Marshal(resp)
	return true, respBytes, nil, nil
}

func (h *LSPHandler) handleInitialized(content []byte) (bool, []byte, [][]byte, error) {
	go h.idx.Reindex()
	if err := h.idx.StartWatching(func() {
		h.idx.Reindex()
	}); err != nil {
		log.Printf("file watcher start failed: %v", err)
	}
	h.initialized = true
	if err := h.downstream.SendNotification(content); err != nil {
		log.Printf("initialized: forward to atmos failed: %v", err)
	}
	return false, nil, nil, nil
}

func (h *LSPHandler) handleDefinition(content []byte) (bool, []byte, [][]byte, error) {
	var req struct {
		JSONRPC string                     `json:"jsonrpc"`
		ID      json.RawMessage            `json:"id"`
		Params  textDocumentPositionParams `json:"params"`
	}
	if err := json.Unmarshal(content, &req); err != nil {
		return true, errorResponse(content, -32602, "Invalid params"), nil, nil
	}

	path := strings.TrimPrefix(req.Params.TextDocument.URI, "file://")
	f := h.idx.GetFile(path)
	if f == nil {
		return true, emptyResult(content, req.ID), nil, nil
	}

	var locations []map[string]interface{}
	for _, imp := range f.Imports {
		if imp.Range.StartLine <= req.Params.Position.Line && imp.Range.EndLine >= req.Params.Position.Line {
			resolved := h.idx.ResolveImport(imp.RawPath, filepath.Dir(path))
			for _, r := range resolved {
				locations = append(locations, map[string]interface{}{
					"uri": "file://" + r,
					"range": map[string]interface{}{
						"start": map[string]uint32{"line": 0, "character": 0},
						"end":   map[string]uint32{"line": 0, "character": 0},
					},
				})
			}
		}
	}
	if locations == nil {
		locations = []map[string]interface{}{}
	}

	resultBytes, _ := json.Marshal(locations)
	resp := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      req.ID,
		"result":  json.RawMessage(resultBytes),
	}
	b, _ := json.Marshal(resp)
	return true, b, nil, nil
}

func (h *LSPHandler) handleReferences(content []byte) (bool, []byte, [][]byte, error) {
	var req struct {
		JSONRPC string                     `json:"jsonrpc"`
		ID      json.RawMessage            `json:"id"`
		Params  textDocumentPositionParams `json:"params"`
	}
	if err := json.Unmarshal(content, &req); err != nil {
		return true, errorResponse(content, -32602, "Invalid params"), nil, nil
	}

	path := strings.TrimPrefix(req.Params.TextDocument.URI, "file://")
	f := h.idx.GetFile(path)
	if f == nil {
		return true, emptyResult(content, req.ID), nil, nil
	}

	var locations []map[string]interface{}
	for _, imp := range f.Imports {
		if imp.Range.StartLine <= req.Params.Position.Line && imp.Range.EndLine >= req.Params.Position.Line {
			refs := h.idx.FindImporters(imp.RawPath)
			for _, r := range refs {
				refFile := h.idx.GetFile(r)
				if refFile != nil {
					for _, refImp := range refFile.Imports {
						if refImp.RawPath == imp.RawPath {
							locations = append(locations, map[string]interface{}{
								"uri": "file://" + r,
								"range": map[string]interface{}{
									"start": map[string]uint32{"line": refImp.Range.StartLine, "character": refImp.Range.StartChar},
									"end":   map[string]uint32{"line": refImp.Range.EndLine, "character": refImp.Range.EndChar},
								},
							})
						}
					}
				}
			}
		}
	}

	for _, comp := range f.Comps {
		if comp.Range.StartLine <= req.Params.Position.Line && comp.Range.EndLine >= req.Params.Position.Line {
			refs := h.idx.FindComponent(comp.Name)
			for _, sf := range refs {
				if sf.Path != path {
					for _, c := range sf.Comps {
						if c.Name == comp.Name {
							locations = append(locations, map[string]interface{}{
								"uri": "file://" + sf.Path,
								"range": map[string]interface{}{
									"start": map[string]uint32{"line": c.Range.StartLine, "character": c.Range.StartChar},
									"end":   map[string]uint32{"line": c.Range.EndLine, "character": c.Range.EndChar},
								},
							})
						}
					}
				}
			}
		}
	}

	if locations == nil {
		locations = []map[string]interface{}{}
	}

	resultBytes, _ := json.Marshal(locations)
	resp := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      req.ID,
		"result":  json.RawMessage(resultBytes),
	}
	b, _ := json.Marshal(resp)
	return true, b, nil, nil
}

func (h *LSPHandler) handleHover(content []byte) (bool, []byte, [][]byte, error) {
	var req struct {
		JSONRPC string                     `json:"jsonrpc"`
		ID      json.RawMessage            `json:"id"`
		Params  textDocumentPositionParams `json:"params"`
	}
	if err := json.Unmarshal(content, &req); err != nil {
		return true, errorResponse(content, -32602, "Invalid params"), nil, nil
	}

	path := strings.TrimPrefix(req.Params.TextDocument.URI, "file://")
	f := h.idx.GetFile(path)

	var hoverContent map[string]interface{}

	if f != nil {
		for _, imp := range f.Imports {
			if imp.Range.StartLine <= req.Params.Position.Line && imp.Range.EndLine >= req.Params.Position.Line {
				resolved := h.idx.ResolveImport(imp.RawPath, filepath.Dir(path))
				value := fmt.Sprintf("**Import:** `%s`\n\n", imp.RawPath)
				if len(resolved) > 0 {
					value += "**Resolves to:**\n"
					for _, r := range resolved {
						value += fmt.Sprintf("- `%s`\n", r)
					}
				} else {
					value += "*Unable to resolve path*"
				}
				hoverContent = map[string]interface{}{
					"kind":  "markdown",
					"value": value,
				}
				break
			}
		}
		if hoverContent == nil {
			for _, comp := range f.Comps {
				if comp.Range.StartLine <= req.Params.Position.Line && comp.Range.EndLine >= req.Params.Position.Line {
					inheritors := h.idx.FindComponent(comp.Name)
					value := fmt.Sprintf("**Component:** `%s`\n\n", comp.Name)
					if len(inheritors) > 1 {
						value += "**Also defined in:**\n"
						for _, sf := range inheritors {
							if sf.Path != path {
								value += fmt.Sprintf("- `%s`\n", sf.Path)
							}
						}
					}
					hoverContent = map[string]interface{}{
						"kind":  "markdown",
						"value": value,
					}
					break
				}
			}
		}
	}

	result := map[string]interface{}{
		"contents": hoverContent,
	}

	resultBytes, _ := json.Marshal(result)
	resp := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      req.ID,
		"result":  json.RawMessage(resultBytes),
	}
	b, _ := json.Marshal(resp)
	return true, b, nil, nil
}

func (h *LSPHandler) handleDiagnostics(content []byte) (bool, []byte, [][]byte, error) {
	if h.diagnosticsDisabled {
		return false, nil, nil, nil
	}

	var req struct {
		JSONRPC string          `json:"jsonrpc"`
		Method  string          `json:"method"`
		Params  textDocumentParams `json:"params"`
	}
	if err := json.Unmarshal(content, &req); err != nil {
		return true, nil, nil, nil
	}

	path := strings.TrimPrefix(req.Params.TextDocument.URI, "file://")
	f := h.idx.GetFile(path)
	if f == nil {
		return false, nil, nil, nil
	}

	if req.Method == "textDocument/didSave" {
		h.idx.ReindexFile(path)
	}

	diags := runBestPracticeChecks(f, filepath.Dir(path), h.idx)

	notification := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "textDocument/publishDiagnostics",
		"params": map[string]interface{}{
			"uri":         req.Params.TextDocument.URI,
			"diagnostics": diags,
		},
	}
	notifBytes, _ := json.Marshal(notification)
	return false, nil, [][]byte{notifBytes}, nil
}

func resolveStacksPath(rootPath string) string {
	atmosYAML := filepath.Join(rootPath, "atmos.yaml")
	basePath := parseAtmosBasePath(atmosYAML)
	if basePath == "" {
		log.Printf("no atmos.yaml at %s or no stacks.base_path, defaulting to stacks/", rootPath)
		return filepath.Join(rootPath, "stacks")
	}
	return filepath.Join(rootPath, basePath)
}

func parseAtmosBasePath(atmosYAMLPath string) string {
	content, err := os.ReadFile(atmosYAMLPath)
	if err != nil {
		return ""
	}

	lines := strings.Split(string(content), "\n")
	inStacks := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "stacks:") {
			inStacks = true
			continue
		}
		if inStacks && strings.HasPrefix(trimmed, "base_path:") {
			parts := strings.SplitN(trimmed, ":", 2)
			if len(parts) == 2 {
				val := strings.TrimSpace(parts[1])
				val = strings.Trim(val, "\"'")
				return val
			}
			return ""
		}
		if inStacks && trimmed != "" && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			return ""
		}
	}
	return ""
}

func errorResponse(content []byte, code int, message string) []byte {
	return buildErrorResponse(content, code, message)
}

func emptyResult(content []byte, id json.RawMessage) []byte {
	resp := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  []interface{}{},
	}
	b, _ := json.Marshal(resp)
	return b
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
