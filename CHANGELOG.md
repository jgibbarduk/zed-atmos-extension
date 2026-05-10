# Changelog

## [0.3.0](https://github.com/jgibbarduk/zed-atmos-extension/compare/v0.2.1...v0.3.0) (2026-05-10)


### Features

* **handler:** auto-trigger next path completion level on directory accept ([c6bccf7](https://github.com/jgibbarduk/zed-atmos-extension/commit/c6bccf7e0237cdf10ac4f92a88e151050da419a6))
* **handler:** auto-trigger next path level on directory accept ([7efbfde](https://github.com/jgibbarduk/zed-atmos-extension/commit/7efbfde5614d635eeea3968075d321bde0bb0565))
* **handler:** fluid nested path completions with isIncomplete ([630866a](https://github.com/jgibbarduk/zed-atmos-extension/commit/630866a04069c940fd79466a32af0044ec2b4b5c))


### Bug Fixes

* **handler:** preserve index data when didChange parses invalid YAML ([d4df652](https://github.com/jgibbarduk/zed-atmos-extension/commit/d4df652f762d34e37a69f493cb65ba1225a0c20b))

## [0.2.1](https://github.com/jgibbarduk/zed-atmos-extension/compare/v0.2.0...v0.2.1) (2026-05-10)


### Bug Fixes

* **diagnostics:** skip unquoted version check for booleans and strings ([e760581](https://github.com/jgibbarduk/zed-atmos-extension/commit/e7605818f54e351d44daf78f330855dd11893b85))
* **diagnostics:** skip unquoted version check for booleans and strings ([c0595a7](https://github.com/jgibbarduk/zed-atmos-extension/commit/c0595a7270261411ede1dde9c28266d8942dbd8d))

## [0.2.0](https://github.com/jgibbarduk/zed-atmos-extension/compare/v0.1.0...v0.2.0) (2026-05-10)


### Features

* **handler:** autocomplete dependency component names ([5d0b2a0](https://github.com/jgibbarduk/zed-atmos-extension/commit/5d0b2a0d08120b7032a797c0dbdfdd2c1ce2d636))
* **handler:** structured markdown hover with headers, code blocks, and horizontal rules ([ea43c06](https://github.com/jgibbarduk/zed-atmos-extension/commit/ea43c06c1dcb2290f5b31935dc3ee985f52772b9))
* implement 10 Atmos LSP improvements + review fixes ([94f59c7](https://github.com/jgibbarduk/zed-atmos-extension/commit/94f59c7ff82d03bbf37799f062601f9a2743881c))


### Bug Fixes

* address final code review blockers before publication ([b1121ec](https://github.com/jgibbarduk/zed-atmos-extension/commit/b1121ecd48ad5bb6a74daf3764aa6e75ed05f050))
* **diagnostics:** skip all metadata.component checks ([60e2de1](https://github.com/jgibbarduk/zed-atmos-extension/commit/60e2de1ad17ceb89bdd6d412167c43373e515fe9))
* **diagnostics:** skip metadata.component check for abstract components ([2cb7adc](https://github.com/jgibbarduk/zed-atmos-extension/commit/2cb7adcdb37cc988a68c6695f202ecab6250891f))
* **handler:** add bullet markers inside yaml code blocks for vars sections ([c1a566e](https://github.com/jgibbarduk/zed-atmos-extension/commit/c1a566e1d8f43830a471bf319be9a4943496a475))
* **handler:** add bullets back to kv, use yaml code blocks for vars sections ([ae0a374](https://github.com/jgibbarduk/zed-atmos-extension/commit/ae0a374f51e2325d0b2a414b0931116c2b4672c9))
* **handler:** use bold keys + plain values in hover, remove inline code background ([aa34f15](https://github.com/jgibbarduk/zed-atmos-extension/commit/aa34f15edaef3e256e3139c7fdb98a301ec3205f))
* resolve all medium/low code review issues before publish ([c568599](https://github.com/jgibbarduk/zed-atmos-extension/commit/c568599256de0e85f5e8b2b9f4723ab1fa6b7b98))

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
