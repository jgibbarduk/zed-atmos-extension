package handler

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jgibbarduk/zed-atmos-extension/lsp-bridge/internal/index"
)

func TestImportResolution_CatalogAccountMap(t *testing.T) {
	dir := t.TempDir()
	stacksDir := filepath.Join(dir, "stacks")
	catalogDir := filepath.Join(stacksDir, "catalog")
	os.MkdirAll(catalogDir, 0755)
	os.WriteFile(filepath.Join(catalogDir, "account-map.yaml"), []byte("vars:\n  account_id: '123456789'\n"), 0644)

	stackDir := filepath.Join(stacksDir, "orgs", "ex1", "core", "root", "global-region")
	os.MkdirAll(stackDir, 0755)
	stackPath := filepath.Join(stackDir, "demo.yaml")
	os.WriteFile(stackPath, []byte("import:\n  - catalog/account-map\n"), 0644)

	idx, _ := index.New(stacksDir)
	idx.SetBasePath(stacksDir)
	idx.Reindex()

	resolved := idx.ResolveImport("catalog/account-map", filepath.Dir(stackPath))

	t.Logf("Resolved imports for catalog/account-map from %s: %v", filepath.Dir(stackPath), resolved)

	found := false
	for _, r := range resolved {
		if filepath.Base(r) == "account-map.yaml" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected catalog/account-map to resolve to account-map.yaml, got %v", resolved)
	}
}
