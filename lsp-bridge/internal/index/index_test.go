package index

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRemoveFile(t *testing.T) {
	idx, _ := New("")
	dir := t.TempDir()
	path := filepath.Join(dir, "stack.yaml")
	os.WriteFile(path, []byte("components:\n  terraform:\n    db:\n"), 0644)
	idx.ReindexFile(path)

	sf := idx.GetFile(path)
	if sf == nil {
		t.Fatal("expected file in index before removal")
	}

	idx.RemoveFile(path)

	sf = idx.GetFile(path)
	if sf != nil {
		t.Fatal("expected file to be removed from index")
	}

	// Verify byComponent index is cleaned up
	paths := idx.FindComponent("db")
	if len(paths) != 0 {
		t.Fatalf("expected 0 paths for component 'db', got %d", len(paths))
	}
}
