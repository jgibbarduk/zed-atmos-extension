package handler

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/jgibbarduk/zed-atmos-extension/lsp-bridge/internal/index"
)

func TestInterpolateNameTemplate(t *testing.T) {
	tests := []struct {
		name          string
		tpl           string
		vars          map[string]string
		componentName string
		want          string
	}{
		{
			name:          "vars substitution",
			tpl:           "{{ .vars.namespace }}",
			vars:          map[string]string{"namespace": "prod"},
			componentName: "",
			want:          "prod",
		},
		{
			name:          "direct key substitution",
			tpl:           "{{ .namespace }}",
			vars:          map[string]string{"namespace": "staging"},
			componentName: "",
			want:          "staging",
		},
		{
			name:          "atmos_component substitution",
			tpl:           "{{ .atmos_component }}",
			vars:          map[string]string{},
			componentName: "vpc",
			want:          "vpc",
		},
		{
			name:          "unknown variable removed",
			tpl:           "{{ .unknown }}",
			vars:          map[string]string{"namespace": "dev"},
			componentName: "",
			want:          "",
		},
		{
			name:          "mixed template and literal",
			tpl:           "{{ .namespace }}-{{ .environment }}-{{ .atmos_component }}",
			vars:          map[string]string{"namespace": "ex1", "environment": "dev"},
			componentName: "db",
			want:          "ex1-dev-db",
		},
		{
			name:          "empty vars and no component",
			tpl:           "{{ .namespace }}-{{ .atmos_component }}",
			vars:          map[string]string{},
			componentName: "",
			want:          "-",
		},
		{
			name:          "partial match removes unmatched",
			tpl:           "prefix-{{ .known }}-suffix-{{ .unknown }}",
			vars:          map[string]string{"known": "val"},
			componentName: "",
			want:          "prefix-val-suffix-",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := interpolateNameTemplate(tt.tpl, tt.vars, tt.componentName)
			if got != tt.want {
				t.Errorf("interpolateNameTemplate(%q, %v, %q) = %q, want %q",
					tt.tpl, tt.vars, tt.componentName, got, tt.want)
			}
		})
	}
}

func TestComputeStackName(t *testing.T) {
	tests := []struct {
		name         string
		setup        func(t *testing.T) (*index.StackFile, *index.Index)
		nameTemplate string
		want         string
	}{
		{
			name: "nil sf",
			setup: func(t *testing.T) (*index.StackFile, *index.Index) {
				idx, _ := index.New(t.TempDir())
				return nil, idx
			},
			nameTemplate: "",
			want:         "",
		},
		{
			name: "nil idx",
			setup: func(t *testing.T) (*index.StackFile, *index.Index) {
				return &index.StackFile{Path: "/tmp/stack.yaml"}, nil
			},
			nameTemplate: "",
			want:         "",
		},
		{
			name: "relative path strips extension",
			setup: func(t *testing.T) (*index.StackFile, *index.Index) {
				dir := t.TempDir()
				stackPath := filepath.Join(dir, "stacks", "dev", "us-east-2.yaml")
				os.MkdirAll(filepath.Dir(stackPath), 0755)
				os.WriteFile(stackPath, []byte("vars:\n  x: 1\n"), 0644)
				idx, _ := index.New(dir)
				idx.SetBasePath(filepath.Join(dir, "stacks"))
				idx.Reindex()
				return idx.GetFile(stackPath), idx
			},
			nameTemplate: "",
			want:         "dev/us-east-2",
		},
		{
			name: "nameTemplate set interpolates",
			setup: func(t *testing.T) (*index.StackFile, *index.Index) {
				dir := t.TempDir()
				stackPath := filepath.Join(dir, "stacks", "dev", "stack.yaml")
				os.MkdirAll(filepath.Dir(stackPath), 0755)
				os.WriteFile(stackPath, []byte("vars:\n  namespace: ex1\n"), 0644)
				idx, _ := index.New(dir)
				idx.SetBasePath(filepath.Join(dir, "stacks"))
				idx.Reindex()
				return idx.GetFile(stackPath), idx
			},
			nameTemplate: "{{ .namespace }}-{{ .atmos_stack }}",
			want:         "ex1-dev/stack",
		},
		{
			name: "nameTemplate empty returns raw rel path",
			setup: func(t *testing.T) (*index.StackFile, *index.Index) {
				dir := t.TempDir()
				stackPath := filepath.Join(dir, "stacks", "prod.yaml")
				os.MkdirAll(filepath.Dir(stackPath), 0755)
				os.WriteFile(stackPath, []byte("vars:\n  x: 1\n"), 0644)
				idx, _ := index.New(dir)
				idx.SetBasePath(filepath.Join(dir, "stacks"))
				idx.Reindex()
				return idx.GetFile(stackPath), idx
			},
			nameTemplate: "",
			want:         "prod",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sf, idx := tt.setup(t)
			got := computeStackName(sf, idx, tt.nameTemplate)
			if got != tt.want {
				t.Errorf("computeStackName() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFindComponentForLine(t *testing.T) {
	sf := &index.StackFile{
		Comps: []index.CompNode{
			{Name: "vpc", Range: index.Range{StartLine: 10, EndLine: 20}},
			{Name: "db", Range: index.Range{StartLine: 25, EndLine: 35}},
		},
	}

	tests := []struct {
		name string
		line uint32
		want string
	}{
		{"no components empty", 5, ""},
		{"line before first", 5, ""},
		{"line inside vpc", 15, "vpc"},
		{"line between vpc and db", 22, "vpc"},
		{"line inside db", 30, "db"},
		{"line after db", 40, "db"},
		{"first line of vpc", 10, "vpc"},
		{"last line of vpc", 20, "vpc"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findComponentForLine(sf, tt.line)
			if got != tt.want {
				t.Errorf("findComponentForLine(line=%d) = %q, want %q", tt.line, got, tt.want)
			}
		})
	}
}

func TestFindComponentForLine_NoComponents(t *testing.T) {
	sf := &index.StackFile{Comps: []index.CompNode{}}
	got := findComponentForLine(sf, 5)
	if got != "" {
		t.Errorf("findComponentForLine with no components = %q, want empty", got)
	}
}

func TestCollectVars(t *testing.T) {
	tests := []struct {
		name string
		setup func(t *testing.T) (*index.StackFile, *index.Index)
		want map[string]string
	}{
		{
			name: "nil sf returns empty map",
			setup: func(t *testing.T) (*index.StackFile, *index.Index) {
				idx, _ := index.New(t.TempDir())
				return nil, idx
			},
			want: map[string]string{},
		},
		{
			name: "local vars only when idx nil",
			setup: func(t *testing.T) (*index.StackFile, *index.Index) {
				return &index.StackFile{
					Path: "/tmp/test.yaml",
					Vars: []index.VarNode{
						{Key: "a", Value: "1"},
						{Key: "b", Value: "2"},
					},
				}, nil
			},
			want: map[string]string{"a": "1", "b": "2"},
		},
		{
			name: "imports merge with local vars overriding",
			setup: func(t *testing.T) (*index.StackFile, *index.Index) {
				dir := t.TempDir()
				parentPath := filepath.Join(dir, "parent.yaml")
				childPath := filepath.Join(dir, "child.yaml")
				os.WriteFile(parentPath, []byte("vars:\n  a: parent\n  b: parent\n"), 0644)
				os.WriteFile(childPath, []byte("import:\n  - parent\nvars:\n  b: child\n  c: child\n"), 0644)
				idx, _ := index.New(dir)
				idx.SetBasePath(dir)
				idx.Reindex()
				return idx.GetFile(childPath), idx
			},
			want: map[string]string{"a": "parent", "b": "child", "c": "child"},
		},
		{
			name: "circular import stopped by depth limit",
			setup: func(t *testing.T) (*index.StackFile, *index.Index) {
				dir := t.TempDir()
				aPath := filepath.Join(dir, "a.yaml")
				bPath := filepath.Join(dir, "b.yaml")
				os.WriteFile(aPath, []byte("import:\n  - b\nvars:\n  a: 1\n"), 0644)
				os.WriteFile(bPath, []byte("import:\n  - a\nvars:\n  b: 2\n"), 0644)
				idx, _ := index.New(dir)
				idx.SetBasePath(dir)
				idx.Reindex()
				return idx.GetFile(aPath), idx
			},
			want: map[string]string{"a": "1", "b": "2"},
		},
		{
			name: "multiple imports later overrides earlier",
			setup: func(t *testing.T) (*index.StackFile, *index.Index) {
				dir := t.TempDir()
				firstPath := filepath.Join(dir, "first.yaml")
				secondPath := filepath.Join(dir, "second.yaml")
				childPath := filepath.Join(dir, "child.yaml")
				os.WriteFile(firstPath, []byte("vars:\n  x: first\n"), 0644)
				os.WriteFile(secondPath, []byte("vars:\n  x: second\n"), 0644)
				os.WriteFile(childPath, []byte("import:\n  - first\n  - second\nvars:\n  y: child\n"), 0644)
				idx, _ := index.New(dir)
				idx.SetBasePath(dir)
				idx.Reindex()
				return idx.GetFile(childPath), idx
			},
			want: map[string]string{"x": "second", "y": "child"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sf, idx := tt.setup(t)
			got := collectVars(sf, idx)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("collectVars() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestYamlTagDocs(t *testing.T) {
	tests := []struct {
		tag  string
		want string
	}{
		{"!env", "Reads an environment variable"},
		{"!exec", "Executes a shell command"},
		{"!include", "Includes the content of another file"},
		{"!terraform.output", "References a Terraform output"},
		{"!terraform.state", "References a Terraform remote state"},
		{"!store", "Reads a value from a configured store"},
		{"!unknown", ""},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.tag, func(t *testing.T) {
			got := yamlTagDocs(tt.tag)
			if tt.want == "" {
				if got != "" {
					t.Errorf("yamlTagDocs(%q) = %q, want empty", tt.tag, got)
				}
				return
			}
			if !contains(got, tt.want) {
				t.Errorf("yamlTagDocs(%q) = %q, want containing %q", tt.tag, got, tt.want)
			}
		})
	}
}

func contains(s, substr string) bool {
	return len(substr) <= len(s) && (s == substr || len(substr) > 0 && indexOf(s, substr) >= 0)
}

func indexOf(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

func TestParseAtmosConfig(t *testing.T) {
	tests := []struct {
		name         string
		content      string
		wantBase     string
		wantTemplate string
	}{
		{
			name: "happy path",
			content: `stacks:
  base_path: stacks
  name_template: "{{ .namespace }}-{{ .environment }}"
`,
			wantBase:     "stacks",
			wantTemplate: "{{ .namespace }}-{{ .environment }}",
		},
		{
			name:         "missing stacks key",
			content:      "other:\n  key: value\n",
			wantBase:     "",
			wantTemplate: "",
		},
		{
			name: "missing name_template",
			content: `stacks:
  base_path: stacks
`,
			wantBase:     "stacks",
			wantTemplate: "",
		},
		{
			name:         "invalid YAML",
			content:      "{[bad",
			wantBase:     "",
			wantTemplate: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			os.WriteFile(filepath.Join(dir, "atmos.yaml"), []byte(tt.content), 0644)
			base, tmpl := parseAtmosConfig(dir)
			if base != tt.wantBase {
				t.Errorf("parseAtmosConfig basePath = %q, want %q", base, tt.wantBase)
			}
			if tmpl != tt.wantTemplate {
				t.Errorf("parseAtmosConfig nameTemplate = %q, want %q", tmpl, tt.wantTemplate)
			}
		})
	}
}

func TestResolveStacksPath(t *testing.T) {
	t.Run("atmos.yaml present with base_path", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "atmos.yaml"), []byte("stacks:\n  base_path: my-stacks\n"), 0644)
		got := resolveStacksPath(dir)
		want := filepath.Join(dir, "my-stacks")
		if got != want {
			t.Errorf("resolveStacksPath() = %q, want %q", got, want)
		}
	})

	t.Run("atmos.yaml absent defaults to stacks", func(t *testing.T) {
		dir := t.TempDir()
		got := resolveStacksPath(dir)
		want := filepath.Join(dir, "stacks")
		if got != want {
			t.Errorf("resolveStacksPath() = %q, want %q", got, want)
		}
	})
}

func TestFirstLine(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"with newline", "hello\nworld", "hello"},
		{"with CRLF", "hello\r\nworld", "hello\r"},
		{"no newline", "hello", "hello"},
		{"empty string", "", ""},
		{"newline at start", "\nworld", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := firstLine(tt.in)
			if got != tt.want {
				t.Errorf("firstLine(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestNormalizeLines(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "LF file",
			in:   "a\nb\nc",
			want: []string{"a", "b", "c"},
		},
		{
			name: "CRLF file",
			in:   "a\r\nb\r\nc",
			want: []string{"a", "b", "c"},
		},
		{
			name: "mixed endings",
			in:   "a\r\nb\nc\r\n",
			want: []string{"a", "b", "c", ""},
		},
		{
			name: "single line no newline",
			in:   "only",
			want: []string{"only"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeLines(tt.in)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("normalizeLines(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestExtractPartialPath(t *testing.T) {
	tests := []struct {
		name   string
		line   string
		cursor int
		want   string
	}{
		{"cursor mid-word", "catalog/vpc", 11, "catalog/vpc"},
		{"cursor after space", "import: ", 7, ""},
		{"quoted path strips quote", `  - "catalog/vpc`, 16, "catalog/vpc"},
		{"tab separator", "import:\tcatalog/vpc", 19, "catalog/vpc"},
		{"colon separator", "path: catalog/vpc", 17, "catalog/vpc"},
		{"single quote stripped", "'catalog/vpc", 12, "catalog/vpc"},
		{"empty after punctuation", "import:\n", 7, ""},
		{"cursor beyond length", "short", 100, "short"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractPartialPath(tt.line, tt.cursor)
			if got != tt.want {
				t.Errorf("extractPartialPath(%q, %d) = %q, want %q",
					tt.line, tt.cursor, got, tt.want)
			}
		})
	}
}

func TestExtractTemplatePartial(t *testing.T) {
	tests := []struct {
		name     string
		line     string
		cursor   int
		want     string
		wantPos  int
	}{
		{
			name:    "cursor inside template vars.na",
			line:    `value: "{{ .vars.na }}"`,
			cursor:  20, // position after "na" inside {{ .vars.na }}
			want:    "vars.na ",
			wantPos: 12,
		},
		{
			name:    "cursor before dot",
			line:    `value: "{{ vars.na }}"`,
			cursor:  20,
			want:    "",
			wantPos: 0,
		},
		{
			name:    "no open braces before cursor",
			line:    "value: hello",
			cursor:  12,
			want:    "",
			wantPos: 0,
		},
		{
			name:    "closing braces between open and cursor",
			line:    `value: "{{ .vars.na }}" {{ .vars.nb }}`,
			cursor:  38,
			want:    "",
			wantPos: 0,
		},
		{
			name:    "cursor at end of partial",
			line:    `value: "{{ .vars.namespace`,
			cursor:  26,
			want:    "vars.namespace",
			wantPos: 12,
		},
		{
			name:    "cursor inside atmos_component",
			line:    `component: "{{ .atmos_component }}"`,
			cursor:  30,
			want:    "atmos_componen",
			wantPos: 16,
		},
		{
			name:    "empty after dot",
			line:    `value: "{{ . }}"`,
			cursor:  16,
			want:    "",
			wantPos: 0, // because line[14] != '.' after skipping spaces
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotPos := extractTemplatePartial(tt.line, tt.cursor)
			if got != tt.want || gotPos != tt.wantPos {
				t.Errorf("extractTemplatePartial(%q, %d) = (%q, %d), want (%q, %d)",
					tt.line, tt.cursor, got, gotPos, tt.want, tt.wantPos)
			}
		})
	}
}
