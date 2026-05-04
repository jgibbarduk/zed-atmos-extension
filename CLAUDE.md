# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

This is a **Zed editor extension** for Atmos stack configurations. It provides language server features (go-to-definition, hover, rename, code actions, diagnostics) for YAML-based Atmos stack files that use Go template syntax.

## Architecture

The project has two main layers:

| Layer | Language | Role |
|-------|----------|------|
| **Zed Extension** | Rust (`src/lib.rs`) | Thin WASM wrapper that registers the `atmos-lsp-bridge` binary as the language server |
| **LSP Bridge** | Go (`lsp-bridge/...`) | The actual language server. Handles LSP methods directly and proxies everything else to the downstream `atmos lsp start --transport stdio` process |

### Key Go packages

- `lsp-bridge/internal/handler/` — Core LSP method router (definition, hover, rename, codeAction, diagnostics)
- `lsp-bridge/internal/index/` — File watcher and in-memory indexes (`byImport`, `byComponent`, `byInherit`)
- `lsp-bridge/internal/proxy/` — Downstream proxy to the real `atmos` CLI LSP
- `lsp-bridge/internal/navigation/` — Navigation helpers (currently a stub)

### Language configuration

- `languages/atmos/` — Tree-sitter config, highlighting queries, and outline queries
- `languages/atmos/config.toml` — Language settings (word characters include `-`, `_`, `.`, `/`)
- `icon-theme/` — Optional icon theme extension

## How to Build

```bash
# Build the Go binary
cd lsp-bridge && go build -o atmos-lsp-bridge .

# Install the Zed extension (from repo root)
zed --install-extension .
```

## How to Test

```bash
cd lsp-bridge
go test ./...
```

## Manual Testing

Use the test project at `~/Development/atmos-test-project` to manually test the extension in Zed. Open any `.yaml` stack file in that project and verify:

- **Hover**: Shows resolved imports, component definitions, accumulated vars, and stack name preview
- **cmd+click**: Jumps from component names to their definitions, from imports to imported files
- **Rename**: Renames component references across the workspace
- **Code actions**: Offers to generate component scaffolds for missing components

## Important Conventions

### Go module path
The module path is `github.com/jgibbarduk/zed-atmos-extension/lsp-bridge`. If you change this, update `go.mod` and all import statements, then run `go mod tidy`.

### Template variable resolution
The LSP resolves Atmos template expressions like `{{ .vars.namespace }}` and `{{ .atmos_component }}`. Key functions:
- `interpolateNameTemplate()` — resolves `name_template` from `atmos.yaml`
- `findTemplateExpressionAtPosition()` — resolves template expressions in var values for hover tooltips

Component-scoped vars track their parent component via `VarNode.Component` (set during parsing in `index/parser.go`). This is used to resolve `{{ .atmos_component }}` correctly regardless of how deep a var is nested inside a component.

### Line-based range matching
Most LSP handlers use line-based range checks (`StartLine <= line && EndLine >= line`) rather than character-precise matching. This is intentional — it makes the LSP forgiving about exact cursor position within a YAML node.

### Git workflow
- The default branch is `main`
- Origin is `https://github.com/jgibbarduk/zed-atmos-extension`

## Common Tasks

- **Add a new LSP feature**: Add a case in `handler.go:HandleMethod()` and implement a `handleXxx()` method
- **Change how YAML is parsed**: Edit `index/parser.go` (e.g., to add new node types to `StackFile`)
- **Update grammar queries**: Edit files in `languages/atmos/` (SCM queries for Tree-sitter)
- **Update the Zed extension manifest**: Edit `extension.toml` (version, description, capabilities)
