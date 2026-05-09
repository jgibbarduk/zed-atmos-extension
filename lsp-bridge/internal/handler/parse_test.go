package handler

import (
    "fmt"
    "os"
    "testing"

    "github.com/jgibbarduk/zed-atmos-extension/lsp-bridge/internal/index"
)

func TestParseUserDemo(t *testing.T) {
    path := "/Users/jamesgibbard/Development/atmos-test-project/stacks/orgs/ex1/core/auto/us-east-2/demo.yaml"
    content, _ := os.ReadFile(path)
    sf := index.ParseYAMLContent(path, content)
    fmt.Printf("ParseError: %q\n", sf.ParseError)
    fmt.Printf("Imports: %d\n", len(sf.Imports))
    for _, imp := range sf.Imports {
        fmt.Printf("  - %q (line %d-%d)\n", imp.RawPath, imp.Range.StartLine, imp.Range.EndLine)
    }
    fmt.Printf("Comps: %d\n", len(sf.Comps))
    fmt.Printf("Vars: %d\n", len(sf.Vars))
}
