package handler

import (
	"fmt"
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
	if idx.GetFile(defaultsPath) == nil && filename != "_defaults.yaml" {
		parentDir := filepath.Dir(dir)
		parentDefaults := filepath.Join(parentDir, "_defaults.yaml")
		if idx.GetFile(parentDefaults) == nil {
			diags = append(diags, Diagnostic{
				Severity: SeverityHint,
				Message:  "Consider adding a `_defaults.yaml` at this level for shared settings",
				Range:    index.Range{StartLine: 0, StartChar: 0, EndLine: 0, EndChar: 0},
				Source:   "atmos-best-practice",
			})
		}
	}

	// Check 4: Unresolvable imports
	for _, imp := range file.Imports {
		resolved := idx.ResolveImport(imp.RawPath, dir)
		if len(resolved) == 0 {
			diags = append(diags, Diagnostic{
				Severity: SeverityError,
				Message:  fmt.Sprintf("Import '%s' cannot be resolved", imp.RawPath),
				Range:    imp.Range,
				Source:   "atmos-import",
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

	// Check 5: Unquoted version values
	for _, v := range file.Vars {
		if strings.Contains(strings.ToLower(v.Key), "version") && !v.IsQuoted {
			diags = append(diags, Diagnostic{
				Severity: SeverityWarning,
				Message:  fmt.Sprintf("Version '%s' should be quoted to prevent YAML float parsing", v.Value),
				Range:    v.Range,
				Source:   "atmos-version",
			})
		}
	}

	// Check 3: Component not in catalog directory
	for _, comp := range file.Comps {
		if !strings.Contains(dir, "catalog") {
			diags = append(diags, Diagnostic{
				Severity: SeverityHint,
				Message:  "Consider using a catalog (" + filepath.Join(idx.BasePath(), "catalog") + ") for reusable component blueprints",
				Range:    comp.Range,
				Source:   "atmos-best-practice",
			})
		}
	}

	// Check 6: Abstract component deployability warning
	for _, meta := range file.Metadata {
		if meta.Type == "abstract" && meta.Component != "" {
			inheritors := idx.FindInheritors(meta.Component)
			if len(inheritors) == 0 {
				diags = append(diags, Diagnostic{
					Severity: SeverityWarning,
					Message:  fmt.Sprintf("Abstract component '%s' has no inheritors", meta.Component),
					Range:    meta.Range,
					Source:   "atmos-abstract",
				})
			}
		}
	}

	// Check 7: Validate dependencies.components
	for _, dep := range file.Deps {
		refs := idx.FindComponent(dep.Component)
		if len(refs) == 0 {
			diags = append(diags, Diagnostic{
				Severity: SeverityError,
				Message:  fmt.Sprintf("Dependency component '%s' not found", dep.Component),
				Range:    dep.Range,
				Source:   "atmos-deps",
			})
		}
	}

	// Check 8: Validate !terraform.state references match declared dependencies
	for _, ts := range file.TerraformState {
		if ts.Component == "" {
			continue
		}
		found := false
		for _, dep := range file.Deps {
			if dep.Component == ts.Component {
				found = true
				break
			}
		}
		if !found {
			diags = append(diags, Diagnostic{
				Severity: SeverityWarning,
				Message:  fmt.Sprintf("!terraform.state references '%s' but it is not declared in dependencies.components", ts.Component),
				Range:    ts.Range,
				Source:   "atmos-deps",
			})
		}
	}

	return diags
}
