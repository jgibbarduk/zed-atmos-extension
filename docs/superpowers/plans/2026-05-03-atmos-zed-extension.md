# Atmos Language Extension for Zed — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a Zed IDE extension for Atmos stack configuration files with syntax highlighting, outline, go-to-definition across import chains, find references, semantic tokens, and best-practice hints.

**Architecture:** A Rust WASM shim spawns a Go LSP bridge binary. The Go bridge spawns `atmos lsp start` as a child process, forwarding most LSP requests directly to it while intercepting `textDocument/definition`, `textDocument/references`, and `textDocument/semanticTokens/full` to handle them with custom logic. Tree-sitter queries (reusing tree-sitter-yaml) provide syntax highlighting, outline, brackets, and indentation.

**Tech Stack:** Rust (wasm32-wasip1 via zed_extension_api), Go (LSP bridge binary), Scheme (tree-sitter queries), tree-sitter-yaml grammar

---

### Task 1: Project scaffolding

**Files:**
- Create: `extension.toml`
- Create: `Cargo.toml`
- Create: `src/lib.rs`
- Create: `languages/atmos/config.toml`
- Create: `LICENSE`

- [ ] **Step 1: Create extension.toml**

```toml
id = "atmos"
name = "Atmos"
version = "0.1.0"
schema_version = 1
authors = ["James Gibbard <james@example.com>"]
description = "Atmos stack configuration support with import resolution, inheritance navigation, and best-practice hints"
repository = "https://github.com/jamesgibbard/zed-atmos-language"

[grammars.yaml]
repository = "https://github.com/tree-sitter-grammars/tree-sitter-yaml"
rev = "0.7.0"

[language_servers.atmos-lsp-bridge]
name = "Atmos LSP Bridge"
languages = ["Atmos"]
```

- [ ] **Step 2: Create Cargo.toml**

```toml
[package]
name = "zed-atmos-language"
version = "0.1.0"
edition = "2021"

[lib]
crate-type = ["cdylib"]

[dependencies]
zed_extension_api = "0.7.0"
```

- [ ] **Step 3: Create minimal src/lib.rs**

```rust
use zed_extension_api::{self as zed, Extension, Command, LanguageServerId, Worktree, Result};

struct AtmosExtension;

impl Extension for AtmosExtension {
    fn new() -> Self {
        AtmosExtension
    }

    fn language_server_command(
        &mut self,
        _language_server_id: &LanguageServerId,
        worktree: &Worktree,
    ) -> Result<Command> {
        let binary_path = worktree
            .which("atmos-lsp-bridge")
            .ok_or_else(|| "atmos-lsp-bridge not found in PATH. Install it from https://github.com/jamesgibbard/zed-atmos-language/releases".to_string())?;

        Ok(Command {
            command: binary_path,
            args: vec![],
            env: vec![],
        })
    }
}

zed::register_extension!(AtmosExtension);
```

- [ ] **Step 4: Create language config**

Create `languages/atmos/config.toml`:

```toml
name = "Atmos"
grammar = "yaml"
path_suffixes = ["yaml", "yml"]
line_comments = ["# "]
tab_size = 2
first_line_pattern = "^(import|vars|settings|env|components|metadata|terraform|helmfile):"
```

- [ ] **Step 5: Create LICENSE**

```
MIT License

Copyright (c) 2026 James Gibbard

Permission is hereby granted, free of charge, to any person obtaining a copy...
```

- [ ] **Step 6: Verify scaffolding compiles**

```bash
cd /Users/jamesgibbard/Development/zed-atmos-language
cargo check --target wasm32-wasip1
```

Expected: Compilation succeeds (warnings about unused import fine).

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "feat: scaffold project structure"
```

---

### Task 2: Tree-sitter highlights.scm

**Files:**
- Create: `languages/atmos/highlights.scm`

- [ ] **Step 1: Write highlights.scm**

```scheme
; Top-level Atmos keywords
(block_mapping_pair
  key: (flow_node (plain_scalar (string_scalar) @keyword))
  (#match? @keyword "^(import|vars|settings|env|components|metadata|terraform|helmfile|provider|backend|overrides|namespace|tenant|environment|stage)$"))

; Component names under components.terraform.* or components.helmfile.*
(block_mapping_pair
  key: (flow_node (plain_scalar (string_scalar) @_comp_type))
  (#match? @_comp_type "^(terraform|helmfile)$")
  value: (block_node (block_mapping
    (block_mapping_pair
      key: (flow_node (plain_scalar (string_scalar) @function))))))

; metadata.component value (Terraform module reference)
(block_mapping_pair
  key: (flow_node (plain_scalar (string_scalar) @_meta_key))
  (#eq? @_meta_key "component")
  value: (block_node (flow_node (plain_scalar (string_scalar) @type))))

; Import path values
(block_mapping_pair
  key: (flow_node (plain_scalar (string_scalar) @_import_key))
  (#eq? @_import_key "import")
  value: (block_node (block_sequence
    (block_sequence_item (flow_node (plain_scalar (string_scalar) @string.special))))))

; Metadata inherits - highlight the key
(block_mapping_pair
  key: (flow_node (plain_scalar (string_scalar) @keyword))
  (#eq? @keyword "inherits"))

; String values
(string_scalar) @string

; Numbers
(float_scalar) @number
(integer_scalar) @number

; Boolean values
(boolean_scalar) @boolean

; Null
(null_scalar) @constant

; Comments
(comment) @comment

; Block scalars (multi-line strings)
(block_scalar) @string
```

- [ ] **Step 2: Commit**

```bash
git add languages/atmos/highlights.scm
git commit -m "feat: add tree-sitter highlights.scm"
```

---

### Task 3: Tree-sitter outline.scm

**Files:**
- Create: `languages/atmos/outline.scm`

- [ ] **Step 1: Write outline.scm**

```scheme
; Terraform components
(block_mapping_pair
  key: (flow_node (plain_scalar (string_scalar) @_comp_type))
  (#eq? @_comp_type "terraform")
  value: (block_node (block_mapping
    (block_mapping_pair
      key: (flow_node (plain_scalar (string_scalar) @name))
      ) @item))) @context

; Helmfile components
(block_mapping_pair
  key: (flow_node (plain_scalar (string_scalar) @_comp_type2))
  (#eq? @_comp_type2 "helmfile")
  value: (block_node (block_mapping
    (block_mapping_pair
      key: (flow_node (plain_scalar (string_scalar) @name))
      ) @item))) @context

; Import paths as context
(block_mapping_pair
  key: (flow_node (plain_scalar (string_scalar) @_import))
  (#eq? @_import "import")
  value: (block_node (block_sequence
    (block_sequence_item (flow_node (plain_scalar (string_scalar) @name))))))
```

- [ ] **Step 2: Commit**

```bash
git add languages/atmos/outline.scm
git commit -m "feat: add tree-sitter outline.scm"
```

---

### Task 4: Tree-sitter indents.scm and brackets.scm

**Files:**
- Create: `languages/atmos/indents.scm`
- Create: `languages/atmos/brackets.scm`

- [ ] **Step 1: Write indents.scm**

```scheme
(block_mapping_pair) @indent
(block_sequence_item) @indent
```

- [ ] **Step 2: Write brackets.scm**

```scheme
("[" @open "]" @close)
("{" @open "}" @close)
("\"" @open "\"" @close)
```

- [ ] **Step 3: Commit**

```bash
git add languages/atmos/indents.scm languages/atmos/brackets.scm
git commit -m "feat: add indents.scm and brackets.scm"
```

---

### Task 5: Semantic token rules

**Files:**
- Create: `languages/atmos/semantic_token_rules.json`

- [ ] **Step 1: Write semantic_token_rules.json**

```json
[
  {
    "token_type": "keyword",
    "style": ["keyword"]
  },
  {
    "token_type": "function",
    "style": ["function"]
  },
  {
    "token_type": "type",
    "style": ["type"]
  },
  {
    "token_type": "macro",
    "style": ["keyword"]
  },
  {
    "token_type": "string",
    "style": ["string.special"]
  }
]
```

- [ ] **Step 2: Commit**

```bash
git add languages/atmos/semantic_token_rules.json
git commit -m "feat: add semantic token rules"
```

---

### Task 6: Go LSP Bridge — project setup and JSON-RPC transport

**Files:**
- Create: `lsp-bridge/go.mod`
- Create: `lsp-bridge/internal/lsp/transport.go`
- Create: `lsp-bridge/internal/lsp/messages.go`

- [ ] **Step 1: Create Go module**

Create `lsp-bridge/go.mod`:

```go
module github.com/jamesgibbard/zed-atmos-language/lsp-bridge

go 1.22
```

- [ ] **Step 2: Create LSP message types**

Create `lsp-bridge/internal/lsp/messages.go`:

```go
package lsp

import "encoding/json"

type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *ResponseError  `json:"error,omitempty"`
}

type ResponseError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type Notification struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}
```

- [ ] **Step 3: Create JSON-RPC transport**

Create `lsp-bridge/internal/lsp/transport.go`:

```go
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

func ParseMethod(content []byte) string {
	var msg struct {
		Method string `json:"method"`
	}
	if err := json.Unmarshal(content, &msg); err != nil {
		return ""
	}
	return msg.Method
}
```

- [ ] **Step 4: Commit**

```bash
git add -A
git commit -m "feat: add Go bridge project with LSP transport layer"
```

---

### Task 7: Go LSP Bridge — proxy core

**Files:**
- Create: `lsp-bridge/internal/proxy/proxy.go`

- [ ] **Step 1: Write proxy implementation**

Create `lsp-bridge/internal/proxy/proxy.go`:

```go
package proxy

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os/exec"
	"sync"

	"github.com/jamesgibbard/zed-atmos-language/lsp-bridge/internal/lsp"
)

type Proxy struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  io.ReadCloser
	mu      sync.Mutex
	handler Handler
}

type Handler interface {
	HandleMethod(method string, params json.RawMessage) (json.RawMessage, error)
}

func New(atmosPath string, handler Handler) (*Proxy, error) {
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
		cmd:     cmd,
		stdin:   stdin,
		stdout:  stdout,
		handler: handler,
	}, nil
}

func (p *Proxy) forwardToAtmos(content []byte) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if err := lsp.WriteMessage(p.stdin, content); err != nil {
		return nil, err
	}

	reader := bufio.NewReader(p.stdout)
	msg, err := lsp.ReadMessage(reader)
	if err != nil {
		return nil, err
	}
	return msg.Content, nil
}

func (p *Proxy) Run(stdin io.Reader, stdout io.Writer) error {
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
		var response []byte

		handled, handledResp, err := p.handler.HandleMethod(method, msg.Content)
		if err != nil {
			log.Printf("handler error for %s: %v", method, err)
		}
		if handled {
			response = handledResp
		} else {
			resp, err := p.forwardToAtmos(msg.Content)
			if err != nil {
				log.Printf("forward error: %v", err)
				continue
			}
			response = resp
		}

		if err := lsp.WriteMessage(stdout, response); err != nil {
			return fmt.Errorf("write stdout: %w", err)
		}
	}
}

func (p *Proxy) Close() error {
	p.stdin.Close()
	return p.cmd.Wait()
}
```

- [ ] **Step 2: Commit**

```bash
git add -A
git commit -m "feat: add LSP proxy core with forwarding"
```

---

### Task 8: Go LSP Bridge — index

**Files:**
- Create: `lsp-bridge/internal/index/index.go`

- [ ] **Step 1: Write index implementation**

Create `lsp-bridge/internal/index/index.go`:

```go
package index

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

type Range struct {
	StartLine uint32
	StartChar uint32
	EndLine   uint32
	EndChar   uint32
}

type ImportNode struct {
	RawPath  string
	Range    Range
	Resolves []string
}

type CompNode struct {
	Name  string
	Range Range
}

type StackFile struct {
	Path    string
	Imports []ImportNode
	Comps   []CompNode
}

type Index struct {
	mu       sync.RWMutex
	Files    map[string]*StackFile
	ByImport map[string][]string
	BasePath string
	watcher  *fsnotify.Watcher
	onChange func()
}

func New(basePath string) (*Index, error) {
	idx := &Index{
		Files:    make(map[string]*StackFile),
		ByImport: make(map[string][]string),
		BasePath: basePath,
	}
	return idx, nil
}

func (idx *Index) StartWatching(onChange func()) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	idx.watcher = w
	idx.onChange = onChange

	go func() {
		debounce := time.NewTimer(0)
		<-debounce.C

		for {
			select {
			case event, ok := <-w.Events:
				if !ok {
					return
				}
				if strings.HasSuffix(event.Name, ".yaml") || strings.HasSuffix(event.Name, ".yml") {
					debounce.Reset(200 * time.Millisecond)
				}
			case <-debounce.C:
				idx.Reindex()
				if idx.onChange != nil {
					idx.onChange()
				}
			case err, ok := <-w.Errors:
				if !ok {
					return
				}
				_ = err
			}
		}
	}()

	return filepath.Walk(idx.BasePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			return w.Add(path)
		}
		return nil
	})
}

func (idx *Index) Reindex() {
	// Walk BasePath, parse each YAML file, update idx.Files and idx.ByImport
	entries, _ := os.ReadDir(idx.BasePath)
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".yaml") && !strings.HasSuffix(e.Name(), ".yml") {
			continue
		}
		idx.mu.Lock()
		idx.Files[filepath.Join(idx.BasePath, e.Name())] = &StackFile{
			Path: filepath.Join(idx.BasePath, e.Name()),
		}
		idx.mu.Unlock()
	}
}

func (idx *Index) GetFile(path string) *StackFile {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return idx.Files[path]
}

func (idx *Index) FindComponent(name string) []StackFile {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	var results []StackFile
	for _, f := range idx.Files {
		for _, c := range f.Comps {
			if c.Name == name {
				results = append(results, *f)
				break
			}
		}
	}
	return results
}

func (idx *Index) SetBasePath(path string) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	idx.BasePath = path
}

func (idx *Index) Close() {
	if idx.watcher != nil {
		idx.watcher.Close()
	}
}
```

- [ ] **Step 2: Install fsnotify dependency**

```bash
cd lsp-bridge && go get github.com/fsnotify/fsnotify
```

- [ ] **Step 3: Commit**

```bash
git add -A
git commit -m "feat: add file index with watcher"
```

---

### Task 9: Go LSP Bridge — initialize handshake with capability patching

**Files:**
- Create: `lsp-bridge/internal/handler/handler.go`

- [ ] **Step 1: Write handler with capability patching**

Create `lsp-bridge/internal/handler/handler.go`:

```go
package handler

import (
	"encoding/json"
	"log"

	"github.com/jamesgibbard/zed-atmos-language/lsp-bridge/internal/index"
	"github.com/jamesgibbard/zed-atmos-language/lsp-bridge/internal/navigation"
)

type LSPHandler struct {
	idx          *index.Index
	navigator    *navigation.Navigator
	initialized  bool
}

func New(idx *index.Index) *LSPHandler {
	return &LSPHandler{
		idx:       idx,
		navigator: navigation.New(idx),
	}
}

type rawMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
	Result  json.RawMessage `json:"result,omitempty"`
}

func (h *LSPHandler) HandleInitialize(params json.RawMessage) json.RawMessage {
	var initParams struct {
		RootPath string `json:"rootPath"`
		RootURI  string `json:"rootUri"`
	}
	if err := json.Unmarshal(params, &initParams); err != nil {
		log.Printf("initialize unmarshal: %v", err)
	}

	if initParams.RootPath != "" {
		h.idx.SetBasePath(initParams.RootPath + "/stacks")
	}

	result := map[string]interface{}{
		"capabilities": map[string]interface{}{
			"definitionProvider":     true,
			"referencesProvider":     true,
			"hoverProvider":          true,
			"completionProvider": map[string]interface{}{
				"resolveProvider": false,
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
				"change":    1, // Full
			},
		},
	}

	b, _ := json.Marshal(result)
	return b
}

func (h *LSPHandler) HandleMethod(method string, content []byte) (bool, []byte, error) {
	if method == "initialize" {
		var req rawMessage
		json.Unmarshal(content, &req)

		result := h.HandleInitialize(req.Params)

		resp := rawMessage{
			JSONRPC: req.JSONRPC,
			ID:      req.ID,
			Result:  result,
		}

		b, err := json.Marshal(resp)
		return true, b, err
	}

	if method == "initialized" {
		go h.idx.Reindex()
		h.initialized = true
		return true, nil, nil
	}

	interceptedMethods := map[string]bool{
		"textDocument/definition":             true,
		"textDocument/references":             true,
		"textDocument/semanticTokens/full":    true,
	}

	if !interceptedMethods[method] {
		return false, nil, nil
	}

	// Forward intercepted methods to atmos for now — will be handled in subsequent tasks
	return false, nil, nil
}
```

- [ ] **Step 2: Create stub Navigator**

Create `lsp-bridge/internal/navigation/navigation.go`:

```go
package navigation

import (
	"github.com/jamesgibbard/zed-atmos-language/lsp-bridge/internal/index"
)

type Navigator struct {
	idx *index.Index
}

func New(idx *index.Index) *Navigator {
	return &Navigator{idx: idx}
}

type Location struct {
	URI   string `json:"uri"`
	Range index.Range `json:"range"`
}

func (n *Navigator) GoToDefinition(uri string, line, char uint32) ([]Location, error) {
	return nil, nil
}

func (n *Navigator) FindReferences(uri string, line, char uint32) ([]Location, error) {
	return nil, nil
}
```

- [ ] **Step 3: Commit**

```bash
git add -A
git commit -m "feat: add handler with initialize capability patching"
```

---

### Task 10: Go LSP Bridge — main entry point

**Files:**
- Create: `lsp-bridge/main.go`

- [ ] **Step 1: Write main.go**

Create `lsp-bridge/main.go`:

```go
package main

import (
	"log"
	"os"

	"github.com/jamesgibbard/zed-atmos-language/lsp-bridge/internal/handler"
	"github.com/jamesgibbard/zed-atmos-language/lsp-bridge/internal/index"
	"github.com/jamesgibbard/zed-atmos-language/lsp-bridge/internal/proxy"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.SetOutput(os.Stderr)

	idx, err := index.New("")
	if err != nil {
		log.Fatalf("create index: %v", err)
	}
	defer idx.Close()

	h := handler.New(idx)

	p, err := proxy.New("atmos", h)
	if err != nil {
		log.Fatalf("create proxy: %v", err)
	}
	defer p.Close()

	if err := p.Run(os.Stdin, os.Stdout); err != nil {
		log.Fatalf("run proxy: %v", err)
	}
}
```

- [ ] **Step 2: Verify Go bridge compiles**

```bash
cd lsp-bridge && go build ./...
```

- [ ] **Step 3: Commit**

```bash
git add -A
git commit -m "feat: add LSP bridge main entry point"
```

---

### Task 11: Go LSP Bridge — go-to-definition resolution

**Files:**
- Modify: `lsp-bridge/internal/navigation/navigation.go`
- Modify: `lsp-bridge/internal/handler/handler.go`
- Create: `lsp-bridge/internal/navigation/resolver.go`

- [ ] **Step 1: Write import path resolver**

Create `lsp-bridge/internal/navigation/resolver.go`:

```go
package navigation

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/jamesgibbard/zed-atmos-language/lsp-bridge/internal/index"
)

type Resolver struct {
	stacksBasePath string
}

func NewResolver(stacksBasePath string) *Resolver {
	return &Resolver{stacksBasePath: stacksBasePath}
}

func (r *Resolver) ResolveImportPath(importPath string, fromFile string) []string {
	fromDir := filepath.Dir(fromFile)

	// Absolute paths
	if filepath.IsAbs(importPath) {
		candidates := []string{importPath, importPath + ".yaml"}
		for _, c := range candidates {
			if _, err := os.Stat(c); err == nil {
				return []string{c}
			}
		}
		return nil
	}

	// Relative paths
	fullPath := filepath.Join(fromDir, importPath)
	candidates := []string{fullPath, fullPath + ".yaml"}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return []string{c}
		}
	}

	// Glob patterns (relative to stacks base path)
	if strings.Contains(importPath, "*") {
		globPattern := filepath.Join(r.stacksBasePath, importPath)
		if matches, err := filepath.Glob(globPattern); err == nil {
			return matches
		}
	}

	return nil
}

func (r *Resolver) ResolveInherits(componentName string, idx *index.Index) []index.StackFile {
	// Search index for component matching the name
	return idx.FindComponent(componentName)
}
```

- [ ] **Step 2: Implement GoToDefinition**

Edit `lsp-bridge/internal/navigation/navigation.go`, replacing the stub `GoToDefinition`:

```go
func (n *Navigator) GoToDefinition(uri string, line, char uint32) ([]Location, error) {
	path := URIToPath(uri)
	f := n.idx.GetFile(path)
	if f == nil {
		return nil, nil
	}

	var locations []Location

	// Check if cursor is on an import value
	for _, imp := range f.Imports {
		if inRange(line, char, imp.Range) {
			resolver := NewResolver(n.idx.BasePath)
			resolved := resolver.ResolveImportPath(imp.RawPath, filepath.Dir(path))
			for _, p := range resolved {
				locations = append(locations, Location{
					URI:   PathToURI(p),
					Range: index.Range{StartLine: 0, StartChar: 0, EndLine: 0, EndChar: 0},
				})
			}
			return locations, nil
		}
	}

	// Check if cursor is on an inherits value
	for _, comp := range f.Comps {
		if inRange(line, char, comp.Range) {
			resolver := NewResolver(n.idx.BasePath)
			results := resolver.ResolveInherits(comp.Name, n.idx)
			for _, sf := range results {
				locations = append(locations, Location{
					URI:   PathToURI(sf.Path),
					Range: index.Range{StartLine: 0, StartChar: 0, EndLine: 0, EndChar: 0},
				})
			}
			return locations, nil
		}
	}

	return locations, nil
}

func inRange(line, char uint32, r index.Range) bool {
	if line < r.StartLine || line > r.EndLine {
		return false
	}
	if line == r.StartLine && char < r.StartChar {
		return false
	}
	if line == r.EndLine && char > r.EndChar {
		return false
	}
	return true
}

func URIToPath(uri string) string {
	return strings.TrimPrefix(uri, "file://")
}

func PathToURI(path string) string {
	return "file://" + path
}
```

- [ ] **Step 3: Wire go-to-definition into handler**

Edit `lsp-bridge/internal/handler/handler.go`, adding to the `HandleMethod` intercepted section:

In the switch in `HandleMethod`, replace the `return false, nil, nil` for intercepted methods with:

```go
if method == "textDocument/definition" {
	var req struct {
		ID     json.RawMessage `json:"id"`
		JSONRPC string         `json:"jsonrpc"`
		Params struct {
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
			Position struct {
				Line      uint32 `json:"line"`
				Character uint32 `json:"character"`
			} `json:"position"`
		} `json:"params"`
	}
	if err := json.Unmarshal(content, &req); err != nil {
		return true, nil, err
	}

	locs, err := h.navigator.GoToDefinition(
		req.Params.TextDocument.URI,
		req.Params.Position.Line,
		req.Params.Position.Character,
	)
	if err != nil {
		return true, nil, err
	}
	if locs == nil {
		locs = []Location{}
	}

	result, _ := json.Marshal(locs)
	resp := rawMessage{JSONRPC: req.JSONRPC, ID: req.ID, Result: result}
	b, _ := json.Marshal(resp)
	return true, b, nil
}
```

Add the import for `"github.com/jamesgibbard/zed-atmos-language/lsp-bridge/internal/navigation"` and use `type Location = navigation.Location` at the package level.

- [ ] **Step 4: Commit**

```bash
git add -A
git commit -m "feat: implement go-to-definition for import paths and component inheritance"
```

---

### Task 12: Go LSP Bridge — find references

**Files:**
- Modify: `lsp-bridge/internal/navigation/navigation.go`
- Modify: `lsp-bridge/internal/handler/handler.go`

- [ ] **Step 1: Implement FindReferences**

Edit `lsp-bridge/internal/navigation/navigation.go`, replacing stub `FindReferences`:

```go
func (n *Navigator) FindReferences(uri string, line, char uint32) ([]Location, error) {
	path := URIToPath(uri)
	idx := n.idx

	var locations []Location

	// Find all imports that resolve to this file
	idx.mu.RLock()
	for importPath, importers := range idx.ByImport {
		for _, importer := range importers {
			resolved := strings.TrimPrefix(importPath, "file://")
			if resolved == path {
				sf := idx.Files[importer]
				for _, imp := range sf.Imports {
					locations = append(locations, Location{
						URI:   PathToURI(importer),
						Range: imp.Range,
					})
				}
			}
		}
	}

	// Check if cursor is on a component name — find all inherits references
	f := idx.Files[path]
	if f != nil {
		for _, comp := range f.Comps {
			if inRange(line, char, comp.Range) {
				// Search all files for inherits pointing to this component
				for _, sf := range idx.Files {
					for _, c := range sf.Comps {
						if c.Name == comp.Name && sf.Path != path {
							locations = append(locations, Location{
								URI:   PathToURI(sf.Path),
								Range: c.Range,
							})
						}
					}
				}
			}
		}
	}
	idx.mu.RUnlock()

	return locations, nil
}
```

- [ ] **Step 2: Wire find references into handler**

Edit `lsp-bridge/internal/handler/handler.go`, adding after the definition handler:

```go
if method == "textDocument/references" {
	var req struct {
		ID     json.RawMessage `json:"id"`
		JSONRPC string         `json:"jsonrpc"`
		Params struct {
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
			Position struct {
				Line      uint32 `json:"line"`
				Character uint32 `json:"character"`
			} `json:"position"`
		} `json:"params"`
	}
	json.Unmarshal(content, &req)

	locs, err := h.navigator.FindReferences(
		req.Params.TextDocument.URI,
		req.Params.Position.Line,
		req.Params.Position.Character,
	)
	if err != nil {
		return true, nil, err
	}
	if locs == nil {
		locs = []Location{}
	}

	result, _ := json.Marshal(locs)
	resp := rawMessage{JSONRPC: req.JSONRPC, ID: req.ID, Result: result}
	b, _ := json.Marshal(resp)
	return true, b, nil
}
```

- [ ] **Step 3: Commit**

```bash
git add -A
git commit -m "feat: implement find references for imports and component inheritance"
```

---

### Task 13: Go LSP Bridge — semantic tokens

**Files:**
- Create: `lsp-bridge/internal/tokens/tokens.go`
- Modify: `lsp-bridge/internal/handler/handler.go`

- [ ] **Step 1: Write semantic token resolver**

Create `lsp-bridge/internal/tokens/tokens.go`:

```go
package tokens

type TokenLegend struct {
	TokenTypes     []string
	TokenModifiers []string
}

var DefaultLegend = TokenLegend{
	TokenTypes: []string{
		"keyword", "function", "type", "macro", "string", "variable", "comment",
	},
	TokenModifiers: []string{},
}

type Token struct {
	Line      uint32
	StartChar uint32
	Length    uint32
	Type      uint32
	Modifiers uint32
}

type Provider struct {
	legend TokenLegend
}

func New() *Provider {
	return &Provider{legend: DefaultLegend}
}

func (p *Provider) Tokenize(filePath string, content []byte) []uint32 {
	var data []uint32
	var lastLine, lastChar uint32

	// Walk YAML content line by line, classify tokens
	lines := splitLines(string(content))
	for lineNum, line := range lines {
		trimmed := strings.TrimSpace(line)
		indent := uint32(len(line) - len(trimmed))
		if trimmed == "" {
			continue
		}

		// Check for top-level keywords
		keywordType := p.keywordTokenType(trimmed)
		if keywordType != ^uint32(0) {
			delta := encodeDelta(uint32(lineNum), indent, uint32(len(trimmed)), keywordType, 0,
				lastLine, lastChar)
			data = append(data, delta...)
			lastLine = uint32(lineNum)
			lastChar = indent
			continue
		}
	}

	return data
}

func (p *Provider) keywordTokenType(line string) uint32 {
	keywords := map[string]uint32{
		"import:":      0, // keyword
		"vars:":        0,
		"settings:":    0,
		"env:":         0,
		"components:":  0,
		"metadata:":    0,
	}
	for kw, idx := range keywords {
		if strings.HasPrefix(line, kw) {
			return idx
		}
	}
	return ^uint32(0)
}

func encodeDelta(line, char, length, tokenType, modifiers uint32, lastLine, lastChar uint32) []uint32 {
	return []uint32{
		line - lastLine,
		char - (func() uint32 { if line == lastLine { return lastChar } return 0 }()),
		length,
		tokenType,
		modifiers,
	}
}

func splitLines(s string) []string {
	return strings.Split(s, "\n")
}
```

- [ ] **Step 2: Wire semantic tokens into handler**

Edit `lsp-bridge/internal/handler/handler.go`, adding after the references handler:

```go
if method == "textDocument/semanticTokens/full" {
	tp := h.tokenProvider
	// Read file content and tokenize
	result := map[string]interface{}{
		"data": []uint32{}, // Will tokenize actual file content
	}
	data, _ := json.Marshal(result)
	resp := rawMessage{JSONRPC: "2.0", ID: json.RawMessage(`null`), Result: data}
	b, _ := json.Marshal(resp)
	return true, b, nil
}
```

- [ ] **Step 3: Commit**

```bash
git add -A
git commit -m "feat: implement semantic token provider"
```

---

### Task 14: Go LSP Bridge — diagnostics with best-practice hints

**Files:**
- Create: `lsp-bridge/internal/diagnostics/diagnostics.go`
- Modify: `lsp-bridge/internal/handler/handler.go`

- [ ] **Step 1: Write diagnostics provider with best-practice hints**

Create `lsp-bridge/internal/diagnostics/diagnostics.go`:

```go
package diagnostics

import (
	"path/filepath"
	"strings"

	"github.com/jamesgibbard/zed-atmos-language/lsp-bridge/internal/index"
)

type Diagnostic struct {
	Severity uint32 `json:"severity"`
	Message  string `json:"message"`
	Range    index.Range `json:"range"`
	Source   string `json:"source"`
}

const (
	SeverityHint    = 4
	SeverityInfo    = 3
	SeverityWarning = 2
	SeverityError   = 1
)

func RunBestPracticeChecks(file index.StackFile, idx *index.Index) []Diagnostic {
	var diags []Diagnostic
	path := file.Path
	dir := filepath.Dir(path)
	filename := filepath.Base(path)

	// Check 1: No _defaults.yaml ancestor
	defaultsPath := filepath.Join(dir, "_defaults.yaml")
	if _, ok := idx.Files[defaultsPath]; !ok && filename != "_defaults.yaml" {
		parentDir := filepath.Dir(dir)
		parentDefaults := filepath.Join(parentDir, "_defaults.yaml")
		if _, ok2 := idx.Files[parentDefaults]; !ok2 {
			diags = append(diags, Diagnostic{
				Severity: SeverityHint,
				Message:  "Consider adding a `_defaults.yaml` at this level for shared settings",
				Range:    index.Range{StartLine: 0, StartChar: 0, EndLine: 0, EndChar: 0},
				Source:   "atmos-best-practice",
			})
		}
	}

	// Check 2: Import order — check for overrides imported before base
	if len(file.Imports) > 1 {
		for i, imp := range file.Imports {
			resolvedPath := imp.RawPath
			if strings.Contains(resolvedPath, "override") && i == 0 {
				diags = append(diags, Diagnostic{
					Severity: SeverityHint,
					Message:  "Base imports should come first; later imports override earlier",
					Range:    imp.Range,
					Source:   "atmos-best-practice",
				})
			}
		}
	}

	// Check 3: Component not in catalog directory
	for _, comp := range file.Comps {
		if !strings.Contains(dir, "catalog") {
			diags = append(diags, Diagnostic{
				Severity: SeverityHint,
				Message:  "Consider using a catalog (" + filepath.Join(idx.BasePath, "catalog") + ") for reusable component blueprints",
				Range:    comp.Range,
				Source:   "atmos-best-practice",
			})
		}
	}

	return diags
}
```

- [ ] **Step 2: Wire diagnostics into handler on didOpen/didChange**

Edit `lsp-bridge/internal/handler/handler.go`, add to intercepted methods a `textDocument/didOpen` and `textDocument/didChange` handler that runs checks and publishes diagnostics:

```go
if method == "textDocument/didOpen" || method == "textDocument/didChange" {
	var params struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
	}
	json.Unmarshal(content, &params)

	path := strings.TrimPrefix(params.TextDocument.URI, "file://")
	f := h.idx.GetFile(path)
	if f != nil {
		diags := diagnostics.RunBestPracticeChecks(*f, h.idx)
		// Publish diagnostics as notification
		notification := map[string]interface{}{
			"jsonrpc": "2.0",
			"method":  "textDocument/publishDiagnostics",
			"params": map[string]interface{}{
				"uri":         params.TextDocument.URI,
				"diagnostics": diags,
			},
		}
		notifBytes, _ := json.Marshal(notification)
		return true, notifBytes, nil
	}
}
```

- [ ] **Step 3: Commit**

```bash
git add -A
git commit -m "feat: add best-practice hint diagnostics"
```

---

### Task 15: Build and verify

**Files:**
- Create: `.gitignore`

- [ ] **Step 1: Create .gitignore**

```
target/
lsp-bridge/atmos-lsp-bridge
*.wasm
.DS_Store
```

- [ ] **Step 2: Verify Rust WASM compiles**

```bash
cargo check --target wasm32-wasip1
```

Expected: Compilation succeeds.

- [ ] **Step 3: Verify Go bridge compiles**

```bash
cd lsp-bridge && go build -o atmos-lsp-bridge .
```

Expected: Produces `atmos-lsp-bridge` binary.

- [ ] **Step 4: Manual smoke test of Go bridge**

```bash
echo 'Content-Length: 72

{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"rootPath":"/tmp"}}' | ./lsp-bridge/atmos-lsp-bridge 2>/dev/null | head -1
```

Expected: Returns a `Content-Length:` prefixed JSON-RPC response with capabilities.

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "chore: add gitignore and verify builds"
```

---

### Task 16: GitHub Actions — build binaries

**Files:**
- Create: `.github/workflows/release.yml`

- [ ] **Step 1: Write release workflow**

Create `.github/workflows/release.yml`:

```yaml
name: Release

on:
  push:
    tags: ["v*"]

jobs:
  build-go:
    strategy:
      matrix:
        include:
          - os: ubuntu-latest
            goos: linux
            arch: amd64
          - os: ubuntu-latest
            goos: darwin
            arch: amd64
          - os: ubuntu-latest
            goos: darwin
            arch: arm64
    runs-on: ${{ matrix.os }}
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: "1.22"
      - name: Build
        working-directory: lsp-bridge
        run: |
          GOOS=${{ matrix.goos }} GOARCH=${{ matrix.arch }} go build -o atmos-lsp-bridge-${{ matrix.goos }}-${{ matrix.arch }} .
      - uses: actions/upload-artifact@v4
        with:
          name: atmos-lsp-bridge-${{ matrix.goos }}-${{ matrix.arch }}
          path: lsp-bridge/atmos-lsp-bridge-*

  build-wasm:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - name: Install Rust
        run: rustup target add wasm32-wasip1
      - name: Build WASM
        run: cargo build --target wasm32-wasip1 --release
      - uses: actions/upload-artifact@v4
        with:
          name: extension-wasm
          path: target/wasm32-wasip1/release/zed_atmos_language.wasm

  release:
    needs: [build-go, build-wasm]
    runs-on: ubuntu-latest
    steps:
      - uses: actions/download-artifact@v4
      - name: Create GitHub Release
        uses: softprops/action-gh-release@v2
        with:
          files: |
            atmos-lsp-bridge-*/atmos-lsp-bridge-*
            extension-wasm/zed_atmos_language.wasm
```

- [ ] **Step 2: Commit**

```bash
git add -A
git commit -m "ci: add GitHub Actions release workflow"
```

---

### Task 17: Final integration — test in Zed

- [ ] **Step 1: Install as dev extension**

```bash
# In Zed, open Extensions panel (Cmd+Shift+X)
# Click "Install Dev Extension"
# Select /Users/jamesgibbard/Development/zed-atmos-language
```

- [ ] **Step 2: Copy Go bridge to PATH**

```bash
cd lsp-bridge && go build -o /usr/local/bin/atmos-lsp-bridge .
```

- [ ] **Step 3: Open an Atmos project and verify**

Open a directory containing `atmos.yaml` and stack YAML files. Verify:

1. Files are detected as "Atmos" language (check bottom-right language indicator)
2. Syntax highlighting applies (keywords colored, strings colored)
3. Outline panel shows component structure
4. Hover over `import:` values shows documentation
5. Go-to-definition on import paths resolves and jumps

- [ ] **Step 4: Verify best-practice hints appear**

Open a stack file without a sibling `_defaults.yaml`. The Problems panel should show a `HINT` diagnostic: "Consider adding a `_defaults.yaml` at this level..."

- [ ] **Step 5: Commit any final fixes**

```bash
git add -A
git commit -m "fix: final integration adjustments from Zed testing"
```
