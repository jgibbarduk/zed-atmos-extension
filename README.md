# Zed Atmos Extension

A [Zed](https://zed.dev) editor extension for [Atmos](https://atmos.tools) stack configurations. It provides rich language support for YAML-based Atmos stack files, including Go template expression resolution, import navigation, component definition jumping, and best-practice diagnostics.

## Features

- **Go-to-definition** — Jump from component names, imports, `metadata.component`, `metadata.inherits`, and `!terraform.state` references to their definitions
- **Hover information** — See resolved import paths, accumulated variables, component definitions, and computed stack names from `atmos.yaml` `name_template`
- **Template resolution** — `{{ .vars.namespace }}`, `{{ .atmos_component }}`, and other template expressions are resolved in hover tooltips
- **Rename** — Rename component references across the entire workspace
- **Code actions** — Generate component scaffolds for missing components
- **Completion** — Suggest known component names inside `dependencies.components`, `settings.depends_on`, and `terraform.state`; suggest template variables inside `{{ ... }}`; suggest paths for imports and `metadata.component`
- **Diagnostics** — 20+ checks including unresolvable imports, duplicate components, missing dependency references, unquoted versions, circular imports, invalid backend types, and abstract components with no inheritors

## Installation

### From Source

```bash
git clone https://github.com/jgibbarduk/zed-atmos-extension.git
cd zed-atmos-extension

# Build the LSP bridge binary
cd lsp-bridge && go build -o atmos-lsp-bridge . && cd ..

# Install the extension in Zed
zed --install-extension .
```

Make sure `atmos-lsp-bridge` is in your `PATH`. The Zed extension looks for it at runtime.

## Requirements

- [Zed](https://zed.dev) editor
- [Go](https://go.dev) 1.22+ (to build the LSP bridge)
- [Atmos](https://atmos.tools) CLI installed (optional — the extension works without it, but some features proxy to the Atmos LSP)

## Architecture

The extension consists of two layers:

| Layer | Language | Role |
|-------|----------|------|
| **Zed Extension** | Rust (`src/lib.rs`) | Thin WASM wrapper that registers the `atmos-lsp-bridge` binary as the language server |
| **LSP Bridge** | Go (`lsp-bridge/...`) | The actual language server. Handles LSP methods directly and proxies unsupported methods to the downstream `atmos lsp start --transport stdio` process |

### Go packages

- `lsp-bridge/internal/handler/` — Core LSP method router
- `lsp-bridge/internal/index/` — File watcher and in-memory indexes of stack files
- `lsp-bridge/internal/proxy/` — Downstream proxy to the real `atmos` CLI LSP
- `lsp-bridge/internal/navigation/` — Navigation helpers

## Development

### Running tests

```bash
cd lsp-bridge
go test ./...
```

### Manual testing

Use the test project at `~/Development/atmos-test-project` (or create your own) to manually test the extension in Zed. Open any Atmos `.yaml` stack file and verify hover, cmd+click navigation, rename, and diagnostics.

### Building

```bash
# Build the Go binary
cd lsp-bridge && go build -o atmos-lsp-bridge .

# Install the extension
zed --install-extension .
```

## License

MIT
