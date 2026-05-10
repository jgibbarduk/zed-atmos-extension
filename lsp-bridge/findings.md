# Findings: Multi-Agent Code Review

## Architect Review
- Handler monolith at ~1,900 lines — should split into focused files before adding more features
- Index locking: `Reindex()` parses outside lock (good), but `UpsertFile/ReindexFile/RemoveFile` parse inside lock
- `collectVars()` does repeated recursive import traversal — should cache per-file
- `GetFileUnsafe()` breaks encapsulation — replace with read-only interface
- Proxy goroutine architecture is sound with `stdoutMu` and proper request serialization

## Code Review

### Critical: None

### Medium
1. **Path traversal** — `checkMetadataComponentDir` joins `meta.Component` unchecked
2. **UTF-16 positions** — `nodeRange` computes byte-based offsets, LSP spec requires UTF-16 code units
3. **recover() anti-pattern** — `publishDiagnostics` uses `defer recover()` around channel sends
4. **fsnotify gap** — New directories created after `StartWatching` are not tracked

### Low
5. **stdout pipe leak** — `proxy.Close` closes stdin + kills process but not stdout reader
6. **Map iteration order** — `findTemplateCompletions` ranges over `vars` map → non-deterministic UX
7. **Goroutine leaks** — `mockHandler` doesn't close `notifCh`, forwarder goroutine blocks
8. **Silent errors** — `filepath.WalkDir` errors in `Reindex` and `StartWatching` are ignored
9. **Magic numbers** — 50 (depth), 300ms (diag debounce), 200ms (watcher debounce) — no named constants

## Current Coverage: 78.4%
- handler: 82.1%
- index: 74.6%
- lsp: 95.3%
- proxy: 63.8%
