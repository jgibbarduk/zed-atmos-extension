package handler

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/jgibbarduk/zed-atmos-extension/lsp-bridge/internal/index"
	"github.com/jgibbarduk/zed-atmos-extension/lsp-bridge/internal/lsp"
)

var componentNamePattern = regexp.MustCompile(`^[/a-zA-Z0-9-_{}. ]+$`)

var validBackendTypes = map[string]bool{
	"local": true, "s3": true, "remote": true, "vault": true,
	"static": true, "azurerm": true, "gcs": true, "cloud": true,
}

func validBackendTypeList() []string {
	var list []string
	for k := range validBackendTypes {
		list = append(list, k)
	}
	sort.Strings(list)
	return list
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

// Checker is a function that inspects a StackFile and returns diagnostics.
type Checker func(file *index.StackFile, dir string, idx *index.Index) []diagnostic

// checkers is the ordered list of all diagnostic rules.
var checkers = []Checker{
	checkParseError,
	checkDefaultsAncestor,
	checkEmptyImports,
	checkDuplicateComponents,
	checkComponentNamePattern,
	checkMetadataType,
	checkMetadataComponentDir,
	checkMetadataInherits,
	checkBackendTypeEnum,
	checkSettingsDependsOn,
	checkUnresolvableImports,
	checkImportOrder,
	checkUnquotedVersion,
	checkCatalogDirectory,
	checkAbstractInheritors,
	checkDependenciesComponents,
	checkTerraformStateDeps,
	checkUnknownTemplateVars,
	checkDuplicateImports,
	checkCircularImports,
}

func runBestPracticeChecks(file *index.StackFile, dir string, idx *index.Index) []diagnostic {
	var diags []diagnostic
	for _, checker := range checkers {
		diags = append(diags, checker(file, dir, idx)...)
	}
	return diags
}

func checkParseError(file *index.StackFile, dir string, idx *index.Index) []diagnostic {
	if file.ParseError == "" {
		return nil
	}
	var lineNum int
	var msg string
	if n, _ := fmt.Sscanf(file.ParseError, "yaml: line %d:", &lineNum); n == 1 {
		msg = fmt.Sprintf("YAML syntax error: %s", file.ParseError)
		lineNum--
		if lineNum < 0 {
			lineNum = 0
		}
	} else {
		msg = fmt.Sprintf("YAML syntax error: %s", file.ParseError)
	}
	line := uint32(lineNum)
	return []diagnostic{{
		Severity: SeverityError,
		Message:  msg,
		Range:    toLSPRange(index.Range{StartLine: line, StartChar: 0, EndLine: line, EndChar: 0}),
		Source:   "atmos-yaml",
	}}
}

func checkDefaultsAncestor(file *index.StackFile, dir string, idx *index.Index) []diagnostic {
	filename := filepath.Base(file.Path)
	defaultsPath := filepath.Join(dir, "_defaults.yaml")
	if idx.GetFileUnsafe(defaultsPath) != nil || filename == "_defaults.yaml" {
		return nil
	}
	parentDir := filepath.Dir(dir)
	parentDefaults := filepath.Join(parentDir, "_defaults.yaml")
	if idx.GetFileUnsafe(parentDefaults) != nil {
		return nil
	}
	return []diagnostic{{
		Severity: SeverityHint,
		Message:  "Consider adding a `_defaults.yaml` at this level for shared settings",
		Range:    toLSPRange(index.Range{StartLine: 0, StartChar: 0, EndLine: 0, EndChar: 0}),
		Source:   "atmos-best-practice",
	}}
}

func checkEmptyImports(file *index.StackFile, dir string, idx *index.Index) []diagnostic {
	if len(file.Imports) > 0 {
		return nil
	}
	return []diagnostic{{
		Severity: SeverityHint,
		Message:  "No imports defined — consider importing base settings",
		Range:    toLSPRange(index.Range{StartLine: 0, StartChar: 0, EndLine: 0, EndChar: 0}),
		Source:   "atmos-best-practice",
	}}
}

func checkDuplicateComponents(file *index.StackFile, dir string, idx *index.Index) []diagnostic {
	var diags []diagnostic
	seen := make(map[string]bool)
	for _, comp := range file.Comps {
		if seen[comp.Name] {
			diags = append(diags, diagnostic{
				Severity: SeverityError,
				Message:  fmt.Sprintf("Duplicate component name '%s' in this file", comp.Name),
				Range:    toLSPRange(comp.Range),
				Source:   "atmos-component",
			})
		} else {
			seen[comp.Name] = true
		}
	}
	return diags
}

func checkComponentNamePattern(file *index.StackFile, dir string, idx *index.Index) []diagnostic {
	var diags []diagnostic
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
	return diags
}

func checkMetadataType(file *index.StackFile, dir string, idx *index.Index) []diagnostic {
	var diags []diagnostic
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
	return diags
}

func checkMetadataComponentDir(file *index.StackFile, dir string, idx *index.Index) []diagnostic {
	var diags []diagnostic
	componentsBase := filepath.Join(idx.BasePath(), "components")
	cleanBase, baseErr := filepath.Abs(componentsBase)
	if baseErr != nil {
		return diags
	}
	for _, meta := range file.Metadata {
		if meta.Component == "" {
			continue
		}
		compDir := filepath.Join(componentsBase, meta.Component)
		cleanComp, compErr := filepath.Abs(compDir)
		if compErr != nil {
			log.Printf("checkMetadataComponentDir: filepath.Abs(%s) failed: %v", compDir, compErr)
			continue
		}
		if cleanComp != cleanBase && !strings.HasPrefix(cleanComp, cleanBase+string(filepath.Separator)) {
			diags = append(diags, diagnostic{
				Severity: SeverityError,
				Message:  fmt.Sprintf("metadata.component '%s' contains invalid path traversal characters", meta.Component),
				Range:    toLSPRange(meta.ComponentRange),
				Source:   "atmos-component",
			})
			continue
		}
		if _, err := os.Stat(compDir); os.IsNotExist(err) {
			diags = append(diags, diagnostic{
				Severity: SeverityError,
				Message:  fmt.Sprintf("metadata.component '%s' does not exist at %s", meta.Component, compDir),
				Range:    toLSPRange(meta.ComponentRange),
				Source:   "atmos-component",
			})
		}
	}
	return diags
}

func checkMetadataInherits(file *index.StackFile, dir string, idx *index.Index) []diagnostic {
	var diags []diagnostic
	for _, meta := range file.Metadata {
		if meta.Inherits != "" {
			refs := idx.FindComponent(meta.Inherits)
			if len(refs) == 0 {
				diags = append(diags, diagnostic{
					Severity: SeverityWarning,
					Message:  fmt.Sprintf("Inherited component '%s' not found", meta.Inherits),
					Range:    toLSPRange(meta.InheritsRange),
					Source:   "atmos-inherit",
				})
			}
		}
	}
	return diags
}

func checkBackendTypeEnum(file *index.StackFile, dir string, idx *index.Index) []diagnostic {
	var diags []diagnostic
	for _, bt := range file.BackendTypes {
		if !validBackendTypes[bt.Type] {
			diags = append(diags, diagnostic{
				Severity: SeverityError,
				Message:  fmt.Sprintf("Invalid %s '%s' (must be one of: %s)", bt.Kind, bt.Type, strings.Join(validBackendTypeList(), ", ")),
				Range:    toLSPRange(bt.Range),
				Source:   "atmos-schema",
			})
		}
	}
	return diags
}

func checkSettingsDependsOn(file *index.StackFile, dir string, idx *index.Index) []diagnostic {
	var diags []diagnostic
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
	return diags
}

func checkUnresolvableImports(file *index.StackFile, dir string, idx *index.Index) []diagnostic {
	var diags []diagnostic
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
	return diags
}

func checkImportOrder(file *index.StackFile, dir string, idx *index.Index) []diagnostic {
	if len(file.Imports) <= 1 {
		return nil
	}
	var diags []diagnostic
	for i, imp := range file.Imports {
		if strings.Contains(imp.RawPath, "override") && i == 0 {
			diags = append(diags, diagnostic{
				Severity: SeverityHint,
				Message:  "Base imports should come first; later imports override earlier",
				Range:    toLSPRange(imp.Range),
				Source:   "atmos-best-practice",
			})
		}
	}
	return diags
}

func checkUnquotedVersion(file *index.StackFile, dir string, idx *index.Index) []diagnostic {
	var diags []diagnostic
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
	return diags
}

func checkCatalogDirectory(file *index.StackFile, dir string, idx *index.Index) []diagnostic {
	if filepath.Base(dir) == "catalog" || strings.HasSuffix(filepath.Base(dir), "-catalog") {
		return nil
	}
	var diags []diagnostic
	for _, comp := range file.Comps {
		diags = append(diags, diagnostic{
			Severity: SeverityHint,
			Message:  "Consider using a catalog (" + filepath.Join(idx.BasePath(), "catalog") + ") for reusable component blueprints",
			Range:    toLSPRange(comp.Range),
			Source:   "atmos-best-practice",
		})
	}
	return diags
}

func checkAbstractInheritors(file *index.StackFile, dir string, idx *index.Index) []diagnostic {
	var diags []diagnostic
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
	return diags
}

func checkDependenciesComponents(file *index.StackFile, dir string, idx *index.Index) []diagnostic {
	var diags []diagnostic
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
	return diags
}

func checkTerraformStateDeps(file *index.StackFile, dir string, idx *index.Index) []diagnostic {
	var diags []diagnostic
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
			for _, dep := range file.SettingsDeps {
				if dep.Component == ts.Component {
					found = true
					break
				}
			}
		}
		if !found {
			diags = append(diags, diagnostic{
				Severity: SeverityWarning,
				Message:  fmt.Sprintf("!terraform.state references '%s' but it is not declared in dependencies.components or settings.depends_on", ts.Component),
				Range:    toLSPRange(ts.Range),
				Source:   "atmos-deps",
			})
		}
	}
	return diags
}

func checkUnknownTemplateVars(file *index.StackFile, dir string, idx *index.Index) []diagnostic {
	vars := collectVars(file, idx)
	var diags []diagnostic
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
	return diags
}

func checkDuplicateImports(file *index.StackFile, dir string, idx *index.Index) []diagnostic {
	var diags []diagnostic
	seen := make(map[string]bool)
	for _, imp := range file.Imports {
		if seen[imp.RawPath] {
			diags = append(diags, diagnostic{
				Severity: SeverityWarning,
				Message:  fmt.Sprintf("Duplicate import '%s'", imp.RawPath),
				Range:    toLSPRange(imp.Range),
				Source:   "atmos-import",
			})
		} else {
			seen[imp.RawPath] = true
		}
	}
	return diags
}

func checkCircularImports(file *index.StackFile, dir string, idx *index.Index) []diagnostic {
	if found, deep := hasCircularImport(file.Path, idx, nil, 0); found {
		msg := "Circular import detected in this stack file"
		if deep {
			msg = "Import chain exceeds maximum depth (50); verify there are no circular imports"
		}
		return []diagnostic{{
			Severity: SeverityError,
			Message:  msg,
			Range:    toLSPRange(index.Range{StartLine: 0, StartChar: 0, EndLine: 0, EndChar: 0}),
			Source:   "atmos-import",
		}}
	}
	return nil
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
