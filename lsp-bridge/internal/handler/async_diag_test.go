package handler

import (
    "encoding/json"
    "fmt"
    "os"
    "path/filepath"
    "testing"
    "time"

    "github.com/jgibbarduk/zed-atmos-extension/lsp-bridge/internal/index"
)

func TestAsyncDiagnostics(t *testing.T) {
    dir := t.TempDir()
    stackPath := filepath.Join(dir, "stack.yaml")
    os.WriteFile(stackPath, []byte("import:\n  - missing\n"), 0644)

    idx, _ := index.New(dir)
    idx.SetBasePath(dir)
    idx.Reindex()

    h := New(idx, &mockDownstream{})

    content, _ := json.Marshal(map[string]interface{}{
        "jsonrpc": "2.0",
        "method":  "textDocument/didOpen",
        "params": map[string]interface{}{
            "textDocument": map[string]interface{}{
                "uri":  "file://" + stackPath,
                "text": "import:\n  - missing\n",
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

    // Wait for async diagnostics
    time.Sleep(500 * time.Millisecond)

    select {
    case notif := <- h.Notifications():
        fmt.Printf("NOTIFICATION: %s\n", string(notif))
    default:
        t.Fatal("expected async diagnostic notification")
    }
}
