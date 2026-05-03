package index

import (
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
					debounce.Reset(200 * time.Millisecond)
				}
			case <-debounce.C:
				idx.Reindex()
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
	entries, err := os.ReadDir(idx.BasePath)
	if err != nil {
		return
	}
	idx.mu.Lock()
	defer idx.mu.Unlock()
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".yaml") && !strings.HasSuffix(e.Name(), ".yml") {
			continue
		}
		fullPath := filepath.Join(idx.BasePath, e.Name())
		if _, exists := idx.Files[fullPath]; !exists {
			idx.Files[fullPath] = &StackFile{Path: fullPath}
		}
	}
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
