package handler

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jgibbarduk/zed-atmos-extension/lsp-bridge/internal/index"
	"github.com/jgibbarduk/zed-atmos-extension/lsp-bridge/internal/lsp"
	"gopkg.in/yaml.v3"
)

const (
	completionItemVariable = lsp.CompletionItemKindVariable
	completionItemFile     = lsp.CompletionItemKindFile
	completionItemFolder   = lsp.CompletionItemKindFolder

	// diagDebounce is the delay before recomputing diagnostics after an edit.
	// 300 ms strikes a balance between responsiveness and avoiding redundant
	// work during rapid keystrokes.
	diagDebounce = 300 * time.Millisecond
)

type downstreamCaller interface {
	CallDownstream(content []byte) (response []byte, notifications [][]byte, err error)
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
	idx                 *index.Index
	downstream          downstreamCaller
	initialized         atomic.Bool
	diagnosticsDisabled bool
	projectRoot         string
	nameTemplate        string
	documentContent     map[string][]byte
	documentContentMu   sync.RWMutex

	// Async diagnostics
	notificationsCh  chan []byte
	diagTimer        *time.Timer
	diagMu           sync.Mutex
	diagPendingURI   string
	diagPendingPath  string
	closed           atomic.Bool
	closeMu          sync.Mutex
	closeOnce        sync.Once
}

func New(idx *index.Index, downstream downstreamCaller) *LSPHandler {
	if idx == nil {
		panic("handler.New: idx is nil")
	}
	return &LSPHandler{
		idx:             idx,
		downstream:      downstream,
		documentContent: make(map[string][]byte),
		notificationsCh: make(chan []byte, 16),
	}
}

// Notifications returns the channel for async notifications from the handler.
func (h *LSPHandler) Notifications() <-chan []byte {
	return h.notificationsCh
}

// Close shuts down the handler, stopping any pending timers and closing the
// notification channel so the proxy goroutine can exit cleanly.
func (h *LSPHandler) Close() {
	h.closed.Store(true)
	h.diagMu.Lock()
	if h.diagTimer != nil {
		h.diagTimer.Stop()
	}
	h.diagMu.Unlock()
	h.closeOnce.Do(func() {
		close(h.notificationsCh)
	})
}

func (h *LSPHandler) HandleMethod(method string, content []byte) (bool, []byte, [][]byte, error) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("PANIC in HandleMethod(%s): %v", method, r)
		}
	}()

	switch method {
	case "initialize":
		return h.handleInitialize(content)
	case "initialized":
		return h.handleInitialized(content)
	case "shutdown":
		h.initialized.Store(false)
		downstreamResp, _, err := h.downstream.CallDownstream(content)
		if err != nil {
			log.Printf("shutdown: forward to atmos failed: %v", err)
		}
		if downstreamResp != nil {
			return true, downstreamResp, nil, nil
		}
		// Always return a response so Zed doesn't hang waiting.
		return true, nullResult(content), nil, nil
	case "exit":
		h.downstream.SendNotification(content)
		return true, nil, nil, nil
	case "textDocument/definition":
		return h.handleDefinition(content)
	case "textDocument/references":
		return h.handleReferences(content)
	case "textDocument/hover":
		start := time.Now()
		handled, resp, notifs, err := h.handleHover(content)
		if dur := time.Since(start); dur > 100*time.Millisecond {
			log.Printf("SLOW hover: %v", dur)
		}
		return handled, resp, notifs, err
	case "textDocument/rename":
		return h.handleRename(content)
	case "textDocument/codeAction":
		return h.handleCodeAction(content)
	case "textDocument/completion":
		return h.handleCompletion(content)
	case "textDocument/didOpen", "textDocument/didChange", "textDocument/didSave", "textDocument/didClose":
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

	// Parse initialization_options and merge with defaults.
	cfg := defaultConfig()
	var initOpts struct {
		InitializationOptions config `json:"initializationOptions"`
	}
	if err := json.Unmarshal(req.Params, &initOpts); err != nil {
		log.Printf("initialize: unmarshal init options: %v", err)
	}

	if initOpts.InitializationOptions.StacksPath != "" {
		cfg.StacksPath = initOpts.InitializationOptions.StacksPath
	}
	if initOpts.InitializationOptions.DiagnosticsEnabled != nil {
		cfg.DiagnosticsEnabled = initOpts.InitializationOptions.DiagnosticsEnabled
	}
	if initOpts.InitializationOptions.LogLevel != "" {
		cfg.LogLevel = initOpts.InitializationOptions.LogLevel
	}

	if rootPath != "" {
		h.projectRoot = rootPath
		basePath, nameTemplate := parseAtmosConfig(rootPath)
		if cfg.StacksPath != "" {
			if filepath.IsAbs(cfg.StacksPath) {
				h.idx.SetBasePath(cfg.StacksPath)
			} else {
				h.idx.SetBasePath(filepath.Join(rootPath, cfg.StacksPath))
			}
		} else if basePath != "" {
			h.idx.SetBasePath(filepath.Join(rootPath, basePath))
		} else {
			h.idx.SetBasePath(resolveStacksPath(rootPath))
		}
		h.nameTemplate = nameTemplate
	}

	if cfg.DiagnosticsEnabled != nil && !*cfg.DiagnosticsEnabled {
		h.diagnosticsDisabled = true
	}

	downstreamResp, _, err := h.downstream.CallDownstream(content)
	if err != nil {
		log.Printf("initialize: downstream atmos lsp failed: %v — returning bridge-only capabilities", err)
	}

	bridgeCaps := map[string]interface{}{
		"definitionProvider": true,
		"referencesProvider": true,
		"renameProvider":     true,
		"hoverProvider":      true,
		"codeActionProvider": true,
		"completionProvider": map[string]interface{}{
			"resolveProvider":   false,
			"triggerCharacters": []string{".", ":", "/", "{"},
		},
		"textDocumentSync": map[string]interface{}{
			"openClose": true,
			"change":    1, // Full document sync — our didChange handler replaces the whole doc
			"save":      map[string]interface{}{"includeText": false},
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

	resultBytes, err := json.Marshal(result)
	if err != nil {
		return true, errorResponse(content, -32603, "Internal error"), nil, nil
	}
	resp := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      req.ID,
		"result":  json.RawMessage(resultBytes),
	}
	respBytes, err := json.Marshal(resp)
	if err != nil {
		return true, errorResponse(content, -32603, "Internal error"), nil, nil
	}
	return true, respBytes, nil, nil
}

func (h *LSPHandler) handleInitialized(content []byte) (bool, []byte, [][]byte, error) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("reindex panic: %v", r)
			}
		}()
		h.idx.Reindex()
		// Publish diagnostics for every file in the workspace so issues
		// appear without requiring each file to be opened individually.
		h.publishWorkspaceDiagnostics()
	}()
	if err := h.idx.StartWatching(func() {
		h.idx.Reindex()
	}); err != nil {
		log.Printf("file watcher start failed: %v", err)
	}
	h.initialized.Store(true)
	if err := h.downstream.SendNotification(content); err != nil {
		log.Printf("initialized: forward to atmos failed: %v", err)
	}
	return true, nil, nil, nil
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

	// Check if cursor is on a metadata.component value
	for _, meta := range f.Metadata {
		if meta.Component != "" && meta.ComponentRange.StartLine <= req.Params.Position.Line && meta.ComponentRange.EndLine >= req.Params.Position.Line {
			refs := h.idx.FindComponent(meta.Component)
			for _, ref := range refs {
				locations = append(locations, map[string]interface{}{
					"uri": "file://" + ref.Path,
					"range": map[string]interface{}{
						"start": map[string]uint32{"line": 0, "character": 0},
						"end":   map[string]uint32{"line": 0, "character": 0},
					},
				})
			}
		}
	}

	// Check if cursor is on a metadata.inherits value
	for _, meta := range f.Metadata {
		if meta.Inherits != "" && meta.InheritsRange.StartLine <= req.Params.Position.Line && meta.InheritsRange.EndLine >= req.Params.Position.Line {
			refs := h.idx.FindComponent(meta.Inherits)
			for _, ref := range refs {
				locations = append(locations, map[string]interface{}{
					"uri": "file://" + ref.Path,
					"range": map[string]interface{}{
						"start": map[string]uint32{"line": 0, "character": 0},
						"end":   map[string]uint32{"line": 0, "character": 0},
					},
				})
			}
		}
	}

	// Check if cursor is on a !terraform.state tag
	for _, ts := range f.TerraformState {
		if ts.Range.StartLine <= req.Params.Position.Line && ts.Range.EndLine >= req.Params.Position.Line {
			if ts.Component == "" {
				continue
			}
			refs := h.idx.FindComponent(ts.Component)
			for _, ref := range refs {
				locations = append(locations, map[string]interface{}{
					"uri": "file://" + ref.Path,
					"range": map[string]interface{}{
						"start": map[string]uint32{"line": 0, "character": 0},
						"end":   map[string]uint32{"line": 0, "character": 0},
					},
				})
			}
		}
	}

	// Check if cursor is on a component name key
	for _, comp := range f.Comps {
		if comp.Range.StartLine <= req.Params.Position.Line && comp.Range.EndLine >= req.Params.Position.Line {
			refs := h.idx.FindComponent(comp.Name)
			for _, ref := range refs {
				locations = append(locations, map[string]interface{}{
					"uri": "file://" + ref.Path,
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

	b, err := buildResponse(req.ID, locations)
	if err != nil {
		return true, errorResponse(content, -32603, "Internal error"), nil, nil
	}
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

	if locations == nil {
		locations = []map[string]interface{}{}
	}

	b, err := buildResponse(req.ID, locations)
	if err != nil {
		return true, errorResponse(content, -32603, "Internal error"), nil, nil
	}
	return true, b, nil, nil
}

func toLSPRange(r index.Range) lsp.Range {
	return lsp.Range{
		Start: lsp.Position{Line: r.StartLine, Character: r.StartChar},
		End:   lsp.Position{Line: r.EndLine, Character: r.EndChar},
	}
}

func (h *LSPHandler) handleRename(content []byte) (bool, []byte, [][]byte, error) {
	var req struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Params  struct {
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
			Position struct {
				Line      uint32 `json:"line"`
				Character uint32 `json:"character"`
			} `json:"position"`
			NewName string `json:"newName"`
		} `json:"params"`
	}
	if err := json.Unmarshal(content, &req); err != nil {
		return true, errorResponse(content, -32602, "Invalid params"), nil, nil
	}

	path := strings.TrimPrefix(req.Params.TextDocument.URI, "file://")
	f := h.idx.GetFile(path)
	if f == nil {
		return true, emptyResult(content, req.ID), nil, nil
	}

	pos := req.Params.Position.Line
	var targetComp string
	for _, comp := range f.Comps {
		if comp.Range.StartLine <= pos && comp.Range.EndLine >= pos {
			targetComp = comp.Name
			break
		}
	}
	if targetComp == "" {
		for _, meta := range f.Metadata {
			if meta.Component != "" && meta.ComponentRange.StartLine <= pos && meta.ComponentRange.EndLine >= pos {
				targetComp = meta.Component
				break
			}
			if meta.Inherits != "" && meta.InheritsRange.StartLine <= pos && meta.InheritsRange.EndLine >= pos {
				targetComp = meta.Inherits
				break
			}
		}
	}
	if targetComp == "" {
		for _, ts := range f.TerraformState {
			if ts.Component != "" && ts.Range.StartLine <= pos && ts.Range.EndLine >= pos {
				targetComp = ts.Component
				break
			}
		}
	}
	if targetComp == "" {
		for _, dep := range f.Deps {
			if dep.Component != "" && dep.Range.StartLine <= pos && dep.Range.EndLine >= pos {
				targetComp = dep.Component
				break
			}
		}
	}
	if targetComp == "" {
		return true, emptyResult(content, req.ID), nil, nil
	}

	editMap := make(map[string][]lsp.TextEdit)

	for _, sf := range h.idx.AllFiles() {
		var edits []lsp.TextEdit

		for _, comp := range sf.Comps {
			if comp.Name == targetComp {
				edits = append(edits, lsp.TextEdit{
					Range:   toLSPRange(comp.Range),
					NewText: req.Params.NewName,
				})
			}
		}

		for _, meta := range sf.Metadata {
			if meta.Component == targetComp {
				edits = append(edits, lsp.TextEdit{
					Range:   toLSPRange(meta.ComponentRange),
					NewText: req.Params.NewName,
				})
			}
			if meta.Inherits == targetComp {
				edits = append(edits, lsp.TextEdit{
					Range:   toLSPRange(meta.InheritsRange),
					NewText: req.Params.NewName,
				})
			}
		}

		for _, ts := range sf.TerraformState {
			if ts.Component == targetComp {
				newText := req.Params.NewName
				if ts.JQExpr != "" {
					newText += " " + ts.JQExpr
				}
				edits = append(edits, lsp.TextEdit{
					Range:   toLSPRange(ts.Range),
					NewText: newText,
				})
			}
		}

		for _, dep := range sf.Deps {
			if dep.Component == targetComp {
				edits = append(edits, lsp.TextEdit{
					Range:   toLSPRange(dep.Range),
					NewText: req.Params.NewName,
				})
			}
		}

		if len(edits) > 0 {
			editMap["file://"+sf.Path] = edits
		}
	}

	result := lsp.WorkspaceEdit{Changes: editMap}
	b, err := buildResponse(req.ID, result)
	if err != nil {
		return true, errorResponse(content, -32603, "Internal error"), nil, nil
	}
	return true, b, nil, nil
}

func (h *LSPHandler) handleCodeAction(content []byte) (bool, []byte, [][]byte, error) {
	var req struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Params  struct {
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
			Range struct {
				Start struct {
					Line      uint32 `json:"line"`
					Character uint32 `json:"character"`
				} `json:"start"`
				End struct {
					Line      uint32 `json:"line"`
					Character uint32 `json:"character"`
				} `json:"end"`
			} `json:"range"`
		} `json:"params"`
	}
	if err := json.Unmarshal(content, &req); err != nil {
		return true, errorResponse(content, -32602, "Invalid params"), nil, nil
	}

	path := strings.TrimPrefix(req.Params.TextDocument.URI, "file://")
	f := h.idx.GetFile(path)
	if f == nil {
		return true, emptyResult(content, req.ID), nil, nil
	}

	var actions []map[string]interface{}

	// Offer "Generate component scaffold" if cursor is on a component name that doesn't exist in catalog
	for _, comp := range f.Comps {
		if comp.Name == "" {
			continue
		}
		if strings.ContainsAny(comp.Name, "/\\") {
			continue
		}
		if comp.Range.StartLine <= req.Params.Range.Start.Line && comp.Range.EndLine >= req.Params.Range.Start.Line {
			catalogPath := filepath.Join(h.idx.BasePath(), "catalog", comp.Name+".yaml")
			if _, err := os.Stat(catalogPath); os.IsNotExist(err) {
				scaffoldContent := fmt.Sprintf("components:\n  terraform:\n    \"%s\":\n      vars: {}\n", comp.Name)
				actions = append(actions, map[string]interface{}{
					"title": "Generate component scaffold in catalog",
					"kind":  "quickfix",
					"edit": map[string]interface{}{
						"changes": map[string]interface{}{
							"file://" + catalogPath: []map[string]interface{}{
								{
									"range": map[string]interface{}{
										"start": map[string]uint32{"line": 0, "character": 0},
										"end":   map[string]uint32{"line": 0, "character": 0},
									},
									"newText": scaffoldContent,
								},
							},
						},
					},
				})
			}
			break
		}
	}

	b, err := buildResponse(req.ID, actions)
	if err != nil {
		return true, errorResponse(content, -32603, "Internal error"), nil, nil
	}
	return true, b, nil, nil
}

func (h *LSPHandler) handleCompletion(content []byte) (bool, []byte, [][]byte, error) {
	var req struct {
		JSONRPC string                     `json:"jsonrpc"`
		ID      json.RawMessage            `json:"id"`
		Params  textDocumentPositionParams `json:"params"`
	}
	if err := json.Unmarshal(content, &req); err != nil {
		return true, errorResponse(content, -32602, "Invalid params"), nil, nil
	}

	path := strings.TrimPrefix(req.Params.TextDocument.URI, "file://")
	basePath := h.idx.BasePath()
	if basePath == "" {
		return true, emptyResult(content, req.ID), nil, nil
	}

	// Read document lines from memory or disk.
	var lines []string
	h.documentContentMu.RLock()
	if docContent, ok := h.documentContent[path]; ok {
		lines = normalizeLines(string(docContent))
	}
	h.documentContentMu.RUnlock()
	if lines == nil {
		if contentBytes, err := os.ReadFile(path); err == nil {
			lines = normalizeLines(string(contentBytes))
		}
	}
	if lines == nil || int(req.Params.Position.Line) >= len(lines) {
		return true, emptyResult(content, req.ID), nil, nil
	}
	lineText := lines[req.Params.Position.Line]

	// Check if the cursor is inside a Go template expression {{ ... }}.
	if partial, startChar := extractTemplatePartial(lineText, int(req.Params.Position.Character)); partial != "" {
		replaceRange := lsp.Range{
			Start: lsp.Position{Line: req.Params.Position.Line, Character: uint32(startChar)},
			End:   lsp.Position{Line: req.Params.Position.Line, Character: req.Params.Position.Character},
		}
		items := findTemplateCompletions(h.idx, path, partial, replaceRange)
		if len(items) > 0 {
			result := map[string]interface{}{
				"isIncomplete": false,
				"items":        items,
			}
			b, err := buildResponse(req.ID, result)
			if err != nil {
				return true, errorResponse(content, -32603, "Internal error"), nil, nil
			}
			return true, b, nil, nil
		}
	}

	// Check if cursor is on a metadata.component line — offer component dirs.
	if isMetadataComponentLine(lineText) {
		partial := extractPartialPath(lineText, int(req.Params.Position.Character))
		partialStartChar := int(req.Params.Position.Character) - len(partial)
		replaceRange := lsp.Range{
			Start: lsp.Position{Line: req.Params.Position.Line, Character: uint32(partialStartChar)},
			End:   lsp.Position{Line: req.Params.Position.Line, Character: req.Params.Position.Character},
		}
		items := findComponentCompletions(basePath, partial, replaceRange)
		if len(items) > 0 {
			result := map[string]interface{}{
				"isIncomplete": len(items) > 20,
				"items":        items,
			}
			b, err := buildResponse(req.ID, result)
			if err != nil {
				return true, errorResponse(content, -32603, "Internal error"), nil, nil
			}
			return true, b, nil, nil
		}
	}

	// Check if cursor is on a component reference inside dependencies or
	// terraform.state — offer known component names.
	if isComponentReferenceContext(lines, int(req.Params.Position.Line)) {
		partial := extractPartialPath(lineText, int(req.Params.Position.Character))
		partialStartChar := int(req.Params.Position.Character) - len(partial)
		replaceRange := lsp.Range{
			Start: lsp.Position{Line: req.Params.Position.Line, Character: uint32(partialStartChar)},
			End:   lsp.Position{Line: req.Params.Position.Line, Character: req.Params.Position.Character},
		}
		items := findDependencyComponentCompletions(h.idx, partial, replaceRange)
		if len(items) > 0 {
			result := map[string]interface{}{
				"isIncomplete": len(items) > 20,
				"items":        items,
			}
			b, err := buildResponse(req.ID, result)
			if err != nil {
				return true, errorResponse(content, -32603, "Internal error"), nil, nil
			}
			return true, b, nil, nil
		}
	}

	// Extract the partial path the user is typing.
	// Look for a quoted or unquoted path fragment on the current line.
	partial := extractPartialPath(lineText, int(req.Params.Position.Character))
	if partial == "" {
		return true, emptyResult(content, req.ID), nil, nil
	}

	// Compute the range of the partial path so the completion can replace it.
	partialStartChar := int(req.Params.Position.Character) - len(partial)
	replaceRange := lsp.Range{
		Start: lsp.Position{Line: req.Params.Position.Line, Character: uint32(partialStartChar)},
		End:   lsp.Position{Line: req.Params.Position.Line, Character: req.Params.Position.Character},
	}

	// Walk the base path and find matching .yaml files.
	items, hasDirs := findPathCompletions(basePath, partial, replaceRange)
	if len(items) == 0 {
		return true, emptyResult(content, req.ID), nil, nil
	}

	// isIncomplete should be true when directories are present so the
	// client keeps re-triggering as the user navigates deeper.
	result := map[string]interface{}{
		"isIncomplete": len(items) > 20 || hasDirs,
		"items":        items,
	}
	b, err := buildResponse(req.ID, result)
	if err != nil {
		return true, errorResponse(content, -32603, "Internal error"), nil, nil
	}
	return true, b, nil, nil
}

// extractPartialPath pulls out the path fragment the user is currently typing.
// It looks backward from the cursor position for the start of a word/path.
func extractPartialPath(line string, cursor int) string {
	if cursor > len(line) {
		cursor = len(line)
	}
	// Find the start of the current token by walking backwards until we hit
	// a character that can't be part of a path.
	start := cursor
	for start > 0 {
		c := line[start-1]
		if c == ' ' || c == '\t' || c == ':' || c == '"' || c == '\'' {
			break
		}
		start--
	}
	// Trim leading quote if present.
	if start < len(line) && (line[start] == '"' || line[start] == '\'') {
		start++
	}
	if start >= cursor {
		return ""
	}
	return line[start:cursor]
}

// extractTemplatePartial detects if the cursor is inside a Go template expression
// (e.g. "{{ .vars.na| }}" where | is the cursor) and returns the partial text
// after the leading dot, plus the character position where that partial starts.
func extractTemplatePartial(line string, cursor int) (string, int) {
	if cursor > len(line) {
		cursor = len(line)
	}
	// Find the nearest "{{" before the cursor.
	openIdx := strings.LastIndex(line[:cursor], "{{")
	if openIdx == -1 {
		return "", 0
	}
	// If there's a closing "}}" between the opening braces and the cursor,
	// the cursor is after a completed template expression, not inside one.
	if strings.Contains(line[openIdx+2:cursor], "}}") {
		return "", 0
	}
	// Walk forward from {{ to find the dot that starts the template variable.
	// Skip spaces between {{ and the dot.
	i := openIdx + 2
	for i < cursor && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	if i >= cursor || line[i] != '.' {
		// No dot found before cursor — not inside a template variable.
		return "", 0
	}
	// The partial starts right after the dot.
	partialStart := i + 1
	if partialStart > cursor {
		return "", 0
	}
	return line[partialStart:cursor], partialStart
}

// findTemplateCompletions suggests available template variables.
func findTemplateCompletions(idx *index.Index, path, partial string, replaceRange lsp.Range) []map[string]interface{} {
	var items []map[string]interface{}
	f := idx.GetFile(path)
	if f == nil {
		return items
	}
	vars := collectVars(f, idx)

	// Determine what the user is typing.
	// Patterns: "vars.na" -> suggest vars, "atmos_" -> suggest builtins, "" -> suggest everything.
	// partial can be "vars." (no key yet) so use HasPrefix, not equality check.
	switch {
	case partial == "vars." || strings.HasPrefix(partial, "vars."):
		keyPrefix := strings.TrimPrefix(partial, "vars.")
		for k, v := range vars {
			if strings.HasPrefix(k, keyPrefix) {
				label := "vars." + k
				detail := v
				if len(detail) > 40 {
					detail = detail[:37] + "..."
				}
				items = append(items, map[string]interface{}{
					"label":  label,
					"kind":   completionItemVariable,
					"detail": detail,
					"textEdit": map[string]interface{}{
						"range":   replaceRange,
						"newText": label,
					},
				})
			}
		}
	default:
		// Suggest built-ins and vars keys.
		builtins := []string{"atmos_component", "atmos_stack", "atmos_stack_file", "workspace", "component"}
		for _, b := range builtins {
			if strings.HasPrefix(b, partial) {
				items = append(items, map[string]interface{}{
					"label":  b,
					"kind":   completionItemVariable,
					"detail": "Built-in template variable",
					"textEdit": map[string]interface{}{
						"range":   replaceRange,
						"newText": b,
					},
				})
			}
		}
		for k, v := range vars {
			if strings.HasPrefix(k, partial) {
				label := "vars." + k
				detail := v
				if len(detail) > 40 {
					detail = detail[:37] + "..."
				}
				items = append(items, map[string]interface{}{
					"label":  label,
					"kind":   completionItemVariable,
					"detail": detail,
					"textEdit": map[string]interface{}{
						"range":   replaceRange,
						"newText": label,
					},
				})
			}
		}
	}
	// Sort by label for deterministic ordering.
	sort.Slice(items, func(i, j int) bool {
		return items[i]["label"].(string) < items[j]["label"].(string)
	})
	return items
}

// isMetadataComponentLine checks if the current line is inside a metadata.component block.
func isMetadataComponentLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "component:")
}

// findComponentCompletions suggests directories under components/.
func findComponentCompletions(basePath, partial string, replaceRange lsp.Range) []map[string]interface{} {
	componentsDir := filepath.Join(basePath, "components")
	entries, err := os.ReadDir(componentsDir)
	if err != nil {
		return nil
	}
	var items []map[string]interface{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if partial != "" && !strings.HasPrefix(name, partial) {
			continue
		}
		items = append(items, map[string]interface{}{
			"label":  name,
			"kind":   completionItemFolder,
			"detail": "Component",
			"textEdit": map[string]interface{}{
				"range":   replaceRange,
				"newText": name,
			},
		})
	}
	return items
}

// isComponentReferenceContext returns true if the cursor is on a component
// reference inside a dependencies or depends_on block.
func isComponentReferenceContext(lines []string, lineIdx int) bool {
	if lineIdx >= len(lines) {
		return false
	}
	trimmed := strings.TrimSpace(lines[lineIdx])

	// Must have "component:" as a key.
	if !strings.Contains(trimmed, "component:") {
		return false
	}

	// Look backward for dependency context markers.
	for i := lineIdx - 1; i >= 0 && i >= lineIdx-30; i-- {
		t := strings.TrimSpace(lines[i])
		if t == "" {
			continue
		}
		if strings.HasPrefix(t, "dependencies:") || strings.HasPrefix(t, "depends_on:") {
			return true
		}
		if strings.HasPrefix(t, "metadata:") {
			return false
		}
		// A top-level "components:" block means we're inside a component
		// definition, not a dependency reference.
		indent := len(lines[i]) - len(strings.TrimLeft(lines[i], " "))
		if strings.HasPrefix(t, "components:") && indent <= 2 {
			return false
		}
	}
	return false
}

// findDependencyComponentCompletions suggests component names from the index.
func findDependencyComponentCompletions(idx *index.Index, partial string, replaceRange lsp.Range) []map[string]interface{} {
	names := idx.ComponentNames()
	var items []map[string]interface{}
	for _, name := range names {
		if partial != "" && !strings.HasPrefix(name, partial) {
			continue
		}
		items = append(items, map[string]interface{}{
			"label":  name,
			"kind":   completionItemVariable,
			"detail": "Component",
			"textEdit": map[string]interface{}{
				"range":   replaceRange,
				"newText": name,
			},
		})
	}
	return items
}

// findPathCompletions walks the stacks directory and returns matching paths.
// The second return value is true if any returned item is a directory,
// signaling that the completion list may continue deeper.
func findPathCompletions(basePath, partial string, replaceRange lsp.Range) ([]map[string]interface{}, bool) {
	var items []map[string]interface{}
	var hasDirs bool
	// If the user typed "catalog/", look inside basePath/catalog/.
	// If they typed "mixins/region/", look inside basePath/mixins/region/.
	searchDir := filepath.Join(basePath, partial)
	// Prevent directory traversal: ensure resolved path is within basePath.
	cleanSearch, err := filepath.Abs(searchDir)
	if err != nil {
		return nil, false
	}
	cleanBase, err := filepath.Abs(basePath)
	if err != nil {
		return nil, false
	}
	if cleanSearch != cleanBase && !strings.HasPrefix(cleanSearch, cleanBase+string(filepath.Separator)) {
		return nil, false
	}
	info, err := os.Stat(searchDir)
	partialIsDir := err == nil && info.IsDir()
	if !partialIsDir {
		// Partial is not an existing directory; try its parent.
		searchDir = filepath.Join(basePath, filepath.Dir(partial))
		cleanSearch, _ = filepath.Abs(searchDir)
		if cleanSearch != cleanBase && !strings.HasPrefix(cleanSearch, cleanBase+string(filepath.Separator)) {
			return nil, false
		}
		info, err = os.Stat(searchDir)
		if err != nil || !info.IsDir() {
			return nil, false
		}
	}

	entries, err := os.ReadDir(searchDir)
	if err != nil {
		return nil, false
	}

	prefix := ""
	if partial != "" && !partialIsDir && !strings.HasSuffix(partial, "/") && !strings.HasSuffix(partial, string(filepath.Separator)) {
		prefix = filepath.Base(partial)
	}

	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if prefix != "" && !strings.HasPrefix(name, prefix) {
			continue
		}

		rel, _ := filepath.Rel(basePath, filepath.Join(searchDir, name))
		if rel == "" {
			continue
		}
		// Strip .yaml / .yml suffix for cleaner labels.
		label := rel
		if strings.HasSuffix(label, ".yaml") {
			label = strings.TrimSuffix(label, ".yaml")
		} else if strings.HasSuffix(label, ".yml") {
			label = strings.TrimSuffix(label, ".yml")
		}

		// Only show directories and yaml files.
		if entry.IsDir() {
			hasDirs = true
			items = append(items, map[string]interface{}{
				"label":  label + "/",
				"kind":   completionItemFolder,
				"detail": "Directory",
				"textEdit": map[string]interface{}{
					"range":   replaceRange,
					"newText": label + "/",
				},
			})
		} else if strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml") {
			items = append(items, map[string]interface{}{
				"label":  label,
				"kind":   completionItemFile,
				"detail": "Stack file",
				"textEdit": map[string]interface{}{
					"range":   replaceRange,
					"newText": label,
				},
			})
		}
	}
	return items, hasDirs
}

// hoverBuilder assembles structured markdown hover content.
type hoverBuilder struct {
	sections []string
}

func (hb *hoverBuilder) header(title string) {
	hb.sections = append(hb.sections, fmt.Sprintf("## %s", title))
}

func (hb *hoverBuilder) codeBlock(lang, code string) {
	hb.sections = append(hb.sections, fmt.Sprintf("```%s\n%s\n```", lang, code))
}

func (hb *hoverBuilder) inlineCode(code string) {
	hb.sections = append(hb.sections, fmt.Sprintf("`%s`", code))
}

func (hb *hoverBuilder) paragraph(text string) {
	hb.sections = append(hb.sections, text)
}

func (hb *hoverBuilder) bullet(text string) {
	hb.sections = append(hb.sections, fmt.Sprintf("- %s", text))
}

func (hb *hoverBuilder) rule() {
	hb.sections = append(hb.sections, "---")
}

func (hb *hoverBuilder) note(text string) {
	hb.sections = append(hb.sections, fmt.Sprintf("> %s", text))
}

func (hb *hoverBuilder) kv(key, val string) {
	hb.sections = append(hb.sections, fmt.Sprintf("- **%s**: %s", key, val))
}

func (hb *hoverBuilder) yamlBlock(lines []string) {
	hb.sections = append(hb.sections, fmt.Sprintf("```yaml\n%s\n```", strings.Join(lines, "\n")))
}

func (hb *hoverBuilder) build() string {
	return strings.Join(hb.sections, "\n\n")
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

	// Pre-compute vars once per hover request — used by multiple hover targets.
	var vars map[string]string
	if f != nil {
		vars = collectVars(f, h.idx)
	}

	var hoverContent map[string]interface{}

	if f != nil {
		for _, imp := range f.Imports {
			if imp.Range.StartLine <= req.Params.Position.Line && imp.Range.EndLine >= req.Params.Position.Line {
				resolved := h.idx.ResolveImport(imp.RawPath, filepath.Dir(path))
				hb := hoverBuilder{}
				hb.header("Import")
				hb.codeBlock("yaml", imp.RawPath)

				if len(resolved) > 0 {
					hb.rule()
					hb.header("Resolves to")
					for _, r := range resolved {
						rel, err := filepath.Rel(h.projectRoot, r)
						if err != nil {
							rel = r
						}
						hb.bullet(rel)
					}
				} else {
					hb.note("Unable to resolve path")
				}

				var importVars []index.VarNode
				for _, r := range resolved {
					parent := h.idx.GetFile(r)
					if parent != nil {
						importVars = append(importVars, parent.Vars...)
					}
				}
				if len(importVars) > 0 {
					hb.rule()
					hb.header("Vars from this import")
					for _, v := range importVars {
						hb.kv(v.Key, v.Value)
					}
				}

				hoverContent = map[string]interface{}{
					"kind":  "markdown",
					"value": hb.build(),
				}
				break
			}
		}
		if hoverContent == nil {
			for _, comp := range f.Comps {
				if comp.Range.StartLine <= req.Params.Position.Line && comp.Range.EndLine >= req.Params.Position.Line {
					definitions := h.idx.FindComponent(comp.Name)
					hb := hoverBuilder{}
					hb.header("Component")
					hb.codeBlock("yaml", comp.Name)

					if len(definitions) > 1 {
						hb.rule()
						hb.header("Also defined in")
						for _, sf := range definitions {
							if sf.Path != path {
								rel, err := filepath.Rel(h.projectRoot, sf.Path)
								if err != nil {
									rel = sf.Path
								}
								hb.bullet(rel)
							}
						}
					}

					if len(vars) > 0 {
						hb.rule()
						hb.header("Accumulated vars")
						keys := make([]string, 0, len(vars))
						for k := range vars {
							keys = append(keys, k)
						}
						sort.Strings(keys)
						var lines []string
						for _, k := range keys {
							lines = append(lines, fmt.Sprintf("- %s: %s", k, vars[k]))
						}
						hb.yamlBlock(lines)
					}

					if h.nameTemplate != "" {
						preview := interpolateNameTemplate(h.nameTemplate, vars, comp.Name)
						if preview != "" {
							hb.rule()
							hb.header("Stack name preview")
							hb.codeBlock("", preview)
							hb.note(fmt.Sprintf("Computed from `atmos.yaml` `name_template` with accumulated vars."))
						}
					}

					hoverContent = map[string]interface{}{
						"kind":  "markdown",
						"value": hb.build(),
					}
					break
				}
			}
		}
		if hoverContent == nil {
			for _, v := range f.Vars {
				if v.Range.StartLine <= req.Params.Position.Line && v.Range.EndLine >= req.Params.Position.Line {
					if strings.Contains(v.Value, "{{") && strings.Contains(v.Value, "}}") {
						expr, resolved := h.findTemplateExpressionAtPosition(f, h.idx, req.Params.Position.Line, req.Params.Position.Character, h.nameTemplate)
						if expr != "" {
							hb := hoverBuilder{}
							hb.header("Template expression")
							hb.codeBlock("go", expr)
							if resolved != "" && resolved != expr {
								hb.rule()
								hb.header("Resolved value")
								hb.codeBlock("", resolved)
							}
							hoverContent = map[string]interface{}{
								"kind":  "markdown",
								"value": hb.build(),
							}
							break
						}
					}
				}
			}
			// Fallback: template expressions outside of vars: blocks
			if hoverContent == nil {
				expr, resolved := h.findTemplateExpressionAtPosition(f, h.idx, req.Params.Position.Line, req.Params.Position.Character, h.nameTemplate)
				if expr != "" {
					hb := hoverBuilder{}
					hb.header("Template expression")
					hb.codeBlock("go", expr)
					if resolved != "" && resolved != expr {
						hb.rule()
						hb.header("Resolved value")
						hb.codeBlock("", resolved)
					}
					hoverContent = map[string]interface{}{
						"kind":  "markdown",
						"value": hb.build(),
					}
				}
			}
		}
		if hoverContent == nil {
			for _, ts := range f.TerraformState {
				if ts.Range.StartLine <= req.Params.Position.Line && ts.Range.EndLine >= req.Params.Position.Line {
					if ts.Component == "" {
						continue
					}
					hb := hoverBuilder{}
					hb.header("Remote state reference")
					hb.codeBlock("yaml", ts.Component)

					if ts.JQExpr != "" {
						hb.rule()
						hb.header("JQ expression")
						hb.codeBlock("jq", ts.JQExpr)
					}

					refs := h.idx.FindComponent(ts.Component)
					if len(refs) > 0 {
						hb.rule()
						hb.header("Component defined in")
						for _, ref := range refs {
							rel, err := filepath.Rel(h.projectRoot, ref.Path)
							if err != nil {
								rel = ref.Path
							}
							hb.bullet(rel)
						}
					}

					hoverContent = map[string]interface{}{
						"kind":  "markdown",
						"value": hb.build(),
					}
					break
				}
			}
		}
	}

	// Hover on YAML function tags (!env, !exec, !include, !terraform.output, !store)
	if hoverContent == nil && f != nil {
		for _, tag := range f.YAMLTags {
			if tag.Range.StartLine <= req.Params.Position.Line && tag.Range.EndLine >= req.Params.Position.Line {
				doc := yamlTagDocs(tag.Tag)
				if doc != "" {
					hb := hoverBuilder{}
					hb.header("YAML function: " + tag.Tag)
					if tag.Key != "" {
						hb.kv("Key", tag.Key)
					}
					if tag.Component != "" {
						hb.kv("Component", tag.Component)
					}
					hb.paragraph(doc)
					if tag.Value != "" {
						hb.rule()
						hb.codeBlock("yaml", tag.Value)
					}
					hoverContent = map[string]interface{}{
						"kind":  "markdown",
						"value": hb.build(),
					}
					break
				}
			}
		}
	}

	// Show resolved variables view for terminal stacks
	if hoverContent == nil && h.nameTemplate != "" && f != nil && len(f.Comps) > 0 {
		if len(vars) > 0 {
			hb := hoverBuilder{}
			hb.header("Resolved variables for this stack")
			keys := make([]string, 0, len(vars))
			for k := range vars {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			var lines []string
			for _, k := range keys {
				lines = append(lines, fmt.Sprintf("- %s: %s", k, vars[k]))
			}
			hb.yamlBlock(lines)
			hb.note("Hover over individual imports to see which file contributed each variable.")
			hoverContent = map[string]interface{}{
				"kind":  "markdown",
				"value": hb.build(),
			}
		}
	}

	if hoverContent == nil {
		return false, nil, nil, nil
	}

	result := map[string]interface{}{
		"contents": hoverContent,
	}

	b, err := buildResponse(req.ID, result)
	if err != nil {
		return true, errorResponse(content, -32603, "Internal error"), nil, nil
	}
	return true, b, nil, nil
}

func (h *LSPHandler) publishDiagnostics(uri string, diags []diagnostic) {
	if h.closed.Load() {
		return
	}
	// Ensure we always send [] rather than null so the client clears stale
	// diagnostics instead of ignoring the notification.
	if diags == nil {
		diags = []diagnostic{}
	}
	notification := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "textDocument/publishDiagnostics",
		"params": map[string]interface{}{
			"uri":         uri,
			"diagnostics": diags,
		},
	}
	notifBytes, err := json.Marshal(notification)
	if err != nil {
		log.Printf("diagnostics: failed to marshal notification: %v", err)
		return
	}
	h.closeMu.Lock()
	defer h.closeMu.Unlock()
	if h.closed.Load() {
		return
	}
	select {
	case h.notificationsCh <- notifBytes:
	default:
		log.Printf("diagnostics: notification channel full, dropping")
	}
}

func (h *LSPHandler) publishWorkspaceDiagnostics() {
	files := h.idx.AllFiles()
	log.Printf("diagnostics: publishing workspace diagnostics for %d files", len(files))
	for _, sf := range files {
		if sf == nil {
			continue
		}
		uri := "file://" + sf.Path
		diags := runBestPracticeChecks(sf, filepath.Dir(sf.Path), h.idx)
		if len(diags) > 0 {
			log.Printf("diagnostics: %s has %d issues", sf.Path, len(diags))
		}
		h.publishDiagnostics(uri, diags)
	}
}

func (h *LSPHandler) handleDiagnostics(content []byte) (bool, []byte, [][]byte, error) {
	if h.diagnosticsDisabled {
		log.Printf("diagnostics: disabled, skipping")
		return false, nil, nil, nil
	}

	var req struct {
		JSONRPC string             `json:"jsonrpc"`
		Method  string             `json:"method"`
		Params  textDocumentParams `json:"params"`
	}
	if err := json.Unmarshal(content, &req); err != nil {
		log.Printf("diagnostics: unmarshal error: %v", err)
		return true, nil, nil, nil
	}

	uri := req.Params.TextDocument.URI
	path := strings.TrimPrefix(uri, "file://")
	log.Printf("diagnostics: %s for %s", req.Method, path)

	// Parse live document content for didOpen / didChange so diagnostics
	// are accurate even before the file is saved to disk.
	if req.Method == "textDocument/didOpen" {
		var openReq struct {
			Params struct {
				TextDocument struct {
					Text string `json:"text"`
				} `json:"textDocument"`
			} `json:"params"`
		}
		if err := json.Unmarshal(content, &openReq); err == nil && openReq.Params.TextDocument.Text != "" {
			text := openReq.Params.TextDocument.Text
			h.documentContentMu.Lock()
			h.documentContent[path] = []byte(text)
			h.documentContentMu.Unlock()
			sf := index.ParseYAMLContent(path, []byte(text))
			h.idx.UpsertFile(path, sf)
		}
	} else if req.Method == "textDocument/didChange" {
		var changeReq struct {
			Params struct {
				ContentChanges []struct {
					Text string `json:"text"`
				} `json:"contentChanges"`
			} `json:"params"`
		}
		if err := json.Unmarshal(content, &changeReq); err != nil {
			log.Printf("diagnostics: didChange unmarshal error: %v", err)
		} else if len(changeReq.Params.ContentChanges) == 0 {
			log.Printf("diagnostics: didChange received with zero contentChanges")
		} else {
			text := changeReq.Params.ContentChanges[0].Text
			log.Printf("diagnostics: didChange text length=%d firstLine=%q", len(text), firstLine(text))
			h.documentContentMu.Lock()
			h.documentContent[path] = []byte(text)
			h.documentContentMu.Unlock()
			sf := index.ParseYAMLContent(path, []byte(text))
			if sf != nil {
				log.Printf("diagnostics: didChange parsed %d imports: %v", len(sf.Imports), importPaths(sf.Imports))
				// When the user is typing incomplete YAML (e.g. inside a template
				// expression), parsing fails and returns an empty StackFile. Preserve
				// the previously parsed data so completions still work.
				if sf.ParseError != "" {
					old := h.idx.GetFile(path)
					if old != nil {
						old.ParseError = sf.ParseError
						sf = old
					}
				}
			} else {
				log.Printf("diagnostics: didChange ParseYAMLContent returned nil")
			}
			h.idx.UpsertFile(path, sf)
		}
	} else if req.Method == "textDocument/didSave" {
		h.documentContentMu.Lock()
		delete(h.documentContent, path)
		h.documentContentMu.Unlock()
		h.idx.ReindexFile(path)
	} else if req.Method == "textDocument/didClose" {
		h.documentContentMu.Lock()
		delete(h.documentContent, path)
		h.documentContentMu.Unlock()
		// Forward to downstream so atmos LSP knows the file was closed.
		// Do NOT clear diagnostics here — they should remain visible in the
		// file tree even when the file is not open in an editor.
		if err := h.downstream.SendNotification(content); err != nil {
			log.Printf("diagnostics: forward didClose to atmos failed: %v", err)
		}
		return true, nil, nil, nil
	}

	// Forward notification to downstream atmos LSP for its own validation.
	if err := h.downstream.SendNotification(content); err != nil {
		log.Printf("diagnostics: forward to atmos failed: %v", err)
	}

	// Debounce diagnostic computation so rapid edits don't block the LSP loop.
	h.diagMu.Lock()
	h.diagPendingURI = uri
	h.diagPendingPath = path
	if h.diagTimer != nil {
		h.diagTimer.Stop()
	}
	h.diagTimer = time.AfterFunc(diagDebounce, func() {
		h.diagMu.Lock()
		uri := h.diagPendingURI
		path := h.diagPendingPath
		h.diagPendingURI = ""
		h.diagPendingPath = ""
		h.diagTimer = nil
		h.diagMu.Unlock()

		f := h.idx.GetFileUnsafe(path)
		if f == nil {
			log.Printf("diagnostics: no parsed file for %s, publishing empty set", path)
			h.publishDiagnostics(uri, []diagnostic{})
			return
		}

		diags := runBestPracticeChecks(f, filepath.Dir(path), h.idx)
		log.Printf("diagnostics: found %d issues for %s (imports=%v)", len(diags), path, importPaths(f.Imports))
		for i, d := range diags {
			log.Printf("diagnostics: [%d] %s (line %d)", i, d.Message, d.Range.Start.Line)
		}
		h.publishDiagnostics(uri, diags)
	})
	h.diagMu.Unlock()

	return true, nil, nil, nil
}

func resolveStacksPath(rootPath string) string {
	basePath, _ := parseAtmosConfig(rootPath)
	if basePath == "" {
		log.Printf("no atmos.yaml at %s or no stacks.base_path, defaulting to stacks/", rootPath)
		return filepath.Join(rootPath, "stacks")
	}
	return filepath.Join(rootPath, basePath)
}

func yamlTagDocs(tag string) string {
	switch tag {
	case "!env":
		return "Reads an environment variable. Usage: `!env ENV_VAR_NAME`"
	case "!exec":
		return "Executes a shell command and returns stdout. Usage: `!exec 'echo hello'`"
	case "!include":
		return "Includes the content of another file. Usage: `!include path/to/file.yaml`"
	case "!terraform.output":
		return "References a Terraform output from another component. Usage: `!terraform.output component_name output_name`"
	case "!terraform.state":
		return "References a Terraform remote state. Usage: `!terraform.state component_name [jq_expression]`"
	case "!store":
		return "Reads a value from a configured store (SSM, etc.). Usage: `!store key`"
	default:
		return ""
	}
}

func parseAtmosConfig(rootPath string) (basePath, nameTemplate string) {
	content, err := os.ReadFile(filepath.Join(rootPath, "atmos.yaml"))
	if err != nil {
		return "", ""
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(content, &doc); err != nil {
		return "", ""
	}
	if len(doc.Content) == 0 {
		return "", ""
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return "", ""
	}

	for i := 0; i < len(root.Content)-1; i += 2 {
		key := root.Content[i].Value
		val := root.Content[i+1]
		if key == "stacks" && val != nil && val.Kind == yaml.MappingNode {
			for j := 0; j < len(val.Content)-1; j += 2 {
				subKey := val.Content[j].Value
				subVal := val.Content[j+1]
				if subKey == "base_path" && subVal != nil {
					basePath = subVal.Value
				}
				if subKey == "name_template" && subVal != nil {
					nameTemplate = subVal.Value
				}
			}
		}
	}
	return basePath, nameTemplate
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// normalizeLines splits text by "\n" and strips trailing "\r" from each line
// so that CRLF files produce the same line content as LF files.
func normalizeLines(text string) []string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSuffix(line, "\r")
	}
	return lines
}

func importPaths(imports []index.ImportNode) []string {
	out := make([]string, len(imports))
	for i, imp := range imports {
		out[i] = imp.RawPath
	}
	return out
}

var (
	nameTemplateVarsRe = regexp.MustCompile(`{{\s*\.vars\.([a-zA-Z0-9_]+)\s*}}`)
	nameTemplateKeyRe  = regexp.MustCompile(`{{\s*\.([a-zA-Z0-9_]+)\s*}}`)
	nameTemplateRemRe  = regexp.MustCompile(`{{\s*[^}]*\s*}}`)
)

func interpolateNameTemplate(tpl string, vars map[string]string, componentName string) string {
	result := nameTemplateVarsRe.ReplaceAllStringFunc(tpl, func(m string) string {
		subs := nameTemplateVarsRe.FindStringSubmatch(m)
		if len(subs) > 1 {
			if v, ok := vars[subs[1]]; ok {
				return v
			}
		}
		return ""
	})

	result = nameTemplateKeyRe.ReplaceAllStringFunc(result, func(m string) string {
		subs := nameTemplateKeyRe.FindStringSubmatch(m)
		if len(subs) > 1 {
			if subs[1] == "atmos_component" && componentName != "" {
				return componentName
			}
			if v, ok := vars[subs[1]]; ok {
				return v
			}
		}
		return ""
	})

	result = nameTemplateRemRe.ReplaceAllString(result, "")
	return strings.TrimSpace(result)
}

var (
	templateVarExprRe = regexp.MustCompile(`{{\s*\.vars\.([a-zA-Z0-9_]+)\s*}}`)
	templateExprRe    = regexp.MustCompile(`{{\s*[^}]+\s*}}`)
	atmosComponentRe  = regexp.MustCompile(`{{\s*\.atmos_component\s*}}`)
	atmosStackRe      = regexp.MustCompile(`{{\s*\.atmos_stack\s*}}`)
)

func (h *LSPHandler) findTemplateExpressionAtPosition(sf *index.StackFile, idx *index.Index, line uint32, char uint32, nameTemplate string) (expr string, resolved string) {
	// 1. Try matching a VarNode (vars: block)
	for _, v := range sf.Vars {
		if v.Range.StartLine == line && v.Range.StartChar <= char && v.Range.EndChar >= char {
			expr = v.Value
			break
		}
	}

	// 2. Fallback: use live document content (from didOpen/didChange) to extract
	// template expressions from the line. If no live content is available, fall
	// back to reading from disk.
	if expr == "" {
		h.documentContentMu.RLock()
		content, ok := h.documentContent[sf.Path]
		h.documentContentMu.RUnlock()
		if !ok {
			var err error
			content, err = os.ReadFile(sf.Path)
			if err != nil {
				return "", ""
			}
		}
		lines := normalizeLines(string(content))
		if int(line) >= len(lines) {
			return "", ""
		}
		lineText := lines[line]
		matchIdxs := templateExprRe.FindAllStringIndex(lineText, -1)
		if len(matchIdxs) == 0 {
			return "", ""
		}
		// Find the match that contains char. If none match, check if char is within
		// the span of all matches (between first start and last end).
		firstMatch := 0
		lastMatch := len(matchIdxs) - 1
		found := false
		for i, m := range matchIdxs {
			if m[0] <= int(char) && m[1] > int(char) {
				firstMatch = i
				lastMatch = i
				found = true
				break
			}
		}
		if !found {
			if int(char) >= matchIdxs[0][0] && int(char) <= matchIdxs[lastMatch][1] {
				// char is in the literal text between matches — show the whole span
			} else {
				return "", ""
			}
		}
		expr = lineText[matchIdxs[firstMatch][0]:matchIdxs[lastMatch][1]]
	}

	resolved = expr
	vars := collectVars(sf, idx)

	// Resolve {{ .vars.X }} → vars[X]
	resolved = templateVarExprRe.ReplaceAllStringFunc(resolved, func(m string) string {
		subs := templateVarExprRe.FindStringSubmatch(m)
		if len(subs) > 1 {
			if v, ok := vars[subs[1]]; ok {
				return v
			}
		}
		return m
	})

	// Resolve {{ .X }} → vars[X] for direct key references
	// (excluding .atmos_component and .atmos_stack which have special handling)
	resolved = nameTemplateKeyRe.ReplaceAllStringFunc(resolved, func(m string) string {
		subs := nameTemplateKeyRe.FindStringSubmatch(m)
		if len(subs) > 1 {
			key := subs[1]
			if key == "atmos_component" || key == "atmos_stack" {
				return m
			}
			if v, ok := vars[key]; ok {
				return v
			}
		}
		return m
	})

	if atmosComponentRe.MatchString(expr) {
		compName := ""
		// First try: was this a VarNode inside a component?
		for _, v := range sf.Vars {
			if v.Range.StartLine == line && v.Range.StartChar <= char && v.Range.EndChar >= char {
				compName = v.Component
				break
			}
		}
		// Fallback: search backward for the nearest component context
		if compName == "" {
			compName = findComponentForLine(sf, line)
		}
		if compName != "" {
			resolved = atmosComponentRe.ReplaceAllString(resolved, compName)
		}
	}

	if atmosStackRe.MatchString(expr) {
		stackName := computeStackName(sf, idx, nameTemplate)
		if stackName != "" {
			resolved = atmosStackRe.ReplaceAllString(resolved, stackName)
		}
	}

	return expr, resolved
}

func computeStackName(sf *index.StackFile, idx *index.Index, nameTemplate string) string {
	if sf == nil || idx == nil {
		return ""
	}
	rawStackName := ""
	if rel, err := filepath.Rel(idx.BasePath(), sf.Path); err == nil {
		rawStackName = strings.TrimSuffix(rel, filepath.Ext(rel))
	}
	if nameTemplate != "" {
		vars := collectVars(sf, idx)
		// Inject atmos_stack so interpolateNameTemplate can resolve it.
		vars["atmos_stack"] = rawStackName
		return interpolateNameTemplate(nameTemplate, vars, "")
	}
	return rawStackName
}

func findComponentForLine(sf *index.StackFile, line uint32) string {
	if len(sf.Comps) == 0 {
		return ""
	}
	// Find the component whose StartLine is closest to but <= line
	best := ""
	bestLine := uint32(0)
	for _, comp := range sf.Comps {
		if comp.Range.StartLine <= line && comp.Range.StartLine >= bestLine {
			best = comp.Name
			bestLine = comp.Range.StartLine
		}
	}
	return best
}

const maxVarDepth = 50

func collectVars(sf *index.StackFile, idx *index.Index) map[string]string {
	vars := make(map[string]string)
	if sf == nil {
		return vars
	}
	visited := make(map[string]bool)
	collectVarsRecursive(sf, idx, vars, visited, 0)
	return vars
}

func collectVarsRecursive(sf *index.StackFile, idx *index.Index, vars map[string]string, visited map[string]bool, depth int) {
	if sf == nil || visited[sf.Path] || depth > maxVarDepth {
		return
	}
	visited[sf.Path] = true

	if idx != nil {
		fromDir := filepath.Dir(sf.Path)
		for _, imp := range sf.Imports {
			resolved := idx.ResolveImport(imp.RawPath, fromDir)
			for _, r := range resolved {
				parent := idx.GetFile(r)
				if parent != nil {
					collectVarsRecursive(parent, idx, vars, visited, depth+1)
				}
			}
		}
	}

	for _, v := range sf.Vars {
		vars[v.Key] = v.Value
	}
}

func errorResponse(content []byte, code int, message string) []byte {
	return lsp.BuildErrorResponse(content, code, message)
}

func buildResponse(id json.RawMessage, result interface{}) ([]byte, error) {
	resultBytes, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	resp := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  json.RawMessage(resultBytes),
	}
	return json.Marshal(resp)
}

func emptyResult(content []byte, id json.RawMessage) []byte {
	resp := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  []interface{}{},
	}
	b, err := json.Marshal(resp)
	if err != nil {
		return errorResponse(content, -32603, "Internal error")
	}
	return b
}

func nullResult(content []byte) []byte {
	var req struct {
		ID json.RawMessage `json:"id"`
	}
	if err := json.Unmarshal(content, &req); err != nil {
		b, _ := json.Marshal(map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      nil,
			"result":  nil,
		})
		return b
	}
	resp := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      req.ID,
		"result":  nil,
	}
	b, err := json.Marshal(resp)
	if err != nil {
		return errorResponse(content, -32603, "Internal error")
	}
	return b
}
