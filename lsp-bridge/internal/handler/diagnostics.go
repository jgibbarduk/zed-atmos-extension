package handler

import (
	"path/filepath"
	"strings"

	"github.com/jamesgibbard/zed-atmos-language/lsp-bridge/internal/index"
)

type Diagnostic struct {
	Severity uint32     `json:"severity"`
	Message  string     `json:"message"`
	Range    index.Range `json:"range"`
	Source   string     `json:"source"`
}

const (
	SeverityHint    = 4
	SeverityInfo    = 3
	SeverityWarning = 2
	SeverityError   = 1
)

func runBestPracticeChecks(file *index.StackFile, dir string, idx *index.Index) []Diagnostic {
	var diags []Diagnostic
	filename := filepath.Base(file.Path)

	// Check 1: No _defaults.yaml ancestor
	defaultsPath := filepath.Join(dir, "_defaults.yaml")
	if _, ok := idx.Files[defaultsPath]; !ok && filename != "_defaults.yaml" {
		parentDir := filepath.Dir(dir)
		parentDefaults := filepath.Join(parentDir, "_defaults.yaml")
		if _, ok2 := idx.Files[parentDefaults]; !ok2 {
			diags = append(diags, Diagnostic{
				Severity: SeverityHint,
				Message:  "Consider adding a `_defaults.yaml` at this level for shared settings",
				Range:    index.Range{StartLine: 0, StartChar: 0, EndLine: 0, EndChar: 0},
				Source:   "atmos-best-practice",
			})
		}
	}

	// Check 2: Import order — check for overrides imported before base
	if len(file.Imports) > 1 {
		for i, imp := range file.Imports {
			resolvedPath := imp.RawPath
			if strings.Contains(resolvedPath, "override") && i == 0 {
				diags = append(diags, Diagnostic{
					Severity: SeverityHint,
					Message:  "Base imports should come first; later imports override earlier",
					Range:    imp.Range,
					Source:   "atmos-best-practice",
				})
			}
		}
	}

	// Check 3: Component not in catalog directory
	for _, comp := range file.Comps {
		if !strings.Contains(dir, "catalog") {
			diags = append(diags, Diagnostic{
				Severity: SeverityHint,
				Message:  "Consider using a catalog (" + filepath.Join(idx.BasePath, "catalog") + ") for reusable component blueprints",
				Range:    comp.Range,
				Source:   "atmos-best-practice",
			})
		}
	}

	return diags
}
