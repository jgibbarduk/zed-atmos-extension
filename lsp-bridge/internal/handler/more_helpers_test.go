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

func TestDefaultConfig(t *testing.T) {
	cfg := defaultConfig()
	if cfg.LogLevel != "info" {
		t.Fatalf("expected LogLevel=info, got %q", cfg.LogLevel)
	}
	if cfg.DiagnosticsEnabled == nil || !*cfg.DiagnosticsEnabled {
		t.Fatal("expected DiagnosticsEnabled=true")
	}
}

func TestErrorResponse(t *testing.T) {
	content := []byte(`{"jsonrpc":"2.0","id":1}`)
	resp := errorResponse(content, -32602, "Invalid params")
	var result map[string]interface{}
	if err := json.Unmarshal(resp, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result["jsonrpc"] != "2.0" {
		t.Fatalf("expected jsonrpc=2.0, got %v", result["jsonrpc"])
	}
	if result["id"] != float64(1) {
		t.Fatalf("expected id=1, got %v", result["id"])
	}
	errObj := result["error"].(map[string]interface{})
	if errObj["code"] != float64(-32602) {
		t.Fatalf("expected code=-32602, got %v", errObj["code"])
	}
}

func TestEmptyResult(t *testing.T) {
	content := []byte(`{"jsonrpc":"2.0","id":42}`)
	resp := emptyResult(content, json.RawMessage(`42`))
	var result map[string]interface{}
	if err := json.Unmarshal(resp, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	arr, ok := result["result"].([]interface{})
	if !ok || len(arr) != 0 {
		t.Fatalf("expected empty result array, got %v", result["result"])
	}
}

func TestNullResult(t *testing.T) {
	content := []byte(`{"jsonrpc":"2.0","id":99}`)
	resp := nullResult(content)
	var result map[string]interface{}
	if err := json.Unmarshal(resp, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result["result"] != nil {
		t.Fatalf("expected null result, got %v", result["result"])
	}
	if result["id"] != float64(99) {
		t.Fatalf("expected id=99, got %v", result["id"])
	}
}

func TestHoverBuilder(t *testing.T) {
	hb := hoverBuilder{}
	hb.inlineCode("test")
	hb.paragraph("hello")
	out := hb.build()
	if out != "`test`\n\nhello" {
		t.Fatalf("unexpected build output: %q", out)
	}
}

func TestIsMetadataComponentLine(t *testing.T) {
	for _, tc := range []struct {
		line string
		want bool
	}{
		{"component: vpc", true},
		{"  component: vpc", true},
		{"other: value", false},
		{"", false},
	} {
		got := isMetadataComponentLine(tc.line)
		if got != tc.want {
			t.Fatalf("isMetadataComponentLine(%q)=%v, want %v", tc.line, got, tc.want)
		}
	}
}

func TestFindComponentCompletions(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "components", "terraform"), 0755)
	os.MkdirAll(filepath.Join(dir, "components", "helmfile"), 0755)

	items := findComponentCompletions(dir, "terr", lsp.Range{})
	if len(items) != 1 {
		t.Fatalf("expected 1 completion, got %d", len(items))
	}
	if items[0]["label"] != "terraform" {
		t.Fatalf("expected terraform, got %v", items[0]["label"])
	}

	items = findComponentCompletions(dir, "", lsp.Range{})
	if len(items) != 2 {
		t.Fatalf("expected 2 completions, got %d", len(items))
	}

	items = findComponentCompletions(filepath.Join(dir, "missing"), "", lsp.Range{})
	if items != nil {
		t.Fatalf("expected nil for missing dir, got %v", items)
	}
}

func TestFindTemplateCompletions(t *testing.T) {
	dir := t.TempDir()
	idx, _ := index.New(dir)
	idx.UpsertFile(filepath.Join(dir, "parent.yaml"), &index.StackFile{
		Path: filepath.Join(dir, "parent.yaml"),
		Vars: []index.VarNode{{Key: "namespace", Value: "prod"}},
	})

	sf := &index.StackFile{
		Path:    filepath.Join(dir, "child.yaml"),
		Imports: []index.ImportNode{{RawPath: "parent"}},
		Vars:    []index.VarNode{{Key: "region", Value: "us-east-1"}},
	}
	idx.UpsertFile(sf.Path, sf)

	r := lsp.Range{Start: lsp.Position{Line: 0, Character: 0}, End: lsp.Position{Line: 0, Character: 0}}

	items := findTemplateCompletions(idx, sf.Path, "vars.", r)
	if len(items) == 0 {
		t.Fatal("expected completions for vars.")
	}

	items = findTemplateCompletions(idx, sf.Path, "atmos_", r)
	found := false
	for _, it := range items {
		if it["label"] == "atmos_component" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected atmos_component builtin completion")
	}

	items = findTemplateCompletions(idx, sf.Path, "", r)
	if len(items) == 0 {
		t.Fatal("expected completions for empty partial")
	}
}

func TestFindTemplateCompletions_detailTruncation(t *testing.T) {
	dir := t.TempDir()
	idx, _ := index.New(dir)
	longValue := strings.Repeat("a", 50)
	sf := &index.StackFile{
		Path: filepath.Join(dir, "test.yaml"),
		Vars: []index.VarNode{{Key: "long", Value: longValue}},
	}
	idx.UpsertFile(sf.Path, sf)
	r := lsp.Range{}
	items := findTemplateCompletions(idx, sf.Path, "vars.long", r)
	if len(items) == 0 {
		t.Fatal("expected completion")
	}
	detail := items[0]["detail"].(string)
	if len(detail) != 40 {
		t.Fatalf("expected detail truncated to 40, got %d", len(detail))
	}
}
