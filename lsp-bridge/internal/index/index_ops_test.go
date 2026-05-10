package index

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func TestNew(t *testing.T) {
	idx, err := New("/some/base")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if idx == nil {
		t.Fatal("expected non-nil Index")
	}
	if len(idx.files) != 0 {
		t.Errorf("expected empty files map, got %d entries", len(idx.files))
	}
	if len(idx.byImport) != 0 {
		t.Errorf("expected empty byImport map, got %d entries", len(idx.byImport))
	}
	if len(idx.byComponent) != 0 {
		t.Errorf("expected empty byComponent map, got %d entries", len(idx.byComponent))
	}
	if len(idx.byInherit) != 0 {
		t.Errorf("expected empty byInherit map, got %d entries", len(idx.byInherit))
	}
}

func TestResolveImport(t *testing.T) {
	dir := t.TempDir()
	basePath := dir

	// Create synthetic files on disk so ReindexFile can parse them.
	// We need real files because ReindexFile calls parseYAMLFile.
	paths := []string{
		filepath.Join(basePath, "globals.yaml"),
		filepath.Join(basePath, "mixins", "mixin.yaml"),
		filepath.Join(basePath, "stacks", "stack.yml"),
		filepath.Join(basePath, "stacks", "mixin.yaml"),
	}
	for _, p := range paths {
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(p, []byte("{}\n"), 0644); err != nil {
			t.Fatalf("write file: %v", err)
		}
	}

	idx, _ := New(basePath)
	for _, p := range paths {
		idx.ReindexFile(p)
	}

	fromDirNoMatch := filepath.Join(basePath, "nonexistent")

	tests := []struct {
		name      string
		rawPath   string
		fromDir   string
		want      []string
		wantEmpty bool
	}{
		{
			name:    "basePath yaml resolved",
			rawPath: "globals",
			fromDir: filepath.Join(basePath, "stacks"),
			want:    []string{filepath.Join(basePath, "globals.yaml")},
		},
		{
			name:    "basePath yml resolved",
			rawPath: "stacks/stack",
			fromDir: filepath.Join(basePath, "mixins"),
			want:    []string{filepath.Join(basePath, "stacks", "stack.yml")},
		},
		{
			name:    "fromDir resolved when basePath misses",
			rawPath: "mixin",
			fromDir: filepath.Join(basePath, "stacks"),
			want:    []string{filepath.Join(basePath, "stacks", "mixin.yaml")},
		},
		{
			name:      "no match returns empty",
			rawPath:   "missing",
			fromDir:   basePath,
			want:      []string{},
			wantEmpty: true,
		},
		{
			name:    "strip existing yaml extension (not double-appended)",
			rawPath: "globals.yaml",
			fromDir: fromDirNoMatch,
			want:    []string{filepath.Join(basePath, "globals.yaml")},
		},
		{
			name:    "strip existing yml extension",
			rawPath: "stacks/stack.yml",
			fromDir: fromDirNoMatch,
			want:    []string{filepath.Join(basePath, "stacks", "stack.yml")},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := idx.ResolveImport(tt.rawPath, tt.fromDir)
			if tt.wantEmpty {
				if len(got) != 0 {
					t.Fatalf("expected empty slice, got %v", got)
				}
				return
			}
			if len(got) != len(tt.want) {
				t.Fatalf("expected %v, got %v", tt.want, got)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Fatalf("expected %v, got %v", tt.want, got)
				}
			}
		})
	}
}

func TestFindImporters(t *testing.T) {
	idx, _ := New("")
	idx.UpsertFile("/a.yaml", &StackFile{
		Path:    "/a.yaml",
		Imports: []ImportNode{{RawPath: "globals"}},
	})
	idx.UpsertFile("/b.yaml", &StackFile{
		Path:    "/b.yaml",
		Imports: []ImportNode{{RawPath: "globals"}, {RawPath: "mixin"}},
	})

	tests := []struct {
		name    string
		rawPath string
		want    []string
	}{
		{
			name:    "import with importers",
			rawPath: "globals",
			want:    []string{"/a.yaml", "/b.yaml"},
		},
		{
			name:    "import with single importer",
			rawPath: "mixin",
			want:    []string{"/b.yaml"},
		},
		{
			name:    "no matches returns empty",
			rawPath: "missing",
			want:    []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := idx.FindImporters(tt.rawPath)
			sort.Strings(got)
			sort.Strings(tt.want)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("expected %v, got %v", tt.want, got)
			}
		})
	}
}

func TestFindInheritors(t *testing.T) {
	idx, _ := New("")
	idx.UpsertFile("/base.yaml", &StackFile{
		Path: "/base.yaml",
		Metadata: []MetadataNode{
			{Name: "base", Inherits: "parent"},
		},
	})
	idx.UpsertFile("/child.yaml", &StackFile{
		Path: "/child.yaml",
		Metadata: []MetadataNode{
			{Name: "child", Inherits: "parent"},
		},
	})
	idx.UpsertFile("/other.yaml", &StackFile{
		Path: "/other.yaml",
		Metadata: []MetadataNode{
			{Name: "other", Inherits: "ancestor"},
		},
	})

	tests := []struct {
		name string
		val  string
		want []string
	}{
		{
			name: "matches present",
			val:  "parent",
			want: []string{"/base.yaml", "/child.yaml"},
		},
		{
			name: "single match",
			val:  "ancestor",
			want: []string{"/other.yaml"},
		},
		{
			name: "no matches",
			val:  "missing",
			want: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := idx.FindInheritors(tt.val)
			sort.Strings(got)
			sort.Strings(tt.want)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("expected %v, got %v", tt.want, got)
			}
		})
	}
}

func TestUpsertFile(t *testing.T) {
	idx, _ := New("")

	// Initial insert
	idx.UpsertFile("/a.yaml", &StackFile{
		Path: "/a.yaml",
		Imports: []ImportNode{
			{RawPath: "globals"},
		},
		Comps: []CompNode{
			{Name: "db"},
		},
		Metadata: []MetadataNode{
			{Component: "vpc", Inherits: "base"},
		},
	})

	// Verify indexes populated
	if got := idx.FindImporters("globals"); len(got) != 1 || got[0] != "/a.yaml" {
		t.Fatalf("unexpected importers after insert: %v", got)
	}
	if got := idx.FindComponent("db"); len(got) != 1 {
		t.Fatalf("unexpected component count after insert: %d", len(got))
	}
	if got := idx.FindInheritors("base"); len(got) != 1 || got[0] != "/a.yaml" {
		t.Fatalf("unexpected inheritors after insert: %v", got)
	}

	// Upsert with nil → creates empty StackFile and cleans old indexes
	idx.UpsertFile("/a.yaml", nil)

	if got := idx.FindImporters("globals"); len(got) != 0 {
		t.Fatalf("expected importers cleaned up, got %v", got)
	}
	if got := idx.FindComponent("db"); len(got) != 0 {
		t.Fatalf("expected components cleaned up, got %d", len(got))
	}
	if got := idx.FindInheritors("base"); len(got) != 0 {
		t.Fatalf("expected inheritors cleaned up, got %v", got)
	}
	if sf := idx.GetFileUnsafe("/a.yaml"); sf == nil || len(sf.Imports) != 0 || len(sf.Comps) != 0 {
		t.Fatal("expected empty StackFile after nil upsert")
	}

	// Re-upsert with new data to verify full cleanup path
	idx.UpsertFile("/a.yaml", &StackFile{
		Path: "/a.yaml",
		Imports: []ImportNode{
			{RawPath: "other"},
		},
		Comps: []CompNode{
			{Name: "cache"},
		},
		Metadata: []MetadataNode{
			{Component: "eks", Inherits: "advanced"},
		},
	})

	if got := idx.FindImporters("other"); len(got) != 1 || got[0] != "/a.yaml" {
		t.Fatalf("unexpected importers after re-upsert: %v", got)
	}
	if got := idx.FindComponent("cache"); len(got) != 1 {
		t.Fatalf("unexpected component count after re-upsert: %d", len(got))
	}
	if got := idx.FindInheritors("advanced"); len(got) != 1 || got[0] != "/a.yaml" {
		t.Fatalf("unexpected inheritors after re-upsert: %v", got)
	}
}

func TestBasePath(t *testing.T) {
	idx, _ := New("")
	if got := idx.BasePath(); got != "" {
		t.Fatalf("expected empty default basePath, got %q", got)
	}

	idx.SetBasePath("/new/base")
	if got := idx.BasePath(); got != "/new/base" {
		t.Fatalf("expected basePath %q, got %q", "/new/base", got)
	}
}

func TestAllFiles(t *testing.T) {
	idx, _ := New("")
	if got := idx.AllFiles(); len(got) != 0 {
		t.Fatalf("expected empty slice for empty index, got %d", len(got))
	}

	idx.UpsertFile("/a.yaml", &StackFile{
		Path:    "/a.yaml",
		Imports: []ImportNode{{RawPath: "globals"}},
	})
	idx.UpsertFile("/b.yaml", &StackFile{
		Path: "/b.yaml",
		Comps: []CompNode{
			{Name: "db"},
		},
	})

	all := idx.AllFiles()
	if len(all) != 2 {
		t.Fatalf("expected 2 files, got %d", len(all))
	}

	// Verify deep copy by modifying returned slice
	for _, sf := range all {
		if sf.Path == "/a.yaml" {
			sf.Imports[0].RawPath = "mutated"
		}
	}

	// Original must be unchanged
	orig := idx.GetFileUnsafe("/a.yaml")
	if orig == nil || orig.Imports[0].RawPath != "globals" {
		t.Fatal("expected original StackFile to be unchanged after mutating AllFiles result")
	}
}

func TestClose_NilWatcher(t *testing.T) {
	idx, _ := New("")
	// Should not panic when watcher is nil
	idx.Close()
}

func TestGetFile_vs_GetFileUnsafe(t *testing.T) {
	idx, _ := New("")
	idx.UpsertFile("/a.yaml", &StackFile{
		Path:    "/a.yaml",
		Imports: []ImportNode{{RawPath: "globals"}},
	})

	// GetFile returns deep copy
	sf := idx.GetFile("/a.yaml")
	if sf == nil {
		t.Fatal("expected non-nil from GetFile")
	}
	sf.Imports[0].RawPath = "mutated"
	orig := idx.GetFileUnsafe("/a.yaml")
	if orig.Imports[0].RawPath != "globals" {
		t.Fatal("GetFile did not return a deep copy")
	}

	// GetFileUnsafe returns same pointer
	unsafe1 := idx.GetFileUnsafe("/a.yaml")
	unsafe2 := idx.GetFileUnsafe("/a.yaml")
	if unsafe1 != unsafe2 {
		t.Fatal("GetFileUnsafe should return the same pointer")
	}
}

func TestFindComponent(t *testing.T) {
	idx, _ := New("")
	idx.UpsertFile("/a.yaml", &StackFile{
		Path: "/a.yaml",
		Comps: []CompNode{
			{Name: "db"},
		},
		Metadata: []MetadataNode{
			{Component: "vpc"},
		},
	})
	idx.UpsertFile("/b.yaml", &StackFile{
		Path: "/b.yaml",
		Comps: []CompNode{
			{Name: "db"},
		},
	})

	tests := []struct {
		name string
		comp string
		want int
	}{
		{
			name: "found in one file",
			comp: "vpc",
			want: 1,
		},
		{
			name: "found in multiple files",
			comp: "db",
			want: 2,
		},
		{
			name: "not found",
			comp: "missing",
			want: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := idx.FindComponent(tt.comp)
			if len(got) != tt.want {
				t.Fatalf("expected %d results, got %d", tt.want, len(got))
			}
		})
	}

	// Verify deep copy: mutate returned value, ensure original unchanged
	results := idx.FindComponent("db")
	if len(results) != 2 {
		t.Fatal("expected 2 results")
	}
	results[0].Comps[0].Name = "mutated"
	orig := idx.GetFileUnsafe("/a.yaml")
	if orig.Comps[0].Name != "db" {
		t.Fatal("FindComponent did not return deep copies")
	}
}

func TestReindexFile(t *testing.T) {
	dir := t.TempDir()
	basePath := dir

	// Create a valid YAML file
	validPath := filepath.Join(basePath, "valid.yaml")
	if err := os.WriteFile(validPath, []byte("import:\n  - globals\ncomponents:\n  terraform:\n    db:\n"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	// Create an invalid YAML file
	invalidPath := filepath.Join(basePath, "invalid.yaml")
	if err := os.WriteFile(invalidPath, []byte("  bad indent\nok:\n  - a\n    b\n"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	// We need a real globals.yaml so that imports resolve during ReindexFile
	globalsPath := filepath.Join(basePath, "globals.yaml")
	if err := os.WriteFile(globalsPath, []byte("{}\n"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	idx, _ := New(basePath)
	idx.ReindexFile(globalsPath)

	// Reindex valid file
	idx.ReindexFile(validPath)
	if sf := idx.GetFile(validPath); sf == nil {
		t.Fatal("expected file after reindex")
	} else if sf.ParseError != "" {
		t.Fatalf("unexpected parse error: %s", sf.ParseError)
	}

	// Verify indexes populated
	if got := idx.FindImporters("globals"); len(got) != 1 || got[0] != validPath {
		t.Fatalf("unexpected importers: %v", got)
	}
	if got := idx.FindComponent("db"); len(got) != 1 {
		t.Fatalf("unexpected component count: %d", len(got))
	}

	// Reindex invalid file → stored with ParseError
	idx.ReindexFile(invalidPath)
	sf := idx.GetFile(invalidPath)
	if sf == nil {
		t.Fatal("expected file after reindex of invalid yaml")
	}
	if sf.ParseError == "" {
		t.Fatal("expected ParseError to be set for invalid yaml")
	}

	// Now test the old-file cleanup path by reindexing valid.yaml with new content.
	// First, write new content that has different imports/components.
	if err := os.WriteFile(validPath, []byte("import:\n  - other\ncomponents:\n  terraform:\n    cache:\n"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	// Create the "other" import target
	otherPath := filepath.Join(basePath, "other.yaml")
	if err := os.WriteFile(otherPath, []byte("{}\n"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	idx.ReindexFile(otherPath)

	// Reindex valid.yaml again
	idx.ReindexFile(validPath)

	// Old import "globals" should no longer point to validPath
	if got := idx.FindImporters("globals"); len(got) != 0 {
		t.Fatalf("expected old import cleaned up, got %v", got)
	}
	// Old component "db" should no longer point to validPath
	if got := idx.FindComponent("db"); len(got) != 0 {
		t.Fatalf("expected old component cleaned up, got %d", len(got))
	}
	// New import "other" should point to validPath
	if got := idx.FindImporters("other"); len(got) != 1 || got[0] != validPath {
		t.Fatalf("unexpected new importers: %v", got)
	}
	// New component "cache" should point to validPath
	if got := idx.FindComponent("cache"); len(got) != 1 {
		t.Fatalf("unexpected new component count: %d", len(got))
	}
}
