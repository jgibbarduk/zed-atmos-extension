# Resolve Multi-Agent Code Review Issues

## Goal
Fix all Medium and Low priority issues identified in the multi-agent review before Zed extension publication.

## Issues

### Medium
1. **Path traversal in `checkMetadataComponentDir`** — `meta.Component` concatenated without sanitization
2. **LSP positions as byte offsets** — `nodeRange` uses bytes, LSP requires UTF-16 code units
3. **`publishDiagnostics` recover() anti-pattern** — Uses `defer recover()` instead of proper synchronization
4. **fsnotify misses new directories** — New subdirectories created after `StartWatching` are not watched

### Low
5. **proxy.Close leaks stdout pipe** — Never closes `p.stdout` `io.ReadCloser`
6. **Non-deterministic completion order** — `findTemplateCompletions` iterates map keys randomly
7. **Test goroutine leaks** — `mockHandler.Close()` doesn't close `notifCh`, forwarder blocks
8. **Silent WalkDir error swallowing** — `Reindex` and `StartWatching` ignore `filepath.WalkDir` errors
9. **Magic numbers** — `maxImportDepth=50`, `300*time.Millisecond` debounce, `200*time.Millisecond` watcher debounce

## Phase 1: Security & Safety Fixes
- Fix path traversal in `checkMetadataComponentDir`
- Fix `publishDiagnostics` recover anti-pattern
- Fix proxy.Close stdout pipe leak
- Fix WalkDir error swallowing

## Phase 2: LSP Correctness
- Fix LSP position computation (UTF-16 code units)
- Fix non-deterministic completion ordering

## Phase 3: Observability & Quality
- Extract named constants for magic numbers
- Fix fsnotify new directory watching
- Fix test goroutine leaks

## Phase 4: Verification
- Run `go test ./... -race`
- Verify coverage doesn't drop below 78%
- Commit all changes
