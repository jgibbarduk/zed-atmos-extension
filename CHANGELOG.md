# Changelog

## 0.1.0 (2025-05-05)

### Features
- Atmos stack file language support for Zed
- Go-to-definition for imports, components, metadata, and terraform state references
- Hover tooltips showing resolved imports, accumulated vars, and stack name preview
- Rename refactoring across workspace
- Code actions to generate component scaffolds
- 17 diagnostic checks (imports, components, vars, dependencies, terraform state, backend types, template variables, circular imports)
- Import path auto-completion
- Live diagnostics on file open/change/save
- Template expression resolution ({{ .vars.X }}, {{ .atmos_component }}, {{ .atmos_stack }})
- Bridge-only mode when atmos CLI is not available

### Bug Fixes
- Fix proxy hanging when downstream atmos LSP fails
- Fix server-to-client request handling from atmos LSP
- Fix diagnostic Range JSON shape for LSP compliance
- Fix dependency extraction for mapping sequences (component: api)

### Build
- Multi-platform Go binaries (linux-amd64, linux-arm64, darwin-amd64, darwin-arm64)
- WASM extension build (wasm32-wasip2)
- CI with Go tests and Rust checks
