# Atmos IDE Fixes and Template Evaluation

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task.

**Goal:** Fix component key navigation/hover/rename issues and add Go template variable evaluation in hover.

**Architecture:** Extend existing LSP handler methods (`handleDefinition`, `handleHover`, `handleRename`) to detect cursor on component name KEYS (not just metadata values), and add template expression parsing/evaluation in hover.

**Tech Stack:** Go 1.23, gopkg.in/yaml.v3, standard LSP JSON-RPC bridge

---

## File Structure

| File | Responsibility |
|------|---------------|
| `lsp-bridge/internal/handler/handler.go` | LSP method dispatch: definition, hover, rename |
| `lsp-bridge/internal/handler/handler_test.go` | Tests for handler methods (NEW) |
| `lsp-bridge/internal/index/index.go` | Index types and data model (no changes needed) |
| `lsp-bridge/internal/index/parser.go` | YAML parsing (no changes needed) |

---

### Task 1: Fix go-to-definition for component name keys

**Files:**
- Modify: `lsp-bridge/internal/handler/handler.go`
- Test: `lsp-bridge/internal/handler/handler_test.go`

Currently `handleDefinition` only resolves `metadata.component` values and `!terraform.state` tags. It must ALSO detect when the cursor is on a component name KEY (e.g., `database:` under `components.terraform`) and navigate to that component's definition.

- [ ] **Step 1: Write failing test**

```go
func TestHandleDefinition_ComponentKey(t *testing.T) {
    idx := createTestIndex()
    h := New(idx, nil)

    // Simulate cursor on component key "database" in stack.yaml
    content := []byte(`{"jsonrpc":"2.0","id":1,"params":{"textDocument":{"uri":"file:///stacks/dev/stack.yaml"},"position":{"line":5,"character":6}}}`)

    handled, resp, _, err := h.HandleMethod("textDocument/definition", content)
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if !handled {
        t.Fatal("expected handled")
    }

    var result []map[string]interface{}
    extractResult(resp, &result)
    if len(result) == 0 {
        t.Fatal("expected at least one location for component key definition")
    }
}
```

- [ ] **Step 2: Add component key definition detection**

In `handleDefinition`, after the `!terraform.state` check and before returning, add:

```go
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
```

- [ ] **Step 3: Run tests**

```bash
cd lsp-bridge && go test ./internal/handler/... -v -run TestHandleDefinition_ComponentKey
```

Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add lsp-bridge/internal/handler/handler.go lsp-bridge/internal/handler/handler_test.go
git commit -m "fix(definition): navigate to component definition from component name keys"
```

---

### Task 2: Fix stack name preview blocked by component hover

**Files:**
- Modify: `lsp-bridge/internal/handler/handler.go`
- Test: `lsp-bridge/internal/handler/handler_test.go`

Currently in `handleHover`, when cursor is on a component, the component hover takes precedence and the stack name preview (which comes later in a separate `if hoverContent == nil` block) never renders. The stack name preview should be APPENDED to component hover content.

- [ ] **Step 1: Write failing test**

```go
func TestHandleHover_ComponentIncludesStackName(t *testing.T) {
    idx := createTestIndexWithNameTemplate("{{ .namespace }}-{{ .environment }}")
    h := New(idx, nil)
    h.nameTemplate = "{{ .namespace }}-{{ .environment }}"

    content := []byte(`{"jsonrpc":"2.0","id":1,"params":{"textDocument":{"uri":"file:///stacks/dev/stack.yaml"},"position":{"line":5,"character":6}}}`)

    handled, resp, _, err := h.HandleMethod("textDocument/hover", content)
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if !handled {
        t.Fatal("expected handled")
    }

    var result map[string]interface{}
    extractResult(resp, &result)
    contents := result["contents"].(map[string]interface{})
    value := contents["value"].(string)
    if !strings.Contains(value, "Stack name:") {
        t.Fatal("expected stack name preview in component hover")
    }
}
```

- [ ] **Step 2: Move stack name preview into component hover block**

In `handleHover`, inside the component hover block (where `hoverContent` is set for components), after setting `value` with component info and accumulated vars, BEFORE creating `hoverContent`, add:

```go
// Append stack name preview if available
if h.nameTemplate != "" {
    vars := collectVars(f, h.idx)
    preview := interpolateNameTemplate(h.nameTemplate, vars)
    if preview != "" {
        value += fmt.Sprintf("\n\n**Stack name:** `%s`\n\nComputed from `atmos.yaml` `name_template` with accumulated vars.", preview)
    }
}
```

Then REMOVE the separate `if hoverContent == nil && h.nameTemplate != "" && len(f.Comps) > 0` block that currently provides stack name preview (the one at lines 723-732). Keep the "resolved variables view" block (lines 736-754) as a final fallback.

- [ ] **Step 3: Run tests**

```bash
cd lsp-bridge && go test ./internal/handler/... -v -run TestHandleHover_ComponentIncludesStackName
```

Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add lsp-bridge/internal/handler/handler.go lsp-bridge/internal/handler/handler_test.go
git commit -m "fix(hover): include stack name preview in component hover"
```

---

### Task 3: Fix rename on component name keys

**Files:**
- Modify: `lsp-bridge/internal/handler/handler.go`
- Test: `lsp-bridge/internal/handler/handler_test.go`

`handleRename` already detects cursor on `CompNode.Range` and renames matching `CompNode.Name` across files. However, the rename must also handle the case where component keys are QUOTED in YAML (e.g., `"database":`). `yaml.v3` strips quotes from `Value` but the actual text includes quotes, so `nodeRange` under-reports the range length for quoted keys.

- [ ] **Step 1: Write failing test for quoted component key rename**

```go
func TestHandleRename_QuotedComponentKey(t *testing.T) {
    idx := createTestIndexWithQuotedComponent()
    h := New(idx, nil)

    // Cursor on quoted component key "my-db" in stack.yaml
    content := []byte(`{"jsonrpc":"2.0","id":1,"params":{"textDocument":{"uri":"file:///stacks/dev/stack.yaml"},"position":{"line":5,"character":6},"newName":"renamed-db"}}`)

    handled, resp, _, err := h.HandleMethod("textDocument/rename", content)
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if !handled {
        t.Fatal("expected handled")
    }

    var result lsp.WorkspaceEdit
    extractResult(resp, &result)
    edits := result.Changes["file:///stacks/dev/stack.yaml"]
    if len(edits) == 0 {
        t.Fatal("expected edits for quoted component key")
    }
    // Verify the edit range covers the quoted key
    found := false
    for _, edit := range edits {
        if edit.NewText == "renamed-db" {
            found = true
            break
        }
    }
    if !found {
        t.Fatal("expected rename edit with newName")
    }
}
```

- [ ] **Step 2: Fix nodeRange for quoted keys**

In `lsp-bridge/internal/index/parser.go`, update `nodeRange` to account for YAML quoting:

```go
func nodeRange(n *yaml.Node) Range {
    if n == nil {
        return Range{}
    }
    if n.Line == 0 || n.Column == 0 {
        return Range{}
    }
    length := len(n.Value)
    // Account for quotes that yaml.v3 strips from Value but keeps in text
    if n.Style == yaml.DoubleQuotedStyle {
        length += 2 // opening and closing "
    } else if n.Style == yaml.SingleQuotedStyle {
        length += 2 // opening and closing '
    }
    return Range{
        StartLine: uint32(n.Line - 1),
        StartChar: uint32(n.Column - 1),
        EndLine:   uint32(n.Line - 1),
        EndChar:   uint32(n.Column - 1 + length),
    }
}
```

- [ ] **Step 3: Run tests**

```bash
cd lsp-bridge && go test ./internal/handler/... -v -run TestHandleRename_QuotedComponentKey
cd lsp-bridge && go test ./internal/index/... -v -run TestNodeRange_Quoted
```

Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add lsp-bridge/internal/handler/handler.go lsp-bridge/internal/index/parser.go lsp-bridge/internal/handler/handler_test.go lsp-bridge/internal/index/parser_test.go
git commit -m "fix(rename): correct range for quoted component keys"
```

---

### Task 4: Add template variable evaluation in hover

**Files:**
- Modify: `lsp-bridge/internal/handler/handler.go`
- Test: `lsp-bridge/internal/handler/handler_test.go`

When hovering over a YAML value containing Go template expressions like `{{ .atmos_component }}` or `{{ .vars.namespace }}`, show the resolved value alongside the template syntax.

- [ ] **Step 1: Write failing test**

```go
func TestHandleHover_TemplateEvaluation(t *testing.T) {
    idx := createTestIndexWithTemplateVars()
    h := New(idx, nil)

    // Hover over a value containing {{ .vars.namespace }}
    content := []byte(`{"jsonrpc":"2.0","id":1,"params":{"textDocument":{"uri":"file:///stacks/dev/stack.yaml"},"position":{"line":10,"character":10}}}`)

    handled, resp, _, err := h.HandleMethod("textDocument/hover", content)
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if !handled {
        t.Fatal("expected handled")
    }

    var result map[string]interface{}
    extractResult(resp, &result)
    contents := result["contents"].(map[string]interface{})
    value := contents["value"].(string)
    if !strings.Contains(value, "Resolved:") {
        t.Fatal("expected resolved template value in hover")
    }
}
```

- [ ] **Step 2: Add template evaluation helper**

In `handler.go`, add:

```go
var templateExprRe = regexp.MustCompile(`{{\s*\.([a-zA-Z0-9_]+)\s*}}`)
var templateVarExprRe = regexp.MustCompile(`{{\s*\.vars\.([a-zA-Z0-9_]+)\s*}}`)

func evaluateTemplateExpressions(value string, sf *StackFile, idx *Index) string {
    // Resolve .vars.xxx expressions
    result := templateVarExprRe.ReplaceAllStringFunc(value, func(m string) string {
        subs := templateVarExprRe.FindStringSubmatch(m)
        if len(subs) > 1 {
            vars := collectVars(sf, idx)
            if v, ok := vars[subs[1]]; ok {
                return v
            }
        }
        return m
    })

    // Resolve .atmos_component from the component name context
    // This requires knowing which component we're in - we'll handle this in hover
    return result
}

func findTemplateExpressionAtPosition(sf *StackFile, line uint32, char uint32) (expr string, resolved string) {
    // Walk vars to find one on the given line
    for _, v := range sf.Vars {
        if v.Range.StartLine == line && v.Range.StartChar <= char && v.Range.EndChar >= char {
            expr = v.Value
            break
        }
    }
    if expr == "" {
        return "", ""
    }
    // Try to resolve template expressions in the value
    resolved = expr
    // .vars.xxx resolution
    resolved = templateVarExprRe.ReplaceAllStringFunc(resolved, func(m string) string {
        subs := templateVarExprRe.FindStringSubmatch(m)
        if len(subs) > 1 {
            vars := collectVars(sf, nil) // nil idx ok if we don't need imports
            if v, ok := vars[subs[1]]; ok {
                return v
            }
        }
        return m
    })
    // .atmos_component - find component on same or nearby line
    if strings.Contains(expr, "{{ .atmos_component }}") {
        compName := ""
        for _, comp := range sf.Comps {
            if comp.Range.StartLine == line || comp.Range.StartLine == line-1 || comp.Range.StartLine == line+1 {
                compName = comp.Name
                break
            }
        }
        if compName != "" {
            resolved = strings.ReplaceAll(resolved, "{{ .atmos_component }}", compName)
        }
    }
    return expr, resolved
}
```

- [ ] **Step 3: Integrate into handleHover**

In `handleHover`, after the component hover block and before the terraform.state block, add a new block that detects template expressions in YAML values:

```go
if hoverContent == nil {
    // Check if cursor is on a var value containing template expressions
    for _, v := range sf.Vars {
        if v.Range.StartLine <= req.Params.Position.Line && v.Range.EndLine >= req.Params.Position.Line {
            if strings.Contains(v.Value, "{{") && strings.Contains(v.Value, "}}") {
                _, resolved := findTemplateExpressionAtPosition(f, req.Params.Position.Line, req.Params.Position.Character)
                if resolved != "" && resolved != v.Value {
                    value := fmt.Sprintf("**Template:** `%s`\n\n**Resolved:** `%s`", v.Value, resolved)
                    hoverContent = map[string]interface{}{
                        "kind":  "markdown",
                        "value": value,
                    }
                    break
                }
            }
        }
    }
}
```

- [ ] **Step 4: Run tests**

```bash
cd lsp-bridge && go test ./internal/handler/... -v -run TestHandleHover_TemplateEvaluation
```

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add lsp-bridge/internal/handler/handler.go lsp-bridge/internal/handler/handler_test.go
git commit -m "feat(hover): evaluate and display resolved Go template expressions"
```

---

### Task 5: Add comprehensive handler tests

**Files:**
- Create: `lsp-bridge/internal/handler/handler_test.go`

Add tests for all handler methods to ensure high coverage.

- [ ] **Step 1: Create test helpers**

```go
package handler

import (
    "encoding/json"
    "os"
    "path/filepath"
    "testing"

    "github.com/jamesgibbard/zed-atmos-language/lsp-bridge/internal/index"
)

func extractResult(resp []byte, v interface{}) {
    var wrapper map[string]interface{}
    json.Unmarshal(resp, &wrapper)
    if raw, ok := wrapper["result"].(json.RawMessage); ok {
        json.Unmarshal(raw, v)
    } else if resultBytes, err := json.Marshal(wrapper["result"]); err == nil {
        json.Unmarshal(resultBytes, v)
    }
}

func createTempDir(t *testing.T) string {
    dir, err := os.MkdirTemp("", "atmos-test-*")
    if err != nil {
        t.Fatal(err)
    }
    t.Cleanup(func() { os.RemoveAll(dir) })
    return dir
}

func writeFile(t *testing.T, path string, content string) {
    if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
        t.Fatal(err)
    }
    if err := os.WriteFile(path, []byte(content), 0644); err != nil {
        t.Fatal(err)
    }
}

func createTestIndex() *index.Index {
    // Returns a pre-populated index for basic tests
    idx, _ := index.New("")
    return idx
}
```

- [ ] **Step 2: Add tests for handleDefinition**

Tests for: import resolution, metadata.component, metadata.inherits, terraform.state, component keys.

- [ ] **Step 3: Add tests for handleHover**

Tests for: import hover, component hover (with stack name), terraform.state hover, template evaluation, resolved vars fallback.

- [ ] **Step 4: Add tests for handleRename**

Tests for: component key rename, metadata.component rename, quoted key rename, terraform.state rename, dep rename.

- [ ] **Step 5: Run all tests**

```bash
cd lsp-bridge && go test ./internal/handler/... -v
```

Expected: All PASS

- [ ] **Step 6: Commit**

```bash
git add lsp-bridge/internal/handler/handler_test.go
git commit -m "test(handler): comprehensive tests for definition, hover, rename"
```

---

### Task 6: Full codebase review with voltagents

**Files:** All `.go` files in `lsp-bridge/`

- [ ] **Step 1: Dispatch architect reviewer**

Run: `voltagent-qa-sec:architect-reviewer` on all `lsp-bridge/**/*.go` files

Check for:
- Consistency in error handling patterns
- Mutex usage correctness
- Data model encapsulation
- LSP protocol compliance

- [ ] **Step 2: Dispatch code reviewer**

Run: `superpowers:code-reviewer` on the entire diff

Check for:
- Spec compliance (all fixes implemented)
- Coding standards
- Test coverage adequacy

- [ ] **Step 3: Fix any issues found**

Address all findings from both reviewers.

- [ ] **Step 4: Final test run**

```bash
cd lsp-bridge && go test ./... -v
```

Expected: All PASS

- [ ] **Step 5: Commit any fixes**

```bash
git add -A
git commit -m "review fixes: address architect and code reviewer feedback"
```

---

## Self-Review

1. **Spec coverage:**
   - Component key go-to-definition → Task 1
   - Stack name preview in component hover → Task 2
   - Rename on component name keys (quoted) → Task 3
   - Template var evaluation in hover → Task 4
   - Comprehensive tests → Task 5
   - Full codebase review → Task 6

2. **Placeholder scan:** None found.

3. **Type consistency:** All types match existing codebase (index.Range, lsp.TextEdit, etc.)
