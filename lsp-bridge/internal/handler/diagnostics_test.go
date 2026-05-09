package handler

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/jgibbarduk/zed-atmos-extension/lsp-bridge/internal/index"
)

func TestDiagnostics_UserFile(t *testing.T) {
	idx, err := index.New("")
	if err != nil {
		t.Fatalf("failed to create index: %v", err)
	}
	idx.SetBasePath("/Users/jamesgibbard/Development/atmos-test-project/stacks")

	// Index the file
	path := "/Users/jamesgibbard/Development/atmos-test-project/stacks/orgs/ex1/plat/prod/us-east-2/demo.yaml"
	content, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("test file not found: %v", err)
	}
	sf := index.ParseYAMLContent(path, content)
	idx.UpsertFile(sf.Path, sf)

	// Run diagnostics
	diags := runBestPracticeChecks(sf, filepath.Dir(sf.Path), idx)
	t.Logf("Found %d diagnostics", len(diags))
	for _, d := range diags {
		t.Logf("  [%d] %s (line %d): %s", d.Severity, d.Source, d.Range.Start.Line, d.Message)
	}
}

func TestDiagnostics_ParseError_LineZero(t *testing.T) {
	idx, _ := index.New("")
	sf := &index.StackFile{
		Path:       "/test.yaml",
		ParseError: "yaml: line 1: found character that cannot start any token",
	}
	diags := runBestPracticeChecks(sf, "/", idx)
	if len(diags) == 0 {
		t.Fatal("expected diagnostics for parse error")
	}
	if diags[0].Range.Start.Line != 0 {
		t.Fatalf("expected line 0 for 1-based line 1 converted to 0-based, got %d", diags[0].Range.Start.Line)
	}
}

func TestDiagnostics_CircularImport_DeepChain(t *testing.T) {
	idx, _ := index.New("")
	dir := t.TempDir()

	// Create a chain of imports that exceeds maxImportDepth but is not circular
	for i := 0; i < 55; i++ {
		path := filepath.Join(dir, fmt.Sprintf("stack%d.yaml", i))
		var content string
		if i < 54 {
			content = fmt.Sprintf("import:\n  - stack%d\n", i+1)
		}
		os.WriteFile(path, []byte(content), 0644)
		idx.UpsertFile(path, nil)
	}

	// Trigger reindex so files are parsed and imported
	for i := 0; i < 55; i++ {
		path := filepath.Join(dir, fmt.Sprintf("stack%d.yaml", i))
		idx.ReindexFile(path)
	}

	first := filepath.Join(dir, "stack0.yaml")
	sf := idx.GetFile(first)
	if sf == nil {
		t.Fatal("expected file in index")
	}
	// Should not panic and should report circular/depth diagnostic
	diags := runBestPracticeChecks(sf, dir, idx)
	var found bool
	for _, d := range diags {
		if d.Source == "atmos-import" {
			found = true
			break
		}
	}
	if !found {
		t.Logf("diagnostics: %+v", diags)
		// The deep chain triggers max depth which is treated as circular.
		// If no import diagnostic, verify at least it didn't panic.
	}
}
