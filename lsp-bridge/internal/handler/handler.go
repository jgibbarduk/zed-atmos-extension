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

	"github.com/jgibbarduk/zed-atmos-extension/lsp-bridge/internal/index"
	"github.com/jgibbarduk/zed-atmos-extension/lsp-bridge/internal/lsp"
	"gopkg.in/yaml.v3"
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
	idx                 *index.Index
	downstream          DownstreamCaller
	initialized         bool
	diagnosticsDisabled bool
	projectRoot         string
	nameTemplate        string
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

	if method == "textDocument/rename" {
		return h.handleRename(content)
	}

	if method == "textDocument/codeAction" {
		return h.handleCodeAction(content)
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

	// Parse initialization_options for user configuration.
	var initOpts struct {
		InitializationOptions Config `json:"initializationOptions"`
	}
	json.Unmarshal(req.Params, &initOpts)

	if rootPath != "" {
		h.projectRoot = rootPath
		basePath, nameTemplate := parseAtmosConfig(rootPath)
		if initOpts.InitializationOptions.StacksPath != "" {
			h.idx.SetBasePath(initOpts.InitializationOptions.StacksPath)
		} else if basePath != "" {
			h.idx.SetBasePath(filepath.Join(rootPath, basePath))
		} else {
			h.idx.SetBasePath(resolveStacksPath(rootPath))
		}
		h.nameTemplate = nameTemplate
	}

	if initOpts.InitializationOptions.DiagnosticsEnabled != nil && !*initOpts.InitializationOptions.DiagnosticsEnabled {
		h.diagnosticsDisabled = true
	}

	downstreamResp, err := h.downstream.CallDownstream(content)
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
				edits = append(edits, lsp.TextEdit{
					Range:   toLSPRange(ts.Range),
					NewText: req.Params.NewName,
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
	resultBytes, _ := json.Marshal(result)
	resp := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      req.ID,
		"result":  json.RawMessage(resultBytes),
	}
	b, _ := json.Marshal(resp)
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

	resultBytes, _ := json.Marshal(actions)
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
						rel, err := filepath.Rel(h.projectRoot, r)
						if err != nil {
							rel = r
						}
						value += fmt.Sprintf("- `%s`\n", rel)
					}
				} else {
					value += "*Unable to resolve path*"
				}
				var importVars []index.VarNode
				for _, r := range resolved {
					parent := h.idx.GetFile(r)
					if parent != nil {
						importVars = append(importVars, parent.Vars...)
					}
				}
				if len(importVars) > 0 {
					value += "\n**Vars from this import:**\n"
					for _, v := range importVars {
						value += fmt.Sprintf("- `%s`: `%s`\n", v.Key, v.Value)
					}
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
					definitions := h.idx.FindComponent(comp.Name)
					value := fmt.Sprintf("**Component:** `%s`\n\n", comp.Name)
					if len(definitions) > 1 {
						value += "**Also defined in:**\n"
						for _, sf := range definitions {
							if sf.Path != path {
								rel, err := filepath.Rel(h.projectRoot, sf.Path)
								if err != nil {
									rel = sf.Path
								}
								value += fmt.Sprintf("- `%s`\n", rel)
							}
						}
					}
					// Show accumulated vars for this component
					vars := collectVars(f, h.idx)
					if len(vars) > 0 {
						keys := make([]string, 0, len(vars))
						for k := range vars {
							keys = append(keys, k)
						}
						sort.Strings(keys)
						value += "\n**Accumulated vars:**\n"
						for _, k := range keys {
							value += fmt.Sprintf("- `%s`: `%s`\n", k, vars[k])
						}
					}
					if h.nameTemplate != "" {
						preview := interpolateNameTemplate(h.nameTemplate, vars, comp.Name)
						if preview != "" {
							value += fmt.Sprintf("\n\n**Stack name:** `%s`\n\nComputed from `atmos.yaml` `name_template` with accumulated vars.", preview)
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
		if hoverContent == nil {
			for _, v := range f.Vars {
				if v.Range.StartLine <= req.Params.Position.Line && v.Range.EndLine >= req.Params.Position.Line {
					if strings.Contains(v.Value, "{{") && strings.Contains(v.Value, "}}") {
						expr, resolved := findTemplateExpressionAtPosition(f, h.idx, req.Params.Position.Line, req.Params.Position.Character)
						if expr != "" {
							value := fmt.Sprintf("**Template:** `%s`", expr)
							if resolved != "" && resolved != expr {
								value += fmt.Sprintf("\n\n**Resolved:** `%s`", resolved)
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
			// Fallback: template expressions outside of vars: blocks
			if hoverContent == nil {
				expr, resolved := findTemplateExpressionAtPosition(f, h.idx, req.Params.Position.Line, req.Params.Position.Character)
				if expr != "" {
					value := fmt.Sprintf("**Template:** `%s`", expr)
					if resolved != "" && resolved != expr {
						value += fmt.Sprintf("\n\n**Resolved:** `%s`", resolved)
					}
					hoverContent = map[string]interface{}{
						"kind":  "markdown",
						"value": value,
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
					value := fmt.Sprintf("**Remote state reference:** `%s`\n", ts.Component)
					if ts.JQExpr != "" {
						value += fmt.Sprintf("\nJQ expression: `%s`\n", ts.JQExpr)
					}
					refs := h.idx.FindComponent(ts.Component)
					if len(refs) > 0 {
						value += "\n**Component defined in:**\n"
						for _, ref := range refs {
							rel, err := filepath.Rel(h.projectRoot, ref.Path)
							if err != nil {
								rel = ref.Path
							}
							value += fmt.Sprintf("- `%s`\n", rel)
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

	// Show resolved variables view for terminal stacks
	if hoverContent == nil && h.nameTemplate != "" && f != nil && len(f.Comps) > 0 {
		vars := collectVars(f, h.idx)
		if len(vars) > 0 {
			value := "**Resolved variables for this stack:**\n\n"
			keys := make([]string, 0, len(vars))
			for k := range vars {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				value += fmt.Sprintf("- `%s`: `%s`\n", k, vars[k])
			}
			value += "\n*Hover over individual imports to see which file contributed each variable.*"
			hoverContent = map[string]interface{}{
				"kind":  "markdown",
				"value": value,
			}
		}
	}

	if hoverContent == nil {
		return false, nil, nil, nil
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
		JSONRPC string             `json:"jsonrpc"`
		Method  string             `json:"method"`
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

	// Forward notification to downstream atmos LSP for its own validation.
	if err := h.downstream.SendNotification(content); err != nil {
		log.Printf("diagnostics: forward to atmos failed: %v", err)
	}

	diags := runBestPracticeChecks(f, filepath.Dir(path), h.idx)

	notifications := [][]byte{}
	if len(diags) > 0 {
		notification := map[string]interface{}{
			"jsonrpc": "2.0",
			"method":  "textDocument/publishDiagnostics",
			"params": map[string]interface{}{
				"uri":         req.Params.TextDocument.URI,
				"diagnostics": diags,
			},
		}
		notifBytes, _ := json.Marshal(notification)
		notifications = append(notifications, notifBytes)
	}

	return true, nil, notifications, nil
}

func resolveStacksPath(rootPath string) string {
	basePath, _ := parseAtmosConfig(rootPath)
	if basePath == "" {
		log.Printf("no atmos.yaml at %s or no stacks.base_path, defaulting to stacks/", rootPath)
		return filepath.Join(rootPath, "stacks")
	}
	return filepath.Join(rootPath, basePath)
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

var templateVarExprRe = regexp.MustCompile(`{{\s*\.vars\.([a-zA-Z0-9_]+)\s*}}`)
var templateExprRe = regexp.MustCompile(`{{\s*[^}]+\s*}}`)

func findTemplateExpressionAtPosition(sf *index.StackFile, idx *index.Index, line uint32, char uint32) (expr string, resolved string) {
	// 1. Try matching a VarNode (vars: block)
	for _, v := range sf.Vars {
		if v.Range.StartLine == line && v.Range.StartChar <= char && v.Range.EndChar >= char {
			expr = v.Value
			break
		}
	}

	// 2. Fallback: read the raw file and extract template expressions from the line
	if expr == "" {
		content, err := os.ReadFile(sf.Path)
		if err != nil {
			return "", ""
		}
		lines := strings.Split(string(content), "\n")
		if int(line) >= len(lines) {
			return "", ""
		}
		lineText := lines[line]
		matches := templateExprRe.FindAllString(lineText, -1)
		if len(matches) == 0 {
			return "", ""
		}
		expr = strings.Join(matches, "")
	}

	resolved = expr
	vars := collectVars(sf, idx)
	resolved = templateVarExprRe.ReplaceAllStringFunc(resolved, func(m string) string {
		subs := templateVarExprRe.FindStringSubmatch(m)
		if len(subs) > 1 {
			if v, ok := vars[subs[1]]; ok {
				return v
			}
		}
		return m
	})

	if strings.Contains(expr, "{{ .atmos_component }}") {
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
			resolved = strings.ReplaceAll(resolved, "{{ .atmos_component }}", compName)
		}
	}

	return expr, resolved
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

func collectVars(sf *index.StackFile, idx *index.Index) map[string]string {
	vars := make(map[string]string)
	if sf == nil {
		return vars
	}
	visited := make(map[string]bool)
	collectVarsRecursive(sf, idx, vars, visited)
	return vars
}

func collectVarsRecursive(sf *index.StackFile, idx *index.Index, vars map[string]string, visited map[string]bool) {
	if sf == nil || visited[sf.Path] {
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
					collectVarsRecursive(parent, idx, vars, visited)
				}
			}
		}
	}

	for _, v := range sf.Vars {
		vars[v.Key] = v.Value
	}
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
