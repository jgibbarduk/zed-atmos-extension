# Atmos Language Extension for Zed — Design Spec v2

**Date:** 2026-05-03
**Status:** Finalized (post-architect-review)

## Overview

A Zed IDE language extension for [Atmos](https://atmos.tools) providing syntax highlighting, code outline, advanced navigation (imports, inheritance, deep merges), best-practice diagnostics, and LSP proxying to `atmos lsp start`.

## Architecture

```
zed-atmos-language/
├── extension.toml                 # Zed extension manifest
├── Cargo.toml                     # Rust WASM — thin LSP launcher
├── src/lib.rs                     # Discovers & launches the Go LSP bridge binary
├── languages/atmos/
│   ├── config.toml                # Language detection (first_line_pattern + path_suffixes)
│   ├── highlights.scm             # Syntax highlighting queries
│   ├── outline.scm                # Structure outline
│   ├── indents.scm                # Indentation rules
│   └── brackets.scm               # Bracket matching
├── lsp-bridge/                    # Go LSP proxy binary
│   ├── main.go                    # Entry point
│   └── internal/
│       ├── proxy/                 # LSP stdio transport + forward-to-atmos loop
│       ├── handler/               # LSP method interception (definition, references, hover, etc.)
│       ├── index/                 # YAML parsing, file index, cross-reference maps
│       ├── navigation/            # Import resolution, inheritance tracking
│       ├── diagnostics/           # Best-practice checks
│       └── lsp/                   # LSP protocol types + error responses
└── icons/
    └── file.svg                   # Atmos logo for file icon theme
```

### Components

| Component | Language | Role |
|---|---|---|
| Zed extension shim | Rust → WASM | Discovers Go LSP bridge binary, spawns it |
| Tree-sitter queries | Scheme (.scm) | Syntax highlighting, outline, brackets, indentation |
| Go LSP bridge | Go | Proxy: intercepts navigation/diagnostics methods, forwards rest to `atmos lsp start` |
| Language config | TOML | File association, comment syntax, grammar binding |

## Workstream A: LSP Proxy Fixes (Critical Correctness)

### A1: Forward `initialize` downstream

**Problem:** `handleInitialize` intercepts `initialize` and returns bridge-only capabilities. The downstream `atmos lsp start` process never receives the handshake, so all forwarded requests (completions, formatting, etc.) silently fail.

**Fix:** Forward `initialize` to `atmos lsp start` first, capture its capabilities response, merge the bridge's additional capabilities on top, return the merged result. This is the standard LSP proxy pattern.

**Key implementation detail:** The proxy loop currently sends messages via `sendLoop()` goroutine. We need a synchronous request/response mechanism for the downstream process so `initialize` can wait for `atmos` to respond before returning. Add a `callDownstream(method, params)` function that sends a request, reads the matching response, and returns it.

### A2: Activate file watcher

**Problem:** `Index.StartWatching()` exists with fsnotify + debounced re-index but is never called.

**Fix:** Call `idx.StartWatching(onChange)` from `handleInitialized` after the base path is set. The `onChange` callback should publish `textDocument/publishDiagnostics` notifications for all open files so diagnostics stay fresh.

### A3: Handle `textDocument/didSave`

**Problem:** Handler intercepts `didOpen`/`didChange` but not `didSave`. After saving, neither diagnostics refresh nor the index updates.

**Fix:** Add `textDocument/didSave` to the handler dispatch. On save: re-parse the saved file, update the index maps for that file, and re-run diagnostics for all open files that import it.

### A4: Propagate `shutdown`/`exit`

**Problem:** Handler intercepts `shutdown` and just sets `h.initialized = false`. The downstream `atmos lsp` process never receives shutdown/exit.

**Fix:** Forward `shutdown` to the downstream process. Wait for the response (or timeout). Send `exit` notification. Kill the process if it doesn't exit within a grace period.

### A5: Improved hover content

**Problem:** Hover only shows `Import: <rawPath>` or `Component: <name>`.

**Fix:** For imports, show the resolved file path(s) along with file-existence status. For components, show the type (terraform/helmfile), and list the inheritance chain (which other stacks define or override this component).

## Workstream B: Extension Manifest

### B1: Add `languages` field

`extension.toml` is missing `languages = ["languages/atmos"]` — without this, Zed doesn't know where to find the language config.

### B2: Add `capabilities` declaration

Declare `capabilities = ["process_execution"]` since the extension spawns the LSP bridge binary.

### B3: Verify grammar config

Verify grammar uses `commit` with full SHA (not `rev` with tag) since Zed shallow-clones and tags don't resolve.

## Workstream C: Syntax Highlighting

### C1: Research tree-sitter-yaml node types

Read the tree-sitter-yaml grammar (`grammar.js`) to identify all valid node types: `block_mapping_pair`, `flow_sequence`, `block_scalar`, `double_quote_scalar`, `single_quote_scalar`, `integer_scalar`, `float_scalar`, `boolean_scalar`, `null_scalar`, `string_scalar`, `anchor`, `alias`, `tag`, `error`, etc.

### C2: Write full highlights.scm

Cover all LSP semantic token types mapped to tree-sitter captures:
- `@comment` — comments
- `@string` — all string scalar types
- `@number` — integers and floats
- `@boolean` — true/false
- `@constant` — null
- `@keyword` — block mapping keys that match Atmos keywords (import, vars, settings, etc.)
- `@type` — metadata.type value, component type keys
- `@function` — component names under terraform/helmfile
- `@string.special` — unquoted import path values
- `@label` — Go template expressions

### C3: Verify other query files

Ensure `brackets.scm`, `outline.scm`, and `indents.scm` are syntactically valid and functional.

## Workstream D: File Icon Theme

### Problem

Zed icon themes map by `file_suffixes` or `file_stems`, not by content pattern. Since Atmos uses `.yaml` suffix (same as regular YAML), we can't distinguish Atmos YAML from plain YAML via file extension alone.

### Approach

Ship a companion icon theme extension that replaces the YAML file icon with the Atmos logo. Rationale: users who install the Atmos extension are working in Atmos repos where `.yaml` = Atmos YAML. The icon theme is a separate extension so users can choose to use it or stick with their existing icon theme.

### Structure

```
atmos-icon-theme/
├── extension.toml
├── icon_themes/
│   └── atmos-icon-theme.json
└── icons/
    └── file.svg  (already downloaded)
```

The `atmos-icon-theme.json` maps `yaml`/`yml` suffixes → the Atmos logo SVG.

## Workstream E: Operational Polish

### E1: Structured logging

Add `--debug` flag to the bridge binary. `log/slog` with levels. Debug mode logs all LSP messages; normal mode logs only errors and startup info.

### E2: User-facing configuration

Support these settings via `initialization_options` or workspace configuration:
- `atmos_cli_path` — override path to `atmos` binary
- `stacks_path` — override stacks base path (skip atmos.yaml parsing)
- `diagnostics_enabled` — toggle best-practice diagnostics
- `log_level` — "debug", "info", "warn", "error"

### E3: Integration tests

Add tests for the proxy→handler pipeline:
- Full initialize→initialized→definition→shutdown lifecycle
- Downstream forwarding (mock atmos LSP)
- Error recovery when downstream crashes

### E4: Incremental index updates

Instead of full walk on every re-index, parse only changed files and update the index maps incrementally. Keeps the current full-walk Reindex() as a fallback for initial build.

## Implementation Order

1. **First:** Workstream A (proxy fixes) + Workstream B (manifest) — unblock functionality
2. **Then in parallel:** Workstream C (highlighting) + Workstream D (icon theme) + Workstream E (polish)
3. **Finally:** End-to-end integration test against the CloudPosse test project

## Testing Plan

| Level | Scope | Approach |
|---|---|---|
| Unit | parser, resolver, diagnostics | Existing `go test` suite (all passing) |
| Unit | proxy sync request/response | New `go test` with mock atmos process |
| Integration | Initialize→forward→merge | Test handler with real LSP messages |
| Integration | File watcher | Test fsnotify triggers re-index |
| End-to-end | Real Atmos repo in Zed | Manual QA: definition, references, hover, diagnostics, completions |
