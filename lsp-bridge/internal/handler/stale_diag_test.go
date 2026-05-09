package handler

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jgibbarduk/zed-atmos-extension/lsp-bridge/internal/index"
)

func TestStaleDiagnostic_ClearsAfterFix(t *testing.T) {
	dir := t.TempDir()
	// Create catalog/account-map.yaml
	os.MkdirAll(filepath.Join(dir, "catalog"), 0755)
	os.WriteFile(filepath.Join(dir, "catalog", "account-map.yaml"), []byte(""), 0644)

	stackPath := filepath.Join(dir, "stack.yaml")
	// First version: typo in import
	os.WriteFile(stackPath, []byte("import:\n  - catalog/account-maps\n"), 0644)

	idx, _ := index.New(dir)
	idx.SetBasePath(dir)
	idx.Reindex()
	h := New(idx, &mockDownstream{})

	// Open the file with typo
	content, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "textDocument/didOpen",
		"params": map[string]interface{}{
			"textDocument": map[string]interface{}{
				"uri":  "file://" + stackPath,
				"text": "import:\n  - catalog/account-maps\n",
			},
		},
	})
	h.HandleMethod("textDocument/didOpen", content)

	time.Sleep(400 * time.Millisecond)
	notif := <-h.Notifications()
	t.Logf("After typo: %s", string(notif))

	// Now simulate a full-document change that fixes the typo
	fixedContent, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "textDocument/didChange",
		"params": map[string]interface{}{
			"textDocument": map[string]interface{}{"uri": "file://" + stackPath},
			"contentChanges": []map[string]interface{}{
				{"text": "import:\n  - catalog/account-map\n"},
			},
		},
	})
	h.HandleMethod("textDocument/didChange", fixedContent)

	time.Sleep(400 * time.Millisecond)
	fixedNotif := <-h.Notifications()
	t.Logf("After fix: %s", string(fixedNotif))

	// After fix, there should be no "cannot be resolved" diagnostic
	if string(fixedNotif) == "" {
		t.Fatal("expected notification after fix")
	}
	if strings.Contains(string(fixedNotif), "cannot be resolved") {
		t.Fatal("diagnostic should have cleared after fixing the import")
	}
}
