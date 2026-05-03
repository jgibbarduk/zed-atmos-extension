# Atmos Language Extension for Zed — Design Spec

**Date:** 2026-05-03
**Status:** Draft

## Overview

A Zed IDE language extension for [Atmos](https://atmos.tools) that provides syntax highlighting, code outline, and advanced navigation across Atmos's hierarchical stack configuration model (imports, inheritance, deep merges).

## Architecture

```
zed-atmos-language/
├── extension.toml              # Zed extension metadata
├── Cargo.toml                  # Rust WASM — thin LSP launcher only
├── src/
│   └── lib.rs                  # Downloads & launches the Go LSP binary
├── languages/
│   └── atmos/
│       ├── config.toml         # Language config (reuses tree-sitter-yaml)
│       ├── highlights.scm      # Atmos-aware syntax highlighting
│       ├── outline.scm         # Stack/component structure outline
│       ├── indents.scm         # Indentation rules
│       └── brackets.scm        # Bracket matching
└── lsp/
    ├── go.mod
    ├── main.go                 # LSP server entry point
    └── internal/
        ├── handler/            # LSP protocol handlers
        ├── parser/             # YAML parsing & import resolution
        ├── resolver/           # Import chain & inheritance graph
        └── index/              # File watcher → cross-reference index
```

### Components

| Component | Language | Role |
|---|---|---|
| Zed extension shim | Rust → WASM | Downloads Go LSP binary from GitHub Releases, spawns it as a child process |
| Tree-sitter queries | Scheme (.scm) | Syntax highlighting, outline, brackets, indentation |
| Go LSP server | Go | Full language intelligence: import resolution, inheritance tracking, cross-references |
| Language config | TOML | File association, comment syntax, grammar binding |

## File Association

Atmos files are YAML files. We use two cooperative strategies to distinguish them from plain YAML:

1. **`atmos.yaml` proximity:** Files under `stacks.base_path` (relative to discovered `atmos.yaml`) are Atmos stack files.
2. **First-line/content pattern:** Root-level `import:`, `components:`, or `vars:` keys signal an Atmos stack file.

The language `config.toml` uses `path_suffixes = ["yaml", "yml"]` with `first_line_pattern` matching these keys.

## Syntax Highlighting

Reuses `tree-sitter-yaml` grammar. Custom `highlights.scm` adds Atmos-specific captures:

| Capture | Target |
|---|---|
| `@keyword` | `import`, `vars`, `settings`, `env`, `components`, `metadata`, `terraform`, `helmfile`, `provider`, `backend`, `overrides` |
| `@string.special` | Values under `import:` keys (file paths and glob patterns) |
| `@function` | Component names under `components.terraform.*` and `components.helmfile.*` |
| `@type` | `metadata.component` value (underlying Terraform module reference) |
| `@keyword` | `metadata.inherits` key |
| `@label` | Go template expressions (`{{ .vars.region }}`) |

## Outline Panel

Displays a hierarchical tree of stacks and their components:

```
▸ tenant1-ue2-prod (stack)
  ▸ terraform
    ▸ vpc/1
    ▸ vpc-flow-logs-bucket
    ▸ eks/cluster
  ▸ helmfile
    ▸ cert-manager
    ▸ ingress-nginx
```

Built via `outline.scm` capturing top-level component keys under `components.terraform.*` and `components.helmfile.*`.

## LSP Features (v1)

### Go-to-Definition

**On an import path** (value under `import:`):
- Resolve relative path from importing file's directory, append `.yaml` if needed
- Expand globs via filepath.Glob (relative to `stacks.base_path`)
- Evaluate Go template expressions using context variables from the file's `vars:` section and import context
- Jump to the resolved file

**On an `inherits` value** (under `metadata.inherits:`):
- Search all indexed files for a component whose key matches the inherited name
- Support relative inheritance paths (sibling/ancestor components)
- Support list-form multiple inheritance

### Find References

- Given a file path: return all import statements across the workspace that resolve to this file
- Given a component name: return all `inherits` references that point to it

### Hover

- Over a setting value: walk the import chain and display which files contributed, in merge order
- Shows the effective value and where it was set/overridden

### Diagnostics

- Unresolved import paths → `WARNING`
- Circular imports → `ERROR`
- Go template parse/evaluation failures → `ERROR` or `WARNING`
- Remote imports (HTTPS URLs) → `INFO` (not processed by this LSP)

## LSP Internals

### Index

```go
type Index struct {
    mu       sync.RWMutex
    files    map[string]*StackFile  // path -> parsed file
    comps    map[string][]Location  // component name -> all definitions
    byImport map[string][]string    // path -> files that import it
}

type StackFile struct {
    Path    string
    Imports []ImportNode
    Comps   []CompNode
}

type ImportNode struct {
    Path     string   // raw import path (may have globs/templates)
    Range    Range    // source position
    Resolves []string // resolved filesystem paths
}
```

### Workspace Detection

On `initialize`, search upward from workspace root for `atmos.yaml`. Read `stacks.base_path`, `stacks.included_paths`, `stacks.excluded_paths` to determine which directories to watch.

### File Watching

- Watch `stacks.base_path` recursively via fsnotify
- Debounce re-indexing at 200ms
- On delete: remove from index, clear associated diagnostics

## Distribution

Pre-compiled Go binaries for macOS (arm64/amd64) and Linux (amd64) hosted on GitHub Releases. The Rust WASM shim downloads the correct binary to a cache directory on first launch. Binaries are ~10MB.

## Testing

| Level | Scope | Approach |
|---|---|---|
| LSP protocol | handler correctness | `go test` with YAML fixture files |
| Import resolution | globs, templates, paths | Table-driven `go test` |
| Cross-file references | Index updates, diagnostics | Integration test against fixture repo in `lsp/testdata/` |
| Extension packaging | WASM compiles, TOML valid | `cargo check --target wasm32-wasip1` |
| End-to-end | Real Atmos repo in Zed | Manual QA checklist |
