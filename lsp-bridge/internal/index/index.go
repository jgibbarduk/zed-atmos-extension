package index

import (
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

const (
	// watcherDebounce is the delay after the last file-change event before
	// triggering a full reindex. 200 ms coalesces rapid saves without
	// introducing noticeable latency.
	watcherDebounce = 200 * time.Millisecond
)

type Range struct {
	StartLine uint32 `json:"startLine"`
	StartChar uint32 `json:"startChar"`
	EndLine   uint32 `json:"endLine"`
	EndChar   uint32 `json:"endChar"`
}

type ImportNode struct {
	RawPath  string   `json:"rawPath"`
	Path     string   `json:"path,omitempty"`     // Resolved path for object-style imports
	Range    Range    `json:"range"`
	Resolves []string `json:"resolves"`
}

type CompNode struct {
	Name  string `json:"name"`
	Range Range  `json:"range"`
}

type MetadataNode struct {
	Component      string `json:"component,omitempty"`
	ComponentRange Range  `json:"component_range,omitempty"`
	Inherits       string `json:"inherits,omitempty"`
	InheritsRange  Range  `json:"inherits_range,omitempty"`
	Type           string `json:"type,omitempty"`
	Name           string `json:"name,omitempty"`
	NameRange      Range  `json:"name_range,omitempty"`
	Range          Range  `json:"range"`
}

type VarNode struct {
	Key       string `json:"key"`
	Value     string `json:"value"`
	Range     Range  `json:"range"`
	IsQuoted  bool   `json:"is_quoted"`
	Component string `json:"component,omitempty"`
}

type DepNode struct {
	Component string `json:"component"`
	Range     Range  `json:"range"`
}

type TerraformStateRef struct {
	Component string `json:"component"`
	JQExpr    string `json:"jq_expr"`
	Range     Range  `json:"range"`
}

type BackendTypeNode struct {
	Component string `json:"component"`
	Type      string `json:"type"`
	Kind      string `json:"kind"`
	Range     Range  `json:"range"`
}

type SettingsDependsOnNode struct {
	Key       string `json:"key"`
	Component string `json:"component"`
	Range     Range  `json:"range"`
}

type YAMLTagNode struct {
	Tag       string `json:"tag"`
	Value     string `json:"value"`
	Key       string `json:"key,omitempty"`
	Component string `json:"component,omitempty"`
	Range     Range  `json:"range"`
}

type StackFile struct {
	Path           string                  `json:"path"`
	Imports        []ImportNode            `json:"imports"`
	Comps          []CompNode              `json:"comps"`
	Metadata       []MetadataNode          `json:"metadata"`
	Vars           []VarNode               `json:"vars"`
	TerraformVars  []VarNode               `json:"terraform_vars,omitempty"`  // root-level terraform.vars
	HelmfileVars   []VarNode               `json:"helmfile_vars,omitempty"`   // root-level helmfile.vars
	OverridesVars  []VarNode               `json:"overrides_vars,omitempty"`  // overrides.vars
	Deps           []DepNode               `json:"deps"`
	TerraformState []TerraformStateRef     `json:"terraform_state"`
	BackendTypes   []BackendTypeNode       `json:"backend_types"`
	SettingsDeps   []SettingsDependsOnNode `json:"settings_deps"`
	YAMLTags       []YAMLTagNode           `json:"yaml_tags,omitempty"`
	ParseError     string                  `json:"-"` // YAML parse error, if any
}

type Index struct {
	mu          sync.RWMutex
	files       map[string]*StackFile
	byImport    map[string][]string
	byComponent map[string][]string
	byInherit   map[string][]string
	basePath    string
	watcher     *fsnotify.Watcher
	onChange    func()
}

func New(basePath string) (*Index, error) {
	return &Index{
		files:       make(map[string]*StackFile),
		byImport:    make(map[string][]string),
		byComponent: make(map[string][]string),
		byInherit:   make(map[string][]string),
		basePath:    basePath,
	}, nil
}

func (idx *Index) StartWatching(onChange func()) error {
	if idx.watcher != nil {
		return nil
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	idx.watcher = w
	idx.onChange = onChange
	cb := onChange

	go func() {
		debounce := time.NewTimer(0)
		<-debounce.C

		for {
			select {
			case event, ok := <-w.Events:
				if !ok {
					return
				}
				if strings.HasSuffix(event.Name, ".yaml") || strings.HasSuffix(event.Name, ".yml") {
					if event.Op&(fsnotify.Write|fsnotify.Create) != 0 {
						idx.ReindexFile(event.Name)
					} else if event.Op&(fsnotify.Remove|fsnotify.Rename) != 0 {
						idx.RemoveFile(event.Name)
					}
					if !debounce.Stop() {
						select {
						case <-debounce.C:
						default:
						}
					}
					debounce.Reset(watcherDebounce)
				} else if event.Op&fsnotify.Create != 0 {
					// A new directory was created — add it to the watcher so
					// files created inside it are tracked.
					if fi, err := os.Stat(event.Name); err == nil && fi.IsDir() {
						if err := w.Add(event.Name); err != nil {
							log.Printf("watcher: failed to add new directory %s: %v", event.Name, err)
						}
					}
				}
			case <-debounce.C:
				if cb != nil {
					cb()
				}
			case err, ok := <-w.Errors:
				if !ok {
					return
				}
				_ = err
			}
		}
	}()

	err = filepath.WalkDir(idx.basePath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			log.Printf("watcher: error accessing %s: %v", path, err)
			return nil
		}
		if d.IsDir() {
			return w.Add(path)
		}
		return nil
	})
	if err != nil {
		log.Printf("watcher: WalkDir failed: %v", err)
	}
	return nil
}

func (idx *Index) Reindex() {
	if idx.basePath == "" {
		return
	}

	// Build new maps outside the lock so filesystem I/O doesn't block readers.
	files := make(map[string]*StackFile)
	byImport := make(map[string][]string)
	byComponent := make(map[string][]string)
	byInherit := make(map[string][]string)

	err := filepath.WalkDir(idx.basePath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			log.Printf("reindex: error accessing %s: %v", path, err)
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".yaml") && !strings.HasSuffix(path, ".yml") {
			return nil
		}

		sf, parseErr := parseYAMLFile(path)
		if parseErr != nil {
			log.Printf("parse %s: %v", path, parseErr)
		}
		if sf == nil {
			sf = &StackFile{Path: path}
		}
		files[path] = sf
		for _, imp := range sf.Imports {
			byImport[imp.RawPath] = append(byImport[imp.RawPath], path)
		}
		for _, comp := range sf.Comps {
			byComponent[comp.Name] = append(byComponent[comp.Name], path)
		}
		for _, meta := range sf.Metadata {
			if meta.Component != "" {
				byComponent[meta.Component] = append(byComponent[meta.Component], path)
			}
			if meta.Inherits != "" {
				byInherit[meta.Inherits] = append(byInherit[meta.Inherits], path)
			}
		}
		return nil
	})
	if err != nil {
		log.Printf("reindex: WalkDir failed: %v", err)
	}

	idx.mu.Lock()
	defer idx.mu.Unlock()
	idx.files = files
	idx.byImport = byImport
	idx.byComponent = byComponent
	idx.byInherit = byInherit
}

func deepCopyStackFile(sf *StackFile) *StackFile {
	if sf == nil {
		return nil
	}
	out := &StackFile{
		Path:       sf.Path,
		ParseError: sf.ParseError,
	}
	if len(sf.Imports) > 0 {
		out.Imports = make([]ImportNode, len(sf.Imports))
		for i, imp := range sf.Imports {
			out.Imports[i] = ImportNode{
				RawPath:  imp.RawPath,
				Path:     imp.Path,
				Range:    imp.Range,
				Resolves: append([]string(nil), imp.Resolves...),
			}
		}
	}
	if len(sf.Comps) > 0 {
		out.Comps = make([]CompNode, len(sf.Comps))
		copy(out.Comps, sf.Comps)
	}
	if len(sf.Metadata) > 0 {
		out.Metadata = make([]MetadataNode, len(sf.Metadata))
		copy(out.Metadata, sf.Metadata)
	}
	if len(sf.Vars) > 0 {
		out.Vars = make([]VarNode, len(sf.Vars))
		copy(out.Vars, sf.Vars)
	}
	if len(sf.Deps) > 0 {
		out.Deps = make([]DepNode, len(sf.Deps))
		copy(out.Deps, sf.Deps)
	}
	if len(sf.TerraformState) > 0 {
		out.TerraformState = make([]TerraformStateRef, len(sf.TerraformState))
		copy(out.TerraformState, sf.TerraformState)
	}
	if len(sf.BackendTypes) > 0 {
		out.BackendTypes = make([]BackendTypeNode, len(sf.BackendTypes))
		copy(out.BackendTypes, sf.BackendTypes)
	}
	if len(sf.SettingsDeps) > 0 {
		out.SettingsDeps = make([]SettingsDependsOnNode, len(sf.SettingsDeps))
		copy(out.SettingsDeps, sf.SettingsDeps)
	}
	if len(sf.TerraformVars) > 0 {
		out.TerraformVars = make([]VarNode, len(sf.TerraformVars))
		copy(out.TerraformVars, sf.TerraformVars)
	}
	if len(sf.HelmfileVars) > 0 {
		out.HelmfileVars = make([]VarNode, len(sf.HelmfileVars))
		copy(out.HelmfileVars, sf.HelmfileVars)
	}
	if len(sf.OverridesVars) > 0 {
		out.OverridesVars = make([]VarNode, len(sf.OverridesVars))
		copy(out.OverridesVars, sf.OverridesVars)
	}
	if len(sf.YAMLTags) > 0 {
		out.YAMLTags = make([]YAMLTagNode, len(sf.YAMLTags))
		copy(out.YAMLTags, sf.YAMLTags)
	}
	return out
}

// GetFile returns a deep copy of the StackFile for the given path.
// Callers may safely modify the returned value.
func (idx *Index) GetFile(path string) *StackFile {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return deepCopyStackFile(idx.files[path])
}

// GetFileUnsafe returns the StackFile directly without copying.
// The returned pointer MUST NOT be modified. Use only for read-only access.
func (idx *Index) GetFileUnsafe(path string) *StackFile {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return idx.files[path]
}

func (idx *Index) FindComponent(name string) []StackFile {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	paths := idx.byComponent[name]
	results := make([]StackFile, 0, len(paths))
	for _, p := range paths {
		if f, ok := idx.files[p]; ok {
			results = append(results, *deepCopyStackFile(f))
		}
	}
	return results
}

func (idx *Index) ResolveImport(rawPath string, fromDir string) []string {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	// Strip any existing extension so we don't double-append.
	cleanPath := strings.TrimSuffix(rawPath, ".yaml")
	cleanPath = strings.TrimSuffix(cleanPath, ".yml")

	candidates := []string{
		filepath.Join(idx.basePath, cleanPath+".yaml"),
		filepath.Join(idx.basePath, cleanPath+".yml"),
		filepath.Join(fromDir, cleanPath+".yaml"),
		filepath.Join(fromDir, cleanPath+".yml"),
	}

	var resolved []string
	for _, c := range candidates {
		if _, ok := idx.files[c]; ok {
			resolved = append(resolved, c)
		}
	}
	return resolved
}

func (idx *Index) FindImporters(rawPath string) []string {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	paths := idx.byImport[rawPath]
	out := make([]string, len(paths))
	copy(out, paths)
	return out
}

func (idx *Index) FindInheritors(name string) []string {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	paths := idx.byInherit[name]
	out := make([]string, len(paths))
	copy(out, paths)
	return out
}

func removePath(slice []string, target string) ([]string, bool) {
	for i, p := range slice {
		if p == target {
			return append(slice[:i], slice[i+1:]...), true
		}
	}
	return slice, false
}

// removeFromIndexes removes all references to `path` from the reverse indexes
// using the entries in `old`. Callers must hold idx.mu.
func (idx *Index) removeFromIndexes(path string, old *StackFile) {
	if old == nil {
		return
	}
	for _, o := range old.Imports {
		if updated, ok := removePath(idx.byImport[o.RawPath], path); ok {
			if len(updated) == 0 {
				delete(idx.byImport, o.RawPath)
			} else {
				idx.byImport[o.RawPath] = updated
			}
		}
	}
	for _, o := range old.Comps {
		if updated, ok := removePath(idx.byComponent[o.Name], path); ok {
			if len(updated) == 0 {
				delete(idx.byComponent, o.Name)
			} else {
				idx.byComponent[o.Name] = updated
			}
		}
	}
	for _, o := range old.Metadata {
		if o.Component != "" {
			if updated, ok := removePath(idx.byComponent[o.Component], path); ok {
				if len(updated) == 0 {
					delete(idx.byComponent, o.Component)
				} else {
					idx.byComponent[o.Component] = updated
				}
			}
		}
		if o.Inherits != "" {
			if updated, ok := removePath(idx.byInherit[o.Inherits], path); ok {
				if len(updated) == 0 {
					delete(idx.byInherit, o.Inherits)
				} else {
					idx.byInherit[o.Inherits] = updated
				}
			}
		}
	}
}

func (idx *Index) ReindexFile(path string) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	oldFile, existed := idx.files[path]

	sf, err := parseYAMLFile(path)
	if err != nil {
		log.Printf("parse %s: %v", path, err)
	}
	if sf == nil {
		sf = &StackFile{Path: path}
	}

	if existed {
		idx.removeFromIndexes(path, oldFile)
	}

	idx.files[path] = sf
	for _, imp := range sf.Imports {
		idx.byImport[imp.RawPath] = append(idx.byImport[imp.RawPath], path)
	}
	for _, comp := range sf.Comps {
		idx.byComponent[comp.Name] = append(idx.byComponent[comp.Name], path)
	}
	for _, meta := range sf.Metadata {
		if meta.Component != "" {
			idx.byComponent[meta.Component] = append(idx.byComponent[meta.Component], path)
		}
		if meta.Inherits != "" {
			idx.byInherit[meta.Inherits] = append(idx.byInherit[meta.Inherits], path)
		}
	}
}

func (idx *Index) RemoveFile(path string) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	oldFile, existed := idx.files[path]
	if !existed || oldFile == nil {
		delete(idx.files, path)
		return
	}

	idx.removeFromIndexes(path, oldFile)
	delete(idx.files, path)
}

func (idx *Index) UpsertFile(path string, sf *StackFile) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	if sf == nil {
		sf = &StackFile{Path: path}
	}

	oldFile, existed := idx.files[path]
	if existed {
		idx.removeFromIndexes(path, oldFile)
	}

	idx.files[path] = sf
	for _, imp := range sf.Imports {
		idx.byImport[imp.RawPath] = append(idx.byImport[imp.RawPath], path)
	}
	for _, comp := range sf.Comps {
		idx.byComponent[comp.Name] = append(idx.byComponent[comp.Name], path)
	}
	for _, meta := range sf.Metadata {
		if meta.Component != "" {
			idx.byComponent[meta.Component] = append(idx.byComponent[meta.Component], path)
		}
		if meta.Inherits != "" {
			idx.byInherit[meta.Inherits] = append(idx.byInherit[meta.Inherits], path)
		}
	}
}

func (idx *Index) SetBasePath(path string) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	idx.basePath = path
}

func (idx *Index) BasePath() string {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return idx.basePath
}

func (idx *Index) AllFiles() []*StackFile {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	result := make([]*StackFile, 0, len(idx.files))
	for _, f := range idx.files {
		result = append(result, deepCopyStackFile(f))
	}
	return result
}

func (idx *Index) Close() {
	if idx.watcher != nil {
		idx.watcher.Close()
	}
}
