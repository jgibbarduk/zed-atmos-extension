package handler

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jgibbarduk/zed-atmos-extension/lsp-bridge/internal/index"
)

var componentNamePattern = regexp.MustCompile(`^[/a-zA-Z0-9-_{}. ]+$`)

var validBackendTypes = map[string]bool{
	"local": true, "s3": true, "remote": true, "vault": true,
	"static": true, "azurerm": true, "gcs": true, "cloud": true,
}

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

	// Check 0: Empty import array
	if len(file.Imports) == 0 {
		diags = append(diags, Diagnostic{
			Severity: SeverityHint,
			Message:  "No imports defined — consider importing base settings",
			Range:    index.Range{StartLine: 0, StartChar: 0, EndLine: 0, EndChar: 0},
			Source:   "atmos-best-practice",
		})
	}

	// Check 12: Duplicate component names in the same file
	compSeen := make(map[string]index.CompNode)
	for _, comp := range file.Comps {
		if prev, ok := compSeen[comp.Name]; ok {
			diags = append(diags, Diagnostic{
				Severity: SeverityError,
				Message:  fmt.Sprintf("Duplicate component name '%s' in this file", comp.Name),
				Range:    comp.Range,
				Source:   "atmos-component",
			})
			_ = prev
		} else {
			compSeen[comp.Name] = comp
		}
	}

	// Check 13: Component name must match Atmos pattern
	for _, comp := range file.Comps {
		if !componentNamePattern.MatchString(comp.Name) {
			diags = append(diags, Diagnostic{
				Severity: SeverityError,
				Message:  fmt.Sprintf("Component name '%s' contains invalid characters (must match ^[/a-zA-Z0-9-_{}. ]+$)", comp.Name),
				Range:    comp.Range,
				Source:   "atmos-component",
			})
		}
	}

	// Check 14: Metadata.type must be "abstract" or "real"
	for _, meta := range file.Metadata {
		if meta.Type != "" && meta.Type != "abstract" && meta.Type != "real" {
			diags = append(diags, Diagnostic{
				Severity: SeverityError,
				Message:  fmt.Sprintf("metadata.type must be 'abstract' or 'real', got '%s'", meta.Type),
				Range:    meta.Range,
				Source:   "atmos-schema",
			})
		}
	}

	// Check 15: Backend type enum validation
	for _, bt := range file.BackendTypes {
		if !validBackendTypes[bt.Type] {
			kind := bt.Kind
			diags = append(diags, Diagnostic{
				Severity: SeverityError,
				Message:  fmt.Sprintf("Invalid %s '%s' (must be one of: local, s3, remote, vault, static, azurerm, gcs, cloud)", kind, bt.Type),
				Range:    bt.Range,
				Source:   "atmos-schema",
			})
		}
	}

	// Check 16: settings.depends_on component existence
	for _, sd := range file.SettingsDeps {
		if sd.Component == "" {
			continue
		}
		refs := idx.FindComponent(sd.Component)
		if len(refs) == 0 {
			diags = append(diags, Diagnostic{
				Severity: SeverityError,
				Message:  fmt.Sprintf("settings.depends_on references component '%s' which was not found", sd.Component),
				Range:    sd.Range,
				Source:   "atmos-deps",
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
		if dep.Component == "" {
			continue
		}
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

	// Check 9: Unknown template variables in template expressions
	vars := collectVars(file, idx)
	for _, v := range file.Vars {
		if !strings.Contains(v.Value, "{{") || !strings.Contains(v.Value, "}}") {
			continue
		}
		seen := make(map[string]bool)
		for _, m := range templateVarExprRe.FindAllStringSubmatch(v.Value, -1) {
			if len(m) > 1 {
				key := m[1]
				if seen[key] {
					continue
				}
				seen[key] = true
				if _, ok := vars[key]; !ok {
					diags = append(diags, Diagnostic{
						Severity: SeverityError,
						Message:  fmt.Sprintf("Unknown template variable '.vars.%s'", key),
						Range:    v.Range,
						Source:   "atmos-template",
					})
				}
			}
		}
		for _, m := range nameTemplateKeyRe.FindAllStringSubmatch(v.Value, -1) {
			if len(m) > 1 {
				key := m[1]
				if key == "atmos_component" || key == "atmos_stack" {
					continue
				}
				if seen[key] {
					continue
				}
				seen[key] = true
				if _, ok := vars[key]; !ok {
					diags = append(diags, Diagnostic{
						Severity: SeverityError,
						Message:  fmt.Sprintf("Unknown template variable '.%s'", key),
						Range:    v.Range,
						Source:   "atmos-template",
					})
				}
			}
		}
	}

	// Check 10: Duplicate imports
	importSeen := make(map[string]index.ImportNode)
	for _, imp := range file.Imports {
		if prev, ok := importSeen[imp.RawPath]; ok {
			diags = append(diags, Diagnostic{
				Severity: SeverityWarning,
				Message:  fmt.Sprintf("Duplicate import '%s'", imp.RawPath),
				Range:    imp.Range,
				Source:   "atmos-import",
			})
			_ = prev
		} else {
			importSeen[imp.RawPath] = imp
		}
	}

	// Check 11: Circular imports
	if hasCircularImport(file.Path, idx, nil) {
		diags = append(diags, Diagnostic{
			Severity: SeverityError,
			Message:  "Circular import detected in this stack file",
			Range:    index.Range{StartLine: 0, StartChar: 0, EndLine: 0, EndChar: 0},
			Source:   "atmos-import",
		})
	}

	return diags
}

func hasCircularImport(path string, idx *index.Index, visited []string) bool {
	for _, v := range visited {
		if v == path {
			return true
		}
	}
	f := idx.GetFile(path)
	if f == nil {
		return false
	}
	nextVisited := append([]string(nil), visited...)
	nextVisited = append(nextVisited, path)
	for _, imp := range f.Imports {
		resolved := idx.ResolveImport(imp.RawPath, filepath.Dir(path))
		for _, r := range resolved {
			if hasCircularImport(r, idx, nextVisited) {
				return true
			}
		}
	}
	return false
}
