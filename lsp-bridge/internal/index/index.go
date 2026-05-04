package index

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

type Range struct {
	StartLine uint32 `json:"startLine"`
	StartChar uint32 `json:"startChar"`
	EndLine   uint32 `json:"endLine"`
	EndChar   uint32 `json:"endChar"`
}

type ImportNode struct {
	RawPath   string   `json:"rawPath"`
	Range     Range    `json:"range"`
	Resolves  []string `json:"resolves"`
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

type StackFile struct {
	Path           string              `json:"path"`
	Imports        []ImportNode        `json:"imports"`
	Comps          []CompNode          `json:"comps"`
	Metadata       []MetadataNode      `json:"metadata"`
	Vars           []VarNode           `json:"vars"`
	Deps           []DepNode           `json:"deps"`
	TerraformState []TerraformStateRef `json:"terraform_state"`
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
					}
					debounce.Reset(200 * time.Millisecond)
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

	return filepath.Walk(idx.basePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			return w.Add(path)
		}
		return nil
	})
}

func (idx *Index) Reindex() {
	if idx.basePath == "" {
		return
	}
	idx.mu.Lock()
	defer idx.mu.Unlock()

	idx.files = make(map[string]*StackFile)
	idx.byImport = make(map[string][]string)
	idx.byComponent = make(map[string][]string)
	idx.byInherit = make(map[string][]string)

	filepath.Walk(idx.basePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
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
		return nil
	})
}

func deepCopyStackFile(sf *StackFile) *StackFile {
	if sf == nil {
		return nil
	}
	out := &StackFile{
		Path: sf.Path,
	}
	if len(sf.Imports) > 0 {
		out.Imports = make([]ImportNode, len(sf.Imports))
		for i, imp := range sf.Imports {
			out.Imports[i] = ImportNode{
				RawPath:  imp.RawPath,
				Range:   imp.Range,
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
	return out
}

func (idx *Index) GetFile(path string) *StackFile {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return deepCopyStackFile(idx.files[path])
}

func (idx *Index) FindComponent(name string) []StackFile {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	paths := idx.byComponent[name]
	var results []StackFile
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

	candidates := []string{
		filepath.Join(idx.basePath, rawPath+".yaml"),
		filepath.Join(idx.basePath, rawPath+".yml"),
		filepath.Join(fromDir, rawPath+".yaml"),
		filepath.Join(fromDir, rawPath+".yml"),
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
	if len(paths) == 0 {
		return nil
	}
	out := make([]string, len(paths))
	copy(out, paths)
	return out
}

func (idx *Index) FindInheritors(name string) []string {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	paths := idx.byInherit[name]
	if len(paths) == 0 {
		return nil
	}
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

	if existed && oldFile != nil {
		for _, old := range oldFile.Imports {
			if updated, ok := removePath(idx.byImport[old.RawPath], path); ok {
				if len(updated) == 0 {
					delete(idx.byImport, old.RawPath)
				} else {
					idx.byImport[old.RawPath] = updated
				}
			}
		}
		for _, old := range oldFile.Comps {
			if updated, ok := removePath(idx.byComponent[old.Name], path); ok {
				if len(updated) == 0 {
					delete(idx.byComponent, old.Name)
				} else {
					idx.byComponent[old.Name] = updated
				}
			}
		}
		for _, old := range oldFile.Metadata {
			if old.Component != "" {
				if updated, ok := removePath(idx.byComponent[old.Component], path); ok {
					if len(updated) == 0 {
						delete(idx.byComponent, old.Component)
					} else {
						idx.byComponent[old.Component] = updated
					}
				}
			}
			if old.Inherits != "" {
				if updated, ok := removePath(idx.byInherit[old.Inherits], path); ok {
					if len(updated) == 0 {
						delete(idx.byInherit, old.Inherits)
					} else {
						idx.byInherit[old.Inherits] = updated
					}
				}
			}
		}
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
