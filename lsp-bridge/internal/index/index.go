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

type StackFile struct {
	Path    string       `json:"path"`
	Imports []ImportNode `json:"imports"`
	Comps   []CompNode   `json:"comps"`
}

type Index struct {
	mu       sync.RWMutex
	Files    map[string]*StackFile
	ByImport map[string][]string
	BasePath string
	watcher  *fsnotify.Watcher
	onChange func()
}

func New(basePath string) (*Index, error) {
	return &Index{
		Files:    make(map[string]*StackFile),
		ByImport: make(map[string][]string),
		BasePath: basePath,
	}, nil
}

func (idx *Index) StartWatching(onChange func()) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	idx.watcher = w
	idx.onChange = onChange

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
				if idx.onChange != nil {
					idx.onChange()
				}
			case err, ok := <-w.Errors:
				if !ok {
					return
				}
				_ = err
			}
		}
	}()

	return filepath.Walk(idx.BasePath, func(path string, info os.FileInfo, err error) error {
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
	if idx.BasePath == "" {
		return
	}
	idx.mu.Lock()
	defer idx.mu.Unlock()

	idx.Files = make(map[string]*StackFile)
	idx.ByImport = make(map[string][]string)

	filepath.Walk(idx.BasePath, func(path string, info os.FileInfo, err error) error {
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
		idx.Files[path] = sf
		for _, imp := range sf.Imports {
			idx.ByImport[imp.RawPath] = append(idx.ByImport[imp.RawPath], path)
		}
		return nil
	})
}

func (idx *Index) GetFile(path string) *StackFile {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return idx.Files[path]
}

func (idx *Index) FindComponent(name string) []StackFile {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	var results []StackFile
	for _, f := range idx.Files {
		for _, c := range f.Comps {
			if c.Name == name {
				results = append(results, *f)
				break
			}
		}
	}
	return results
}

func (idx *Index) ResolveImport(rawPath string, fromDir string) []string {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	candidates := []string{
		filepath.Join(idx.BasePath, rawPath+".yaml"),
		filepath.Join(idx.BasePath, rawPath+".yml"),
		filepath.Join(fromDir, rawPath+".yaml"),
		filepath.Join(fromDir, rawPath+".yml"),
	}

	var resolved []string
	for _, c := range candidates {
		if _, ok := idx.Files[c]; ok {
			resolved = append(resolved, c)
		}
	}
	return resolved
}

func (idx *Index) FindImporters(rawPath string) []string {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return idx.ByImport[rawPath]
}

func (idx *Index) ReindexFile(path string) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	oldFile, existed := idx.Files[path]

	sf, err := parseYAMLFile(path)
	if err != nil {
		log.Printf("parse %s: %v", path, err)
	}
	if sf == nil {
		sf = &StackFile{Path: path}
	}

	if existed && oldFile != nil {
		for _, old := range oldFile.Imports {
			oldImporters := idx.ByImport[old.RawPath]
			for i, p := range oldImporters {
				if p == path {
					idx.ByImport[old.RawPath] = append(oldImporters[:i], oldImporters[i+1:]...)
					break
				}
			}
			if len(idx.ByImport[old.RawPath]) == 0 {
				delete(idx.ByImport, old.RawPath)
			}
		}
	}

	idx.Files[path] = sf
	for _, imp := range sf.Imports {
		idx.ByImport[imp.RawPath] = append(idx.ByImport[imp.RawPath], path)
	}
}

func (idx *Index) SetBasePath(path string) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	idx.BasePath = path
}

func (idx *Index) Close() {
	if idx.watcher != nil {
		idx.watcher.Close()
	}
}
