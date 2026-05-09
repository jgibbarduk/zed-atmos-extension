package handler

import (
	"path/filepath"
	"testing"

	"github.com/jgibbarduk/zed-atmos-extension/lsp-bridge/internal/index"
)

func TestImportResolution_CatalogAccountMap(t *testing.T) {
	idx, _ := index.New("/Users/jamesgibbard/Development/atmos-test-project/stacks")
	idx.SetBasePath("/Users/jamesgibbard/Development/atmos-test-project/stacks")
	idx.Reindex()

	path := "/Users/jamesgibbard/Development/atmos-test-project/stacks/orgs/ex1/core/root/global-region/demo.yaml"
	resolved := idx.ResolveImport("catalog/account-map", filepath.Dir(path))

	t.Logf("Resolved imports for catalog/account-map from %s: %v", filepath.Dir(path), resolved)

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
