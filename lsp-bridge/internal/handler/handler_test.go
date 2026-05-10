package handler

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jgibbarduk/zed-atmos-extension/lsp-bridge/internal/index"
	"github.com/jgibbarduk/zed-atmos-extension/lsp-bridge/internal/lsp"
)

// mockDownstream implements downstreamCaller for tests.
type mockDownstream struct{}

func (m *mockDownstream) CallDownstream(content []byte) ([]byte, [][]byte, error) { return nil, nil, nil }
func (m *mockDownstream) SendNotification(content []byte) error         { return nil }

// mockDownstreamWithResponse returns a predefined JSON-RPC response.
type mockDownstreamWithResponse struct {
	mockDownstream
	response []byte
}

func (m *mockDownstreamWithResponse) CallDownstream(content []byte) ([]byte, [][]byte, error) {
	return m.response, nil, nil
}

func extractResult(resp []byte, v interface{}) {
	var wrapper map[string]json.RawMessage
	json.Unmarshal(resp, &wrapper)
	if raw, ok := wrapper["result"]; ok {
		json.Unmarshal(raw, v)
	}
}

func mustMarshal(t *testing.T, v interface{}) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func createTestIndex(t *testing.T, files map[string]string) *index.Index {
	t.Helper()
	dir := t.TempDir()
	for relPath, content := range files {
		fullPath := filepath.Join(dir, relPath)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	idx, err := index.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	idx.SetBasePath(dir)
	idx.Reindex()
	return idx
}

func TestHandleDefinition_Import(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "stacks/dev"), 0755)
	os.WriteFile(filepath.Join(dir, "stacks/dev/defaults.yaml"), []byte("vars:\n  namespace: dev\n"), 0644)
	os.WriteFile(filepath.Join(dir, "stacks/dev/stack.yaml"), []byte("import:\n  - defaults\n"), 0644)
	idx2, err := index.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	idx2.SetBasePath(dir)
	idx2.Reindex()
	h2 := New(idx2, &mockDownstream{})
	stackPath := filepath.Join(dir, "stacks/dev/stack.yaml")
	content := mustMarshal(t, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"params": map[string]interface{}{
			"textDocument": map[string]string{"uri": "file://" + stackPath},
			"position":     map[string]uint32{"line": 1, "character": 4},
		},
	})

	handled, resp, _, err := h2.HandleMethod("textDocument/definition", content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !handled {
		t.Fatal("expected handled")
	}
	var locations []map[string]interface{}
	extractResult(resp, &locations)
	if len(locations) == 0 {
		t.Fatal("expected at least one location for import definition")
	}
}

func TestHandleDefinition_ComponentKey(t *testing.T) {
	dir := t.TempDir()
	compPath := filepath.Join(dir, "components/terraform/database.yaml")
	os.MkdirAll(filepath.Dir(compPath), 0755)
	os.WriteFile(compPath, []byte("vars:\n  engine: postgres\n"), 0644)

	stackPath := filepath.Join(dir, "stacks/dev/stack.yaml")
	os.MkdirAll(filepath.Dir(stackPath), 0755)
	os.WriteFile(stackPath, []byte("components:\n  terraform:\n    database:\n      vars:\n        size: large\n"), 0644)

	idx, err := index.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	idx.SetBasePath(dir)
	idx.Reindex()
	h := New(idx, &mockDownstream{})

	content := mustMarshal(t, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"params": map[string]interface{}{
			"textDocument": map[string]string{"uri": "file://" + stackPath},
			"position":     map[string]uint32{"line": 2, "character": 4},
		},
	})

	handled, resp, _, err := h.HandleMethod("textDocument/definition", content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !handled {
		t.Fatal("expected handled")
	}
	var locations []map[string]interface{}
	extractResult(resp, &locations)
	if len(locations) == 0 {
		t.Fatal("expected at least one location for component key definition")
	}
}

func TestHandleHover_ComponentWithStackName(t *testing.T) {
	dir := t.TempDir()
	stackPath := filepath.Join(dir, "stacks/dev/stack.yaml")
	os.MkdirAll(filepath.Dir(stackPath), 0755)
	os.WriteFile(stackPath, []byte("vars:\n  namespace: dev\n  environment: staging\ncomponents:\n  terraform:\n    database:\n      vars:\n        size: large\n"), 0644)

	idx, err := index.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	idx.SetBasePath(dir)
	idx.Reindex()
	h := New(idx, &mockDownstream{})
	h.nameTemplate = "{{ .namespace }}-{{ .environment }}"

	content := mustMarshal(t, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"params": map[string]interface{}{
			"textDocument": map[string]string{"uri": "file://" + stackPath},
			"position":     map[string]uint32{"line": 5, "character": 4},
		},
	})

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
	if !strings.Contains(value, "## Component") {
		t.Fatalf("expected '## Component' in hover, got: %s", value)
	}
	if !strings.Contains(value, "## Stack name preview") {
		t.Fatalf("expected '## Stack name preview' in hover, got: %s", value)
	}
	if !strings.Contains(value, "dev-staging") {
		t.Fatalf("expected resolved stack name 'dev-staging' in hover, got: %s", value)
	}
}

func TestHandleHover_TemplateExpression(t *testing.T) {
	dir := t.TempDir()
	stackPath := filepath.Join(dir, "stacks/dev/stack.yaml")
	os.MkdirAll(filepath.Dir(stackPath), 0755)
	os.WriteFile(stackPath, []byte("vars:\n  namespace: dev\ncomponents:\n  terraform:\n    database:\n      vars:\n        db_name: '{{ .vars.namespace }}_db'\n"), 0644)

	idx, err := index.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	idx.SetBasePath(dir)
	idx.Reindex()
	h := New(idx, &mockDownstream{})

	// Hover over the db_name value line (line 6 in 0-indexed), cursor on the value
	content := mustMarshal(t, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"params": map[string]interface{}{
			"textDocument": map[string]string{"uri": "file://" + stackPath},
			"position":     map[string]uint32{"line": 6, "character": 20},
		},
	})

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
	if !strings.Contains(value, "## Template expression") {
		t.Fatalf("expected '## Template expression' in hover, got: %s", value)
	}
	if !strings.Contains(value, "## Resolved value") {
		t.Fatalf("expected '## Resolved value' in hover, got: %s", value)
	}
	if !strings.Contains(value, "dev_db") {
		t.Fatalf("expected resolved value 'dev_db' in hover, got: %s", value)
	}
}

func TestHandleHover_TemplateExpression_AtmosComponent(t *testing.T) {
	dir := t.TempDir()
	stackPath := filepath.Join(dir, "stacks/dev/stack.yaml")
	os.MkdirAll(filepath.Dir(stackPath), 0755)
	os.WriteFile(stackPath, []byte("components:\n  terraform:\n    database:\n      vars:\n        name: '{{ .atmos_component }}'\n"), 0644)

	idx, err := index.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	idx.SetBasePath(dir)
	idx.Reindex()
	h := New(idx, &mockDownstream{})

	// Hover over the name value line (line 4 in 0-indexed), cursor on the value
	content := mustMarshal(t, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"params": map[string]interface{}{
			"textDocument": map[string]string{"uri": "file://" + stackPath},
			"position":     map[string]uint32{"line": 4, "character": 20},
		},
	})

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
	if !strings.Contains(value, "## Template expression") {
		t.Fatalf("expected '## Template expression' in hover, got: %s", value)
	}
	if !strings.Contains(value, "## Resolved value") {
		t.Fatalf("expected '## Resolved value' in hover, got: %s", value)
	}
	if !strings.Contains(value, "database") {
		t.Fatalf("expected resolved value 'database' in hover, got: %s", value)
	}
}

func TestHandleHover_ComponentWithAtmosComponentStackName(t *testing.T) {
	dir := t.TempDir()
	stackPath := filepath.Join(dir, "stacks/dev/stack.yaml")
	os.MkdirAll(filepath.Dir(stackPath), 0755)
	os.WriteFile(stackPath, []byte("vars:\n  namespace: dev\ncomponents:\n  terraform:\n    database:\n      vars:\n        size: large\n"), 0644)

	idx, err := index.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	idx.SetBasePath(dir)
	idx.Reindex()
	h := New(idx, &mockDownstream{})
	h.nameTemplate = "{{ .namespace }}-{{ .atmos_component }}"

	content := mustMarshal(t, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"params": map[string]interface{}{
			"textDocument": map[string]string{"uri": "file://" + stackPath},
			"position":     map[string]uint32{"line": 4, "character": 4},
		},
	})

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
	if !strings.Contains(value, "## Stack name preview") {
		t.Fatalf("expected '## Stack name preview' in hover, got: %s", value)
	}
	if !strings.Contains(value, "dev-database") {
		t.Fatalf("expected resolved stack name 'dev-database' in hover, got: %s", value)
	}
}

func TestHandleHover_TemplateExpression_DirectKey(t *testing.T) {
	dir := t.TempDir()
	stackPath := filepath.Join(dir, "stacks/dev/stack.yaml")
	os.MkdirAll(filepath.Dir(stackPath), 0755)
	os.WriteFile(stackPath, []byte("vars:\n  namespace: dev\ncomponents:\n  terraform:\n    database:\n      vars:\n        env: '{{ .namespace }}'\n"), 0644)

	idx, err := index.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	idx.SetBasePath(dir)
	idx.Reindex()
	h := New(idx, &mockDownstream{})

	// Hover over the env value line (line 6 in 0-indexed)
	content := mustMarshal(t, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"params": map[string]interface{}{
			"textDocument": map[string]string{"uri": "file://" + stackPath},
			"position":     map[string]uint32{"line": 6, "character": 14},
		},
	})

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
	if !strings.Contains(value, "## Template expression") {
		t.Fatalf("expected '## Template expression' in hover, got: %s", value)
	}
	if !strings.Contains(value, "## Resolved value") {
		t.Fatalf("expected '## Resolved value' in hover, got: %s", value)
	}
	if !strings.Contains(value, "dev") {
		t.Fatalf("expected resolved value 'dev' in hover, got: %s", value)
	}
}

func TestHandleHover_TemplateExpression_NonVarsBlock(t *testing.T) {
	dir := t.TempDir()
	stackPath := filepath.Join(dir, "stacks/mixins/atmos-pro/default.yaml")
	os.MkdirAll(filepath.Dir(stackPath), 0755)
	os.WriteFile(stackPath, []byte("drift-detection-wf-config: &drift-detection-wf-config\n  atmos-terraform-plan.yaml:\n    inputs:\n      component: \"{{ .atmos_component }}\"\n      stack: \"{{ .atmos_stack }}\"\n"), 0644)

	idx, err := index.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	idx.SetBasePath(dir)
	idx.Reindex()
	h := New(idx, &mockDownstream{})

	// Hover over line 3 (0-indexed) — component: "{{ .atmos_component }}"
	content := mustMarshal(t, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"params": map[string]interface{}{
			"textDocument": map[string]string{"uri": "file://" + stackPath},
			"position":     map[string]uint32{"line": 3, "character": 20},
		},
	})

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
	if !strings.Contains(value, "## Template expression") {
		t.Fatalf("expected '## Template expression' in hover for non-vars block expression, got: %s", value)
	}
	if !strings.Contains(value, "{{ .atmos_component }}") {
		t.Fatalf("expected template expression in hover, got: %s", value)
	}
}

func TestHandleHover_TemplateExpression_MultiExpressionLine(t *testing.T) {
	// Place the expression outside vars: so the fallback path is exercised.
	dir := t.TempDir()
	stackPath := filepath.Join(dir, "stacks/dev/stack.yaml")
	os.MkdirAll(filepath.Dir(stackPath), 0755)
	os.WriteFile(stackPath, []byte("vars:\n  tenant: plat\n  stage: dev\nsettings:\n  env:\n    name: \"{{ .vars.tenant }}-{{ .vars.stage }}\"\n"), 0644)

	idx, err := index.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	idx.SetBasePath(dir)
	idx.Reindex()
	h := New(idx, &mockDownstream{})

	// Hover over the dash between template expressions on line 5 (0-indexed)
	content := mustMarshal(t, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"params": map[string]interface{}{
			"textDocument": map[string]string{"uri": "file://" + stackPath},
			"position":     map[string]uint32{"line": 5, "character": 29},
		},
	})

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
	if !strings.Contains(value, "## Template expression") {
		t.Fatalf("expected '## Template expression' in hover, got: %s", value)
	}
	if !strings.Contains(value, "## Resolved value") {
		t.Fatalf("expected '## Resolved value' in hover, got: %s", value)
	}
	if !strings.Contains(value, "plat-dev") {
		t.Fatalf("expected resolved value 'plat-dev' (with dash preserved), got: %s", value)
	}
	// Ensure we didn't lose the dash by concatenating matches
	if strings.Contains(value, "platdev") {
		t.Fatalf("resolved value incorrectly dropped dash, got: %s", value)
	}
}

func TestHandleHover_TemplateExpression_AtmosStack(t *testing.T) {
	dir := t.TempDir()
	stackPath := filepath.Join(dir, "stacks/orgs/ex1/plat/dev/us-east-2.yaml")
	os.MkdirAll(filepath.Dir(stackPath), 0755)
	os.WriteFile(stackPath, []byte("vars:\n  namespace: ex1\ncomponents:\n  terraform:\n    database:\n      vars:\n        stack_ref: '{{ .atmos_stack }}'\n"), 0644)

	idx, err := index.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	// BasePath should be the stacks directory, matching real Atmos projects.
	idx.SetBasePath(filepath.Join(dir, "stacks"))
	idx.Reindex()
	h := New(idx, &mockDownstream{})
	h.nameTemplate = "{{ .namespace }}-{{ .atmos_stack }}"

	// Hover over the stack_ref value line (line 6 in 0-indexed)
	content := mustMarshal(t, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"params": map[string]interface{}{
			"textDocument": map[string]string{"uri": "file://" + stackPath},
			"position":     map[string]uint32{"line": 6, "character": 20},
		},
	})

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
	if !strings.Contains(value, "## Template expression") {
		t.Fatalf("expected '## Template expression' in hover, got: %s", value)
	}
	if !strings.Contains(value, "## Resolved value") {
		t.Fatalf("expected '## Resolved value' in hover, got: %s", value)
	}
	expectedStack := "ex1-orgs/ex1/plat/dev/us-east-2"
	if !strings.Contains(value, expectedStack) {
		t.Fatalf("expected resolved stack name '%s' in hover, got: %s", expectedStack, value)
	}
}

func TestHandleRename_ComponentKey(t *testing.T) {
	dir := t.TempDir()
	stackPath := filepath.Join(dir, "stacks/dev/stack.yaml")
	os.MkdirAll(filepath.Dir(stackPath), 0755)
	os.WriteFile(stackPath, []byte("components:\n  terraform:\n    database:\n      vars:\n        size: large\n"), 0644)

	idx, err := index.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	idx.SetBasePath(dir)
	idx.Reindex()
	h := New(idx, &mockDownstream{})

	content := mustMarshal(t, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"params": map[string]interface{}{
			"textDocument": map[string]string{"uri": "file://" + stackPath},
			"position":     map[string]uint32{"line": 2, "character": 4},
			"newName":      "renamed-db",
		},
	})

	handled, resp, _, err := h.HandleMethod("textDocument/rename", content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !handled {
		t.Fatal("expected handled")
	}
	var edit lsp.WorkspaceEdit
	extractResult(resp, &edit)
	edits := edit.Changes["file://"+stackPath]
	if len(edits) == 0 {
		t.Fatal("expected edits for component key rename")
	}
	found := false
	for _, e := range edits {
		if e.NewText == "renamed-db" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected rename edit with NewText='renamed-db', got %+v", edits)
	}
}

func TestHandleRename_MetadataComponent(t *testing.T) {
	dir := t.TempDir()
	stackPath := filepath.Join(dir, "stacks/dev/stack.yaml")
	os.MkdirAll(filepath.Dir(stackPath), 0755)
	os.WriteFile(stackPath, []byte("components:\n  terraform:\n    database:\n      metadata:\n        component: database\n"), 0644)

	idx, err := index.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	idx.SetBasePath(dir)
	idx.Reindex()
	h := New(idx, &mockDownstream{})

	// Cursor on metadata.component value "database" at line 4 (0-indexed)
	content := mustMarshal(t, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"params": map[string]interface{}{
			"textDocument": map[string]string{"uri": "file://" + stackPath},
			"position":     map[string]uint32{"line": 4, "character": 8},
			"newName":      "renamed-db",
		},
	})

	handled, resp, _, err := h.HandleMethod("textDocument/rename", content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !handled {
		t.Fatal("expected handled")
	}
	var edit lsp.WorkspaceEdit
	extractResult(resp, &edit)
	edits := edit.Changes["file://"+stackPath]
	if len(edits) == 0 {
		t.Fatal("expected edits for metadata component rename")
	}
	foundKey := false
	foundMeta := false
	for _, e := range edits {
		if e.NewText == "renamed-db" {
			if e.Range.Start.Line == 2 {
				foundKey = true
			}
			if e.Range.Start.Line == 4 {
				foundMeta = true
			}
		}
	}
	if !foundKey {
		t.Error("expected rename of component key at line 2")
	}
	if !foundMeta {
		t.Error("expected rename of metadata.component at line 4")
	}
}

func TestHandleRename_TerraformState(t *testing.T) {
	dir := t.TempDir()
	stackPath := filepath.Join(dir, "stacks/dev/stack.yaml")
	os.MkdirAll(filepath.Dir(stackPath), 0755)
	os.WriteFile(stackPath, []byte("components:\n  terraform:\n    database:\n      vars:\n        state: !terraform.state database .outputs.id\n"), 0644)

	idx, err := index.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	idx.SetBasePath(dir)
	idx.Reindex()
	h := New(idx, &mockDownstream{})

	// Cursor on terraform.state line (line 4 in 0-indexed)
	content := mustMarshal(t, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"params": map[string]interface{}{
			"textDocument": map[string]string{"uri": "file://" + stackPath},
			"position":     map[string]uint32{"line": 4, "character": 10},
			"newName":      "renamed-db",
		},
	})

	handled, resp, _, err := h.HandleMethod("textDocument/rename", content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !handled {
		t.Fatal("expected handled")
	}
	var edit lsp.WorkspaceEdit
	extractResult(resp, &edit)
	edits := edit.Changes["file://"+stackPath]
	if len(edits) == 0 {
		t.Fatal("expected edits for terraform.state rename")
	}
	found := false
	for _, e := range edits {
		if e.NewText == "renamed-db .outputs.id" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected rename edit with NewText='renamed-db .outputs.id', got %+v", edits)
	}
}

func TestHandleCodeAction_Scaffold(t *testing.T) {
	dir := t.TempDir()
	// Ensure no catalog file exists for "new-service"
	stackPath := filepath.Join(dir, "stacks/dev/stack.yaml")
	os.MkdirAll(filepath.Dir(stackPath), 0755)
	os.WriteFile(stackPath, []byte("components:\n  terraform:\n    new-service:\n      vars:\n        size: large\n"), 0644)

	idx, err := index.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	idx.SetBasePath(dir)
	idx.Reindex()
	h := New(idx, &mockDownstream{})

	content := mustMarshal(t, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"params": map[string]interface{}{
			"textDocument": map[string]string{"uri": "file://" + stackPath},
			"range": map[string]interface{}{
				"start": map[string]uint32{"line": 2, "character": 4},
				"end":   map[string]uint32{"line": 2, "character": 14},
			},
		},
	})

	handled, resp, _, err := h.HandleMethod("textDocument/codeAction", content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !handled {
		t.Fatal("expected handled")
	}
	var actions []map[string]interface{}
	extractResult(resp, &actions)
	if len(actions) == 0 {
		t.Fatal("expected at least one code action for missing catalog component")
	}
	found := false
	for _, action := range actions {
		if strings.Contains(action["title"].(string), "Generate component scaffold") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected scaffold code action, got %+v", actions)
	}
}

func TestDiagnostics_UnknownTemplateVariable(t *testing.T) {
	dir := t.TempDir()
	stackPath := filepath.Join(dir, "stacks/dev/stack.yaml")
	os.MkdirAll(filepath.Dir(stackPath), 0755)
	os.WriteFile(stackPath, []byte("vars:\n  namespace: dev\ncomponents:\n  terraform:\n    database:\n      vars:\n        test: \"{{ .namespac }}\"\n"), 0644)

	idx, err := index.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	idx.SetBasePath(dir)
	idx.Reindex()

	f := idx.GetFile(stackPath)
	if f == nil {
		t.Fatal("expected file in index")
	}

	diags := runBestPracticeChecks(f, filepath.Dir(stackPath), idx)
	if len(diags) == 0 {
		t.Fatal("expected diagnostics for unknown template variable")
	}
	found := false
	for _, d := range diags {
		if d.Severity == SeverityError && strings.Contains(d.Message, "namespac") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected error diagnostic for '.namespac', got: %+v", diags)
	}
}

func TestDiagnostics_KnownTemplateVariable(t *testing.T) {
	dir := t.TempDir()
	stackPath := filepath.Join(dir, "stacks/dev/stack.yaml")
	os.MkdirAll(filepath.Dir(stackPath), 0755)
	os.WriteFile(stackPath, []byte("vars:\n  namespace: dev\ncomponents:\n  terraform:\n    database:\n      vars:\n        test: \"{{ .namespace }}\"\n"), 0644)

	idx, err := index.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	idx.SetBasePath(dir)
	idx.Reindex()

	f := idx.GetFile(stackPath)
	if f == nil {
		t.Fatal("expected file in index")
	}

	diags := runBestPracticeChecks(f, filepath.Dir(stackPath), idx)
	for _, d := range diags {
		if strings.Contains(d.Message, "namespace") {
			t.Fatalf("unexpected diagnostic for known variable: %+v", d)
		}
	}
}

func TestDiagnostics_UnknownTemplateVariable_VarsPrefix(t *testing.T) {
	dir := t.TempDir()
	stackPath := filepath.Join(dir, "stacks/dev/stack.yaml")
	os.MkdirAll(filepath.Dir(stackPath), 0755)
	os.WriteFile(stackPath, []byte("vars:\n  namespace: dev\ncomponents:\n  terraform:\n    database:\n      vars:\n        test: \"{{ .vars.unknwn }}\"\n"), 0644)

	idx, err := index.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	idx.SetBasePath(dir)
	idx.Reindex()

	f := idx.GetFile(stackPath)
	if f == nil {
		t.Fatal("expected file in index")
	}

	diags := runBestPracticeChecks(f, filepath.Dir(stackPath), idx)
	found := false
	for _, d := range diags {
		if d.Severity == SeverityError && strings.Contains(d.Message, ".vars.unknwn") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected error diagnostic for '.vars.unknwn', got: %+v", diags)
	}
}

func TestDiagnostics_DuplicateImport(t *testing.T) {
	dir := t.TempDir()
	defaultsPath := filepath.Join(dir, "stacks/dev/defaults.yaml")
	os.MkdirAll(filepath.Dir(defaultsPath), 0755)
	os.WriteFile(defaultsPath, []byte("vars:\n  namespace: dev\n"), 0644)

	stackPath := filepath.Join(dir, "stacks/dev/stack.yaml")
	os.WriteFile(stackPath, []byte("import:\n  - defaults\n  - defaults\nvars:\n  x: 1\n"), 0644)

	idx, err := index.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	idx.SetBasePath(dir)
	idx.Reindex()

	f := idx.GetFile(stackPath)
	if f == nil {
		t.Fatal("expected file in index")
	}

	diags := runBestPracticeChecks(f, filepath.Dir(stackPath), idx)
	found := false
	for _, d := range diags {
		if d.Severity == SeverityWarning && strings.Contains(d.Message, "Duplicate import") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected duplicate import diagnostic, got: %+v", diags)
	}
}

func TestDiagnostics_CircularImport(t *testing.T) {
	dir := t.TempDir()
	aPath := filepath.Join(dir, "stacks/a.yaml")
	bPath := filepath.Join(dir, "stacks/b.yaml")
	os.MkdirAll(filepath.Dir(aPath), 0755)
	os.WriteFile(aPath, []byte("import:\n  - b\nvars:\n  x: 1\n"), 0644)
	os.WriteFile(bPath, []byte("import:\n  - a\nvars:\n  y: 2\n"), 0644)

	idx, err := index.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	idx.SetBasePath(dir)
	idx.Reindex()

	f := idx.GetFile(aPath)
	if f == nil {
		t.Fatal("expected file in index")
	}

	diags := runBestPracticeChecks(f, filepath.Dir(aPath), idx)
	found := false
	for _, d := range diags {
		if d.Severity == SeverityError && strings.Contains(d.Message, "Circular import") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected circular import diagnostic, got: %+v", diags)
	}
}

func TestDiagnostics_EmptyImport(t *testing.T) {
	dir := t.TempDir()
	stackPath := filepath.Join(dir, "stacks/dev/stack.yaml")
	os.MkdirAll(filepath.Dir(stackPath), 0755)
	os.WriteFile(stackPath, []byte("vars:\n  x: 1\n"), 0644)

	idx, err := index.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	idx.SetBasePath(dir)
	idx.Reindex()

	f := idx.GetFile(stackPath)
	if f == nil {
		t.Fatal("expected file in index")
	}

	diags := runBestPracticeChecks(f, filepath.Dir(stackPath), idx)
	found := false
	for _, d := range diags {
		if d.Severity == SeverityHint && strings.Contains(d.Message, "No imports defined") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected empty import hint, got: %+v", diags)
	}
}

func TestDiagnostics_InvalidComponentName(t *testing.T) {
	dir := t.TempDir()
	stackPath := filepath.Join(dir, "stacks/dev/stack.yaml")
	os.MkdirAll(filepath.Dir(stackPath), 0755)
	os.WriteFile(stackPath, []byte("components:\n  terraform:\n    'bad:name':\n      vars:\n        x: 1\n"), 0644)

	idx, err := index.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	idx.SetBasePath(dir)
	idx.Reindex()

	f := idx.GetFile(stackPath)
	if f == nil {
		t.Fatal("expected file in index")
	}

	diags := runBestPracticeChecks(f, filepath.Dir(stackPath), idx)
	found := false
	for _, d := range diags {
		if d.Severity == SeverityError && strings.Contains(d.Message, "invalid characters") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected invalid component name diagnostic, got: %+v", diags)
	}
}

func TestDiagnostics_InvalidMetadataType(t *testing.T) {
	dir := t.TempDir()
	stackPath := filepath.Join(dir, "stacks/dev/stack.yaml")
	os.MkdirAll(filepath.Dir(stackPath), 0755)
	os.WriteFile(stackPath, []byte("components:\n  terraform:\n    vpc:\n      metadata:\n        type: abtract\n      vars:\n        x: 1\n"), 0644)

	idx, err := index.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	idx.SetBasePath(dir)
	idx.Reindex()

	f := idx.GetFile(stackPath)
	if f == nil {
		t.Fatal("expected file in index")
	}

	diags := runBestPracticeChecks(f, filepath.Dir(stackPath), idx)
	found := false
	for _, d := range diags {
		if d.Severity == SeverityError && strings.Contains(d.Message, "metadata.type must be") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected invalid metadata.type diagnostic, got: %+v", diags)
	}
}

func TestDiagnostics_DuplicateComponentName(t *testing.T) {
	dir := t.TempDir()
	stackPath := filepath.Join(dir, "stacks/dev/stack.yaml")
	os.MkdirAll(filepath.Dir(stackPath), 0755)
	os.WriteFile(stackPath, []byte("components:\n  terraform:\n    vpc:\n      vars:\n        x: 1\n    vpc:\n      vars:\n        y: 2\n"), 0644)

	idx, err := index.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	idx.SetBasePath(dir)
	idx.Reindex()

	f := idx.GetFile(stackPath)
	if f == nil {
		t.Fatal("expected file in index")
	}

	diags := runBestPracticeChecks(f, filepath.Dir(stackPath), idx)
	found := false
	for _, d := range diags {
		if d.Severity == SeverityError && strings.Contains(d.Message, "Duplicate component name") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected duplicate component name diagnostic, got: %+v", diags)
	}
}

func TestDiagnostics_InvalidBackendType(t *testing.T) {
	dir := t.TempDir()
	stackPath := filepath.Join(dir, "stacks/dev/stack.yaml")
	os.MkdirAll(filepath.Dir(stackPath), 0755)
	os.WriteFile(stackPath, []byte("terraform:\n  backend_type: s4\n"), 0644)

	idx, err := index.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	idx.SetBasePath(dir)
	idx.Reindex()

	f := idx.GetFile(stackPath)
	if f == nil {
		t.Fatal("expected file in index")
	}

	diags := runBestPracticeChecks(f, filepath.Dir(stackPath), idx)
	found := false
	for _, d := range diags {
		if d.Severity == SeverityError && strings.Contains(d.Message, "Invalid backend_type") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected invalid backend_type diagnostic, got: %+v", diags)
	}
}

func TestDiagnostics_ValidBackendType(t *testing.T) {
	dir := t.TempDir()
	stackPath := filepath.Join(dir, "stacks/dev/stack.yaml")
	os.MkdirAll(filepath.Dir(stackPath), 0755)
	os.WriteFile(stackPath, []byte("terraform:\n  backend_type: s3\n"), 0644)

	idx, err := index.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	idx.SetBasePath(dir)
	idx.Reindex()

	f := idx.GetFile(stackPath)
	if f == nil {
		t.Fatal("expected file in index")
	}

	diags := runBestPracticeChecks(f, filepath.Dir(stackPath), idx)
	for _, d := range diags {
		if strings.Contains(d.Message, "backend_type") {
			t.Fatalf("unexpected diagnostic for valid backend_type: %+v", d)
		}
	}
}

func TestDiagnostics_SettingsDependsOn_MissingComponent(t *testing.T) {
	dir := t.TempDir()
	stackPath := filepath.Join(dir, "stacks/dev/stack.yaml")
	os.MkdirAll(filepath.Dir(stackPath), 0755)
	os.WriteFile(stackPath, []byte("components:\n  terraform:\n    vpc:\n      settings:\n        depends_on:\n          1:\n            component: nonexistent\n      vars:\n        x: 1\n"), 0644)

	idx, err := index.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	idx.SetBasePath(dir)
	idx.Reindex()

	f := idx.GetFile(stackPath)
	if f == nil {
		t.Fatal("expected file in index")
	}

	diags := runBestPracticeChecks(f, filepath.Dir(stackPath), idx)
	found := false
	for _, d := range diags {
		if d.Severity == SeverityError && strings.Contains(d.Message, "settings.depends_on references component") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected settings.depends_on missing component diagnostic, got: %+v", diags)
	}
}

func TestDiagnostics_SettingsDependsOn_ValidComponent(t *testing.T) {
	dir := t.TempDir()
	stackPath := filepath.Join(dir, "stacks/dev/stack.yaml")
	os.MkdirAll(filepath.Dir(stackPath), 0755)
	os.WriteFile(stackPath, []byte("components:\n  terraform:\n    vpc:\n      settings:\n        depends_on:\n          1:\n            component: other\n      vars:\n        x: 1\n    other:\n      vars:\n        y: 2\n"), 0644)

	idx, err := index.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	idx.SetBasePath(dir)
	idx.Reindex()

	f := idx.GetFile(stackPath)
	if f == nil {
		t.Fatal("expected file in index")
	}

	diags := runBestPracticeChecks(f, filepath.Dir(stackPath), idx)
	for _, d := range diags {
		if strings.Contains(d.Message, "settings.depends_on") {
			t.Fatalf("unexpected diagnostic for valid settings.depends_on: %+v", d)
		}
	}
}

func TestHandleHover_Import(t *testing.T) {
	dir := t.TempDir()
	defaultsPath := filepath.Join(dir, "stacks/dev/defaults.yaml")
	os.MkdirAll(filepath.Dir(defaultsPath), 0755)
	os.WriteFile(defaultsPath, []byte("vars:\n  namespace: dev\n"), 0644)

	stackPath := filepath.Join(dir, "stacks/dev/stack.yaml")
	os.MkdirAll(filepath.Dir(stackPath), 0755)
	os.WriteFile(stackPath, []byte("import:\n  - defaults\n"), 0644)

	idx, err := index.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	idx.SetBasePath(dir)
	idx.Reindex()
	h := New(idx, &mockDownstream{})

	content := mustMarshal(t, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"params": map[string]interface{}{
			"textDocument": map[string]string{"uri": "file://" + stackPath},
			"position":     map[string]uint32{"line": 1, "character": 4},
		},
	})

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
	if !strings.Contains(value, "## Import") {
		t.Fatalf("expected '## Import' in hover, got: %s", value)
	}
	if !strings.Contains(value, "defaults") {
		t.Fatalf("expected import path 'defaults' in hover, got: %s", value)
	}
}

func TestNodeRange_QuotedKey(t *testing.T) {
	// This test verifies that quoted YAML keys produce correct ranges.
	// It uses the real parseYAMLFile via a temp file in the index package.
	dir := t.TempDir()
	path := filepath.Join(dir, "test.yaml")
	os.WriteFile(path, []byte("components:\n  terraform:\n    \"quoted-key\":\n      vars:\n        x: 1\n"), 0644)

	idx, err := index.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	idx.SetBasePath(dir)
	idx.Reindex()

	f := idx.GetFile(path)
	if f == nil {
		t.Fatal("expected file in index")
	}
	if len(f.Comps) == 0 {
		t.Fatal("expected at least one component")
	}
	comp := f.Comps[0]
	if comp.Name != "quoted-key" {
		t.Fatalf("expected component name 'quoted-key', got %q", comp.Name)
	}
	// Line 2 (0-indexed), character should cover "quoted-key" including quotes
	// In the YAML text, the line is `    "quoted-key":`
	// The key starts at column 5 (0-indexed: 4), and the text is "quoted-key" (12 chars)
	// So StartChar should be 4 and EndChar should be 4 + 12 = 16
	if comp.Range.StartChar != 4 {
		t.Fatalf("expected StartChar=4 for quoted key, got %d", comp.Range.StartChar)
	}
	expectedEndChar := uint32(4 + len("\"quoted-key\""))
	if comp.Range.EndChar != expectedEndChar {
		t.Fatalf("expected EndChar=%d for quoted key, got %d", expectedEndChar, comp.Range.EndChar)
	}
}

func TestExtractPartialPath_Hyphen(t *testing.T) {
	line := "    - catalog/vpc-peering"
	cursor := len(line)
	got := extractPartialPath(line, cursor)
	want := "catalog/vpc-peering"
	if got != want {
		t.Fatalf("extractPartialPath(%q, %d) = %q, want %q", line, cursor, got, want)
	}
}

func TestExtractTemplatePartial_ClosingBrace(t *testing.T) {
	line := `value: "{{ .atmos_component }}"`
	// Cursor after the closing braces, inside the quotes
	cursor := strings.Index(line, `}}"`) + 2
	got, pos := extractTemplatePartial(line, cursor)
	if got != "" {
		t.Fatalf("extractTemplatePartial(%q, %d) = %q (pos=%d), want empty", line, cursor, got, pos)
	}
}

func TestFindPathCompletions_ExistingDir(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "catalog", "vpc"), 0755)
	os.WriteFile(filepath.Join(dir, "catalog", "vpc", "main.yaml"), []byte(""), 0644)
	os.WriteFile(filepath.Join(dir, "catalog", "vpc", "vars.yaml"), []byte(""), 0644)

	items := findPathCompletions(dir, "catalog/vpc", lsp.Range{})
	if len(items) == 0 {
		t.Fatal("expected completions for existing directory, got none")
	}
	var labels []string
	for _, it := range items {
		labels = append(labels, it["label"].(string))
	}
	if len(labels) != 2 {
		t.Fatalf("expected 2 completions, got %d: %v", len(labels), labels)
	}
}

func TestDocumentContent_DidClose(t *testing.T) {
	idx, _ := index.New(t.TempDir())
	h := New(idx, &mockDownstream{})
	uri := "file:///test.yaml"
	path := strings.TrimPrefix(uri, "file://")
	h.documentContent[path] = []byte("test content")

	content := mustMarshal(t, map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "textDocument/didClose",
		"params": map[string]interface{}{
			"textDocument": map[string]string{"uri": uri},
		},
	})

	handled, _, _, err := h.HandleMethod("textDocument/didClose", content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !handled {
		t.Fatal("expected handled")
	}
	if _, ok := h.documentContent[path]; ok {
		t.Fatal("expected documentContent to be deleted after didClose")
	}
}

func TestHandleInitialize(t *testing.T) {
	downstreamResp := mustMarshal(t, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"result": map[string]interface{}{
			"capabilities": map[string]interface{}{
				"documentFormattingProvider": true,
			},
		},
	})
	md := &mockDownstreamWithResponse{response: downstreamResp}

	dir := t.TempDir()
	idx, _ := index.New(dir)
	h := New(idx, md)

	content := mustMarshal(t, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"params": map[string]interface{}{
			"rootPath": dir,
			"initializationOptions": map[string]interface{}{
				"stacksPath":         "",
				"diagnosticsEnabled": true,
				"atmosCLIPath":       "",
				"logLevel":           "debug",
			},
		},
	})

	handled, resp, _, err := h.HandleMethod("initialize", content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !handled {
		t.Fatal("expected handled")
	}

	var result map[string]interface{}
	extractResult(resp, &result)
	caps := result["capabilities"].(map[string]interface{})
	if caps["definitionProvider"] != true {
		t.Fatal("expected bridge definitionProvider capability")
	}
	if caps["documentFormattingProvider"] != true {
		t.Fatal("expected downstream documentFormattingProvider capability merged")
	}
}

func TestHandleShutdown(t *testing.T) {
	dir := t.TempDir()
	idx, _ := index.New(dir)
	h := New(idx, &mockDownstream{})
	h.initialized.Store(true)

	content := mustMarshal(t, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
	})

	handled, resp, _, err := h.HandleMethod("shutdown", content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !handled {
		t.Fatal("expected handled")
	}
	if h.initialized.Load() {
		t.Fatal("expected initialized to be false after shutdown")
	}
	var result interface{}
	json.Unmarshal(resp, &result)
	if result == nil {
		t.Fatal("expected non-nil response")
	}
}

func TestHandleExit(t *testing.T) {
	dir := t.TempDir()
	idx, _ := index.New(dir)
	h := New(idx, &mockDownstream{})

	content := mustMarshal(t, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
	})

	handled, resp, _, err := h.HandleMethod("exit", content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !handled {
		t.Fatal("expected handled")
	}
	if resp != nil {
		t.Fatal("expected nil response for exit")
	}
}

func TestHandleCompletion_Template(t *testing.T) {
	dir := t.TempDir()
	stackPath := filepath.Join(dir, "stacks/dev/stack.yaml")
	os.MkdirAll(filepath.Dir(stackPath), 0755)
	os.WriteFile(stackPath, []byte("vars:\n  namespace: dev\ncomponents:\n  terraform:\n    database:\n      vars:\n        name: '{{ .vars.na }}'\n"), 0644)

	idx, err := index.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	idx.SetBasePath(dir)
	idx.Reindex()
	h := New(idx, &mockDownstream{})
	h.documentContent[stackPath] = []byte("vars:\n  namespace: dev\ncomponents:\n  terraform:\n    database:\n      vars:\n        name: '{{ .vars.na }}'\n")

	content := mustMarshal(t, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"params": map[string]interface{}{
			"textDocument": map[string]string{"uri": "file://" + stackPath},
			"position":     map[string]uint32{"line": 6, "character": 25},
		},
	})

	handled, resp, _, err := h.HandleMethod("textDocument/completion", content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !handled {
		t.Fatal("expected handled")
	}
	var result map[string]interface{}
	extractResult(resp, &result)
	items := result["items"].([]interface{})
	if len(items) == 0 {
		t.Fatal("expected template completion items")
	}
}

func TestHandleCompletion_Path(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "catalog", "vpc"), 0755)
	os.WriteFile(filepath.Join(dir, "catalog", "vpc", "main.yaml"), []byte(""), 0644)

	stackPath := filepath.Join(dir, "stacks/dev/stack.yaml")
	os.MkdirAll(filepath.Dir(stackPath), 0755)
	os.WriteFile(stackPath, []byte("import:\n  - catalog/vpc/main\n"), 0644)

	idx, err := index.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	idx.SetBasePath(dir)
	idx.Reindex()
	h := New(idx, &mockDownstream{})
	h.documentContent[stackPath] = []byte("import:\n  - catalog/vpc/\n")

	content := mustMarshal(t, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"params": map[string]interface{}{
			"textDocument": map[string]string{"uri": "file://" + stackPath},
			"position":     map[string]uint32{"line": 1, "character": 18},
		},
	})

	handled, resp, _, err := h.HandleMethod("textDocument/completion", content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !handled {
		t.Fatal("expected handled")
	}
	var result map[string]interface{}
	extractResult(resp, &result)
	items := result["items"].([]interface{})
	if len(items) == 0 {
		t.Fatal("expected path completion items")
	}
}

func TestHandleReferences_Import(t *testing.T) {
	dir := t.TempDir()
	defaultsPath := filepath.Join(dir, "stacks/dev/defaults.yaml")
	os.MkdirAll(filepath.Dir(defaultsPath), 0755)
	os.WriteFile(defaultsPath, []byte("vars:\n  namespace: dev\n"), 0644)

	stackPath := filepath.Join(dir, "stacks/dev/stack.yaml")
	os.MkdirAll(filepath.Dir(stackPath), 0755)
	os.WriteFile(stackPath, []byte("import:\n  - defaults\n"), 0644)

	idx, err := index.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	idx.SetBasePath(dir)
	idx.Reindex()
	h := New(idx, &mockDownstream{})

	content := mustMarshal(t, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"params": map[string]interface{}{
			"textDocument": map[string]string{"uri": "file://" + stackPath},
			"position":     map[string]uint32{"line": 1, "character": 4},
		},
	})

	handled, resp, _, err := h.HandleMethod("textDocument/references", content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !handled {
		t.Fatal("expected handled")
	}
	var locations []map[string]interface{}
	extractResult(resp, &locations)
	if len(locations) == 0 {
		t.Fatal("expected at least one reference location for import")
	}
}

func TestHandleReferences_Component(t *testing.T) {
	dir := t.TempDir()
	stackPath := filepath.Join(dir, "stacks/dev/stack.yaml")
	os.MkdirAll(filepath.Dir(stackPath), 0755)
	os.WriteFile(stackPath, []byte("components:\n  terraform:\n    database:\n      vars:\n        size: large\n"), 0644)

	idx, err := index.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	idx.SetBasePath(dir)
	idx.Reindex()
	h := New(idx, &mockDownstream{})

	content := mustMarshal(t, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"params": map[string]interface{}{
			"textDocument": map[string]string{"uri": "file://" + stackPath},
			"position":     map[string]uint32{"line": 2, "character": 4},
		},
	})

	handled, resp, _, err := h.HandleMethod("textDocument/references", content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !handled {
		t.Fatal("expected handled")
	}
	var locations []map[string]interface{}
	extractResult(resp, &locations)
	if len(locations) == 0 {
		t.Fatal("expected at least one reference location for component")
	}
}

func TestHandleDiagnostics_DidOpen(t *testing.T) {
	dir := t.TempDir()
	stackPath := filepath.Join(dir, "stacks/dev/stack.yaml")
	os.MkdirAll(filepath.Dir(stackPath), 0755)
	os.WriteFile(stackPath, []byte("vars:\n  namespace: dev\n"), 0644)

	idx, err := index.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	idx.SetBasePath(dir)
	idx.Reindex()
	h := New(idx, &mockDownstream{})

	content := mustMarshal(t, map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "textDocument/didOpen",
		"params": map[string]interface{}{
			"textDocument": map[string]interface{}{
				"uri":  "file://" + stackPath,
				"text": "vars:\n  namespace: staging\n",
			},
		},
	})

	handled, _, _, err := h.HandleMethod("textDocument/didOpen", content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !handled {
		t.Fatal("expected handled")
	}

	// Verify documentContent was updated
	if doc, ok := h.documentContent[stackPath]; !ok || !strings.Contains(string(doc), "staging") {
		t.Fatal("expected documentContent to be updated with didOpen text")
	}
}

func TestHandleDiagnostics_DidChange(t *testing.T) {
	dir := t.TempDir()
	stackPath := filepath.Join(dir, "stacks/dev/stack.yaml")
	os.MkdirAll(filepath.Dir(stackPath), 0755)
	os.WriteFile(stackPath, []byte("vars:\n  namespace: dev\n"), 0644)

	idx, err := index.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	idx.SetBasePath(dir)
	idx.Reindex()
	h := New(idx, &mockDownstream{})

	content := mustMarshal(t, map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "textDocument/didChange",
		"params": map[string]interface{}{
			"textDocument": map[string]interface{}{"uri": "file://" + stackPath},
			"contentChanges": []map[string]string{
				{"text": "vars:\n  namespace: changed\n"},
			},
		},
	})

	handled, _, _, err := h.HandleMethod("textDocument/didChange", content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !handled {
		t.Fatal("expected handled")
	}

	// Verify documentContent was updated
	if doc, ok := h.documentContent[stackPath]; !ok || !strings.Contains(string(doc), "changed") {
		t.Fatal("expected documentContent to be updated with didChange text")
	}
}

func TestHandleDiagnostics_DidSave(t *testing.T) {
	dir := t.TempDir()
	stackPath := filepath.Join(dir, "stacks/dev/stack.yaml")
	os.MkdirAll(filepath.Dir(stackPath), 0755)
	os.WriteFile(stackPath, []byte("vars:\n  namespace: dev\n"), 0644)

	idx, err := index.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	idx.SetBasePath(dir)
	idx.Reindex()
	h := New(idx, &mockDownstream{})
	h.documentContent[stackPath] = []byte("vars:\n  namespace: temp\n")

	content := mustMarshal(t, map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "textDocument/didSave",
		"params": map[string]interface{}{
			"textDocument": map[string]interface{}{"uri": "file://" + stackPath},
		},
	})

	handled, _, _, err := h.HandleMethod("textDocument/didSave", content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !handled {
		t.Fatal("expected handled")
	}

	// Verify documentContent was cleared
	if _, ok := h.documentContent[stackPath]; ok {
		t.Fatal("expected documentContent to be cleared after didSave")
	}
}
