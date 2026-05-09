package handler

import (
	"fmt"
	"log"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jgibbarduk/zed-atmos-extension/lsp-bridge/internal/index"
	"github.com/jgibbarduk/zed-atmos-extension/lsp-bridge/internal/lsp"
)

var componentNamePattern = regexp.MustCompile(`^[/a-zA-Z0-9-_{}. ]+$`)

var validBackendTypes = map[string]bool{
	"local": true, "s3": true, "remote": true, "vault": true,
	"static": true, "azurerm": true, "gcs": true, "cloud": true,
}

type diagnostic struct {
	Severity uint32    `json:"severity"`
	Message  string    `json:"message"`
	Range    lsp.Range `json:"range"`
	Source   string    `json:"source"`
}

const (
	SeverityHint    = 4
	SeverityInfo    = 3
	SeverityWarning = 2
	SeverityError   = 1
)

func runBestPracticeChecks(file *index.StackFile, dir string, idx *index.Index) []diagnostic {
	var diags []diagnostic
	filename := filepath.Base(file.Path)

	// Check 0: YAML parse error
	if file.ParseError != "" {
		// Try to extract line number from error like "yaml: line 30: found unexpected end of stream"
		var lineNum int
		var msg string
		if n, _ := fmt.Sscanf(file.ParseError, "yaml: line %d:", &lineNum); n == 1 {
			msg = fmt.Sprintf("YAML syntax error: %s", file.ParseError)
			lineNum-- // convert to 0-based
			if lineNum < 0 {
				lineNum = 0
			}
		} else {
			msg = fmt.Sprintf("YAML syntax error: %s", file.ParseError)
		}
		line := uint32(lineNum)
		diags = append(diags, diagnostic{
			Severity: SeverityError,
			Message:  msg,
			Range:    toLSPRange(index.Range{StartLine: line, StartChar: 0, EndLine: line, EndChar: 0}),
			Source:   "atmos-yaml",
		})
	}

	// Check 1: No _defaults.yaml ancestor
	defaultsPath := filepath.Join(dir, "_defaults.yaml")
	if idx.GetFileUnsafe(defaultsPath) == nil && filename != "_defaults.yaml" {
		parentDir := filepath.Dir(dir)
		parentDefaults := filepath.Join(parentDir, "_defaults.yaml")
		if idx.GetFileUnsafe(parentDefaults) == nil {
			diags = append(diags, diagnostic{
				Severity: SeverityHint,
				Message:  "Consider adding a `_defaults.yaml` at this level for shared settings",
				Range:    toLSPRange(index.Range{StartLine: 0, StartChar: 0, EndLine: 0, EndChar: 0}),
				Source:   "atmos-best-practice",
			})
		}
	}

	// Check 2: Empty import array
	if len(file.Imports) == 0 {
		diags = append(diags, diagnostic{
			Severity: SeverityHint,
			Message:  "No imports defined — consider importing base settings",
			Range:    toLSPRange(index.Range{StartLine: 0, StartChar: 0, EndLine: 0, EndChar: 0}),
			Source:   "atmos-best-practice",
		})
	}

	// Check 3: Duplicate component names in the same file
	compSeen := make(map[string]index.CompNode)
	for _, comp := range file.Comps {
		if prev, ok := compSeen[comp.Name]; ok {
			diags = append(diags, diagnostic{
				Severity: SeverityError,
				Message:  fmt.Sprintf("Duplicate component name '%s' in this file", comp.Name),
				Range:    toLSPRange(comp.Range),
				Source:   "atmos-component",
			})
			_ = prev
		} else {
			compSeen[comp.Name] = comp
		}
	}

	// Check 4: Component name must match Atmos pattern
	for _, comp := range file.Comps {
		if !componentNamePattern.MatchString(comp.Name) {
			diags = append(diags, diagnostic{
				Severity: SeverityError,
				Message:  fmt.Sprintf("Component name '%s' contains invalid characters (must match ^[/a-zA-Z0-9-_{}. ]+$)", comp.Name),
				Range:    toLSPRange(comp.Range),
				Source:   "atmos-component",
			})
		}
	}

	// Check 5: Metadata.type must be "abstract" or "real"
	for _, meta := range file.Metadata {
		if meta.Type != "" && meta.Type != "abstract" && meta.Type != "real" {
			diags = append(diags, diagnostic{
				Severity: SeverityError,
				Message:  fmt.Sprintf("metadata.type must be 'abstract' or 'real', got '%s'", meta.Type),
				Range:    toLSPRange(meta.Range),
				Source:   "atmos-schema",
			})
		}
	}

	// Check 6: Backend type enum validation
	for _, bt := range file.BackendTypes {
		if !validBackendTypes[bt.Type] {
			kind := bt.Kind
			diags = append(diags, diagnostic{
				Severity: SeverityError,
				Message:  fmt.Sprintf("Invalid %s '%s' (must be one of: local, s3, remote, vault, static, azurerm, gcs, cloud)", kind, bt.Type),
				Range:    toLSPRange(bt.Range),
				Source:   "atmos-schema",
			})
		}
	}

	// Check 7: settings.depends_on component existence
	for _, sd := range file.SettingsDeps {
		if sd.Component == "" {
			continue
		}
		refs := idx.FindComponent(sd.Component)
		if len(refs) == 0 {
			diags = append(diags, diagnostic{
				Severity: SeverityError,
				Message:  fmt.Sprintf("settings.depends_on references component '%s' which was not found", sd.Component),
				Range:    toLSPRange(sd.Range),
				Source:   "atmos-deps",
			})
		}
	}

	// Check 8: Unresolvable imports
	for _, imp := range file.Imports {
		resolved := idx.ResolveImport(imp.RawPath, dir)
		if len(resolved) == 0 {
			log.Printf("diagnostics: import '%s' from dir='%s' unresolved (basePath=%s)", imp.RawPath, dir, idx.BasePath())
			diags = append(diags, diagnostic{
				Severity: SeverityError,
				Message:  fmt.Sprintf("Import '%s' cannot be resolved", imp.RawPath),
				Range:    toLSPRange(imp.Range),
				Source:   "atmos-import",
			})
		} else {
			log.Printf("diagnostics: import '%s' resolved to %v", imp.RawPath, resolved)
		}
	}

	// Check 9: Import order — check for overrides imported before base
	if len(file.Imports) > 1 {
		for i, imp := range file.Imports {
			resolvedPath := imp.RawPath
			if strings.Contains(resolvedPath, "override") && i == 0 {
				diags = append(diags, diagnostic{
					Severity: SeverityHint,
					Message:  "Base imports should come first; later imports override earlier",
					Range:    toLSPRange(imp.Range),
					Source:   "atmos-best-practice",
				})
			}
		}
	}

	// Check 10: Unquoted version values
	for _, v := range file.Vars {
		if strings.Contains(strings.ToLower(v.Key), "version") && !v.IsQuoted {
			diags = append(diags, diagnostic{
				Severity: SeverityWarning,
				Message:  fmt.Sprintf("Version '%s' should be quoted to prevent YAML float parsing", v.Value),
				Range:    toLSPRange(v.Range),
				Source:   "atmos-version",
			})
		}
	}

	// Check 11: Component not in catalog directory
	for _, comp := range file.Comps {
		if !strings.Contains(dir, "catalog") {
			diags = append(diags, diagnostic{
				Severity: SeverityHint,
				Message:  "Consider using a catalog (" + filepath.Join(idx.BasePath(), "catalog") + ") for reusable component blueprints",
				Range:    toLSPRange(comp.Range),
				Source:   "atmos-best-practice",
			})
		}
	}

	// Check 12: Abstract component deployability warning
	for _, meta := range file.Metadata {
		if meta.Type == "abstract" && meta.Component != "" {
			inheritors := idx.FindInheritors(meta.Component)
			if len(inheritors) == 0 {
				diags = append(diags, diagnostic{
					Severity: SeverityWarning,
					Message:  fmt.Sprintf("Abstract component '%s' has no inheritors", meta.Component),
					Range:    toLSPRange(meta.Range),
					Source:   "atmos-abstract",
				})
			}
		}
	}

	// Check 13: Validate dependencies.components
	for _, dep := range file.Deps {
		if dep.Component == "" {
			continue
		}
		refs := idx.FindComponent(dep.Component)
		if len(refs) == 0 {
			diags = append(diags, diagnostic{
				Severity: SeverityError,
				Message:  fmt.Sprintf("Dependency component '%s' not found", dep.Component),
				Range:    toLSPRange(dep.Range),
				Source:   "atmos-deps",
			})
		}
	}

	// Check 14: Validate !terraform.state references match declared dependencies
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
			diags = append(diags, diagnostic{
				Severity: SeverityWarning,
				Message:  fmt.Sprintf("!terraform.state references '%s' but it is not declared in dependencies.components", ts.Component),
				Range:    toLSPRange(ts.Range),
				Source:   "atmos-deps",
			})
		}
	}

	// Check 15: Unknown template variables in template expressions
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
					diags = append(diags, diagnostic{
						Severity: SeverityError,
						Message:  fmt.Sprintf("Unknown template variable '.vars.%s'", key),
						Range:    toLSPRange(v.Range),
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
					diags = append(diags, diagnostic{
						Severity: SeverityError,
						Message:  fmt.Sprintf("Unknown template variable '.%s'", key),
						Range:    toLSPRange(v.Range),
						Source:   "atmos-template",
					})
				}
			}
		}
	}

	// Check 16: Duplicate imports
	importSeen := make(map[string]index.ImportNode)
	for _, imp := range file.Imports {
		if prev, ok := importSeen[imp.RawPath]; ok {
			diags = append(diags, diagnostic{
				Severity: SeverityWarning,
				Message:  fmt.Sprintf("Duplicate import '%s'", imp.RawPath),
				Range:    toLSPRange(imp.Range),
				Source:   "atmos-import",
			})
			_ = prev
		} else {
			importSeen[imp.RawPath] = imp
		}
	}

	// Check 11: Circular imports
	if found, deep := hasCircularImport(file.Path, idx, nil, 0); found {
		msg := "Circular import detected in this stack file"
		if deep {
			msg = "Import chain exceeds maximum depth (50); verify there are no circular imports"
		}
		diags = append(diags, diagnostic{
			Severity: SeverityError,
			Message:  msg,
			Range:    toLSPRange(index.Range{StartLine: 0, StartChar: 0, EndLine: 0, EndChar: 0}),
			Source:   "atmos-import",
		})
	}

	return diags
}

const maxImportDepth = 50

func hasCircularImport(path string, idx *index.Index, visited map[string]struct{}, depth int) (bool, bool) {
	if depth > maxImportDepth {
		return true, true // true = exceeded depth, true = deep chain (not necessarily circular)
	}
	if visited == nil {
		visited = make(map[string]struct{})
	}
	if _, ok := visited[path]; ok {
		return true, false // true = circular, false = actual cycle
	}
	f := idx.GetFileUnsafe(path)
	if f == nil {
		return false, false
	}
	visited[path] = struct{}{}
	for _, imp := range f.Imports {
		resolved := idx.ResolveImport(imp.RawPath, filepath.Dir(path))
		for _, r := range resolved {
			if found, deep := hasCircularImport(r, idx, visited, depth+1); found {
				return true, deep
			}
		}
	}
	delete(visited, path)
	return false, false
}
