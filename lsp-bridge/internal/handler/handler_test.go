package handler

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jamesgibbard/zed-atmos-language/lsp-bridge/internal/index"
	"github.com/jamesgibbard/zed-atmos-language/lsp-bridge/internal/lsp"
)

// mockDownstream implements DownstreamCaller for tests.
type mockDownstream struct{}

func (m *mockDownstream) CallDownstream(content []byte) ([]byte, error) { return nil, nil }
func (m *mockDownstream) SendNotification(content []byte) error         { return nil }

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
	if !strings.Contains(value, "Component:") {
		t.Fatalf("expected 'Component:' in hover, got: %s", value)
	}
	if !strings.Contains(value, "Stack name:") {
		t.Fatalf("expected 'Stack name:' in hover, got: %s", value)
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
	if !strings.Contains(value, "Template:") {
		t.Fatalf("expected 'Template:' in hover, got: %s", value)
	}
	if !strings.Contains(value, "Resolved:") {
		t.Fatalf("expected 'Resolved:' in hover, got: %s", value)
	}
	if !strings.Contains(value, "dev_db") {
		t.Fatalf("expected resolved value 'dev_db' in hover, got: %s", value)
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
		if e.NewText == "renamed-db" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected rename edit with NewText='renamed-db', got %+v", edits)
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
	if !strings.Contains(value, "Import:") {
		t.Fatalf("expected 'Import:' in hover, got: %s", value)
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
