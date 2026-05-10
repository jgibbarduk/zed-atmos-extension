package index

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestParseYAMLContent_emptyDoc(t *testing.T) {
	sf := ParseYAMLContent("test.yaml", []byte(""))
	if sf == nil {
		t.Fatal("expected non-nil StackFile")
	}
	if sf.Path != "test.yaml" {
		t.Fatalf("expected Path=test.yaml, got %q", sf.Path)
	}
}

func TestParseYAMLContent_sequenceRoot(t *testing.T) {
	sf := ParseYAMLContent("test.yaml", []byte("- a\n- b\n"))
	if sf.ParseError != "" {
		t.Fatalf("unexpected parse error: %s", sf.ParseError)
	}
	// Sequence root should be ignored, leaving empty StackFile
	if len(sf.Imports) != 0 || len(sf.Comps) != 0 {
		t.Fatal("expected empty StackFile for sequence root")
	}
}

func TestParseYAMLContent_parseError(t *testing.T) {
	sf := ParseYAMLContent("test.yaml", []byte("import: [\n"))
	if sf.ParseError == "" {
		t.Fatal("expected parse error")
	}
}

func TestNodeRange_nil(t *testing.T) {
	r := nodeRange(nil)
	if r.StartLine != 0 || r.StartChar != 0 {
		t.Fatalf("expected zero range for nil, got %+v", r)
	}
}

func TestNodeRange_zeroLine(t *testing.T) {
	// yaml.Node with Line==0 is treated as empty range
	n := &yaml.Node{Line: 0, Column: 5, Value: "test"}
	r := nodeRange(n)
	if r.StartLine != 0 {
		t.Fatalf("expected zero range for zero line, got %+v", r)
	}
}

func TestNodeRange_quotedStyles(t *testing.T) {
	for _, tc := range []struct {
		style yaml.Style
		want  uint32
	}{
		{yaml.DoubleQuotedStyle, 6}, // "test" = 4 chars + 2 quotes
		{yaml.SingleQuotedStyle, 6},
		{0, 4}, // unquoted
	} {
		n := &yaml.Node{Line: 1, Column: 1, Value: "test", Style: tc.style}
		r := nodeRange(n)
		if r.EndChar != tc.want {
			t.Fatalf("style %d: expected EndChar=%d, got %d", tc.style, tc.want, r.EndChar)
		}
	}
}

func TestNodeRange_nonASCII(t *testing.T) {
	// é is 1 rune but 2 bytes — rune count should be 4
	n := &yaml.Node{Line: 1, Column: 1, Value: "café", Style: 0}
	r := nodeRange(n)
	if r.EndChar != 4 {
		t.Fatalf("expected EndChar=4 for 'café' (4 runes), got %d", r.EndChar)
	}
}

func TestExtractImports_scalar(t *testing.T) {
	yaml := `import:
  - a
  - b
`
	sf := ParseYAMLContent("test.yaml", []byte(yaml))
	if len(sf.Imports) != 2 {
		t.Fatalf("expected 2 imports, got %d", len(sf.Imports))
	}
	if sf.Imports[0].RawPath != "a" {
		t.Fatalf("expected import[0]=a, got %q", sf.Imports[0].RawPath)
	}
	if sf.Imports[1].RawPath != "b" {
		t.Fatalf("expected import[1]=b, got %q", sf.Imports[1].RawPath)
	}
}

func TestExtractImports_objectStyle(t *testing.T) {
	yaml := `import:
  - path: "catalog/vpc"
    context:
      env: prod
`
	sf := ParseYAMLContent("test.yaml", []byte(yaml))
	if len(sf.Imports) != 1 {
		t.Fatalf("expected 1 import, got %d", len(sf.Imports))
	}
	if sf.Imports[0].RawPath != "catalog/vpc" {
		t.Fatalf("expected RawPath=catalog/vpc, got %q", sf.Imports[0].RawPath)
	}
	if sf.Imports[0].Path != "catalog/vpc" {
		t.Fatalf("expected Path=catalog/vpc, got %q", sf.Imports[0].Path)
	}
}

func TestExtractImports_nonSequence(t *testing.T) {
	yaml := `import: "single"
`
	sf := ParseYAMLContent("test.yaml", []byte(yaml))
	if len(sf.Imports) != 0 {
		t.Fatalf("expected 0 imports for scalar import, got %d", len(sf.Imports))
	}
}

func TestExtractComponents(t *testing.T) {
	yaml := `components:
  terraform:
    vpc:
      vars:
        name: test
`
	sf := ParseYAMLContent("test.yaml", []byte(yaml))
	if len(sf.Comps) != 1 {
		t.Fatalf("expected 1 component, got %d", len(sf.Comps))
	}
	if sf.Comps[0].Name != "vpc" {
		t.Fatalf("expected component name vpc, got %q", sf.Comps[0].Name)
	}
}

func TestExtractComponents_helmfile(t *testing.T) {
	yaml := `components:
  helmfile:
    nginx:
      vars: {}
`
	sf := ParseYAMLContent("test.yaml", []byte(yaml))
	if len(sf.Comps) != 1 {
		t.Fatalf("expected 1 component, got %d", len(sf.Comps))
	}
	if sf.Comps[0].Name != "nginx" {
		t.Fatalf("expected component name nginx, got %q", sf.Comps[0].Name)
	}
}

func TestExtractComponents_reservedNamesSkipped(t *testing.T) {
	yaml := `components:
  terraform:
    vars:
      name: test
    metadata:
      component: vpc
`
	sf := ParseYAMLContent("test.yaml", []byte(yaml))
	for _, comp := range sf.Comps {
		if comp.Name == "vars" || comp.Name == "metadata" {
			t.Fatalf("reserved name %q should be skipped", comp.Name)
		}
	}
}

func TestExtractMetadata(t *testing.T) {
	yaml := `components:
  terraform:
    vpc:
      metadata:
        component: "vpc"
        type: "abstract"
        name: "my-vpc"
`
	sf := ParseYAMLContent("test.yaml", []byte(yaml))
	if len(sf.Metadata) != 1 {
		t.Fatalf("expected 1 metadata, got %d", len(sf.Metadata))
	}
	meta := sf.Metadata[0]
	if meta.Component != "vpc" {
		t.Fatalf("expected component=vpc, got %q", meta.Component)
	}
	if meta.Type != "abstract" {
		t.Fatalf("expected type=abstract, got %q", meta.Type)
	}
	if meta.Name != "my-vpc" {
		t.Fatalf("expected name=my-vpc, got %q", meta.Name)
	}
}

func TestExtractMetadata_inheritsSequence(t *testing.T) {
	yaml := `components:
  terraform:
    vpc:
      metadata:
        inherits:
          - base
          - network
`
	sf := ParseYAMLContent("test.yaml", []byte(yaml))
	if len(sf.Metadata) != 2 {
		t.Fatalf("expected 2 metadata entries for sequence inherits, got %d", len(sf.Metadata))
	}
	if sf.Metadata[0].Inherits != "base" {
		t.Fatalf("expected inherits[0]=base, got %q", sf.Metadata[0].Inherits)
	}
	if sf.Metadata[1].Inherits != "network" {
		t.Fatalf("expected inherits[1]=network, got %q", sf.Metadata[1].Inherits)
	}
}

func TestExtractMetadata_emptyNotAppended(t *testing.T) {
	yaml := `components:
  terraform:
    vpc:
      metadata: {}
`
	sf := ParseYAMLContent("test.yaml", []byte(yaml))
	for _, meta := range sf.Metadata {
		if meta.Component == "" && meta.Inherits == "" && meta.Type == "" && meta.Name == "" {
			t.Fatal("empty metadata should not be appended")
		}
	}
}

func TestExtractDependencies(t *testing.T) {
	yaml := `components:
  terraform:
    vpc:
      dependencies:
        components:
          - vpc
          - component: db
`
	sf := ParseYAMLContent("test.yaml", []byte(yaml))
	if len(sf.Deps) != 2 {
		t.Fatalf("expected 2 deps, got %d", len(sf.Deps))
	}
	if sf.Deps[0].Component != "vpc" {
		t.Fatalf("expected dep[0]=vpc, got %q", sf.Deps[0].Component)
	}
	if sf.Deps[1].Component != "db" {
		t.Fatalf("expected dep[1]=db, got %q", sf.Deps[1].Component)
	}
}

func TestExtractDependencies_missingSubkey(t *testing.T) {
	yaml := `components:
  terraform:
    vpc:
      dependencies:
        vars:
          - a
`
	sf := ParseYAMLContent("test.yaml", []byte(yaml))
	if len(sf.Deps) != 0 {
		t.Fatalf("expected 0 deps when components subkey missing, got %d", len(sf.Deps))
	}
}

func TestExtractTerraformStateTags(t *testing.T) {
	yaml := `components:
  terraform:
    vpc:
      vars:
        state: !terraform.state vpc .jq
`
	sf := ParseYAMLContent("test.yaml", []byte(yaml))
	if len(sf.TerraformState) != 1 {
		t.Fatalf("expected 1 terraform state ref, got %d", len(sf.TerraformState))
	}
	if sf.TerraformState[0].Component != "vpc" {
		t.Fatalf("expected component=vpc, got %q", sf.TerraformState[0].Component)
	}
	if sf.TerraformState[0].JQExpr != ".jq" {
		t.Fatalf("expected jq=.jq, got %q", sf.TerraformState[0].JQExpr)
	}
}

func TestExtractYAMLTags(t *testing.T) {
	yaml := `components:
  terraform:
    vpc:
      vars:
        env: !env HOME
`
	sf := ParseYAMLContent("test.yaml", []byte(yaml))
	found := false
	for _, tag := range sf.YAMLTags {
		if tag.Tag == "!env" {
			found = true
			if tag.Value != "HOME" {
				t.Fatalf("expected value=HOME, got %q", tag.Value)
			}
		}
	}
	if !found {
		t.Fatal("expected !env tag in YAMLTags")
	}
}

func TestExtractYAMLTags_mappingNode(t *testing.T) {
	yaml := `components:
  terraform:
    vpc:
      vars:
        data: !include
          path: file.yaml
`
	sf := ParseYAMLContent("test.yaml", []byte(yaml))
	found := false
	for _, tag := range sf.YAMLTags {
		if tag.Tag == "!include" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected !include tag on mapping node")
	}
}

func TestExtractYAMLTags_standardTagIgnored(t *testing.T) {
	yaml := `vars:
  name: !!str hello
`
	sf := ParseYAMLContent("test.yaml", []byte(yaml))
	for _, tag := range sf.YAMLTags {
		if tag.Tag == "!!str" {
			t.Fatal("standard !!str tag should be ignored")
		}
	}
}

func TestExtractBackendTypes(t *testing.T) {
	yaml := `components:
  terraform:
    vpc:
      backend_type: s3
      remote_state_backend_type: local
`
	sf := ParseYAMLContent("test.yaml", []byte(yaml))
	if len(sf.BackendTypes) != 2 {
		t.Fatalf("expected 2 backend types, got %d", len(sf.BackendTypes))
	}
	if sf.BackendTypes[0].Type != "s3" || sf.BackendTypes[0].Kind != "backend_type" {
		t.Fatalf("expected first backend_type=s3, got %+v", sf.BackendTypes[0])
	}
	if sf.BackendTypes[1].Type != "local" || sf.BackendTypes[1].Kind != "remote_state_backend_type" {
		t.Fatalf("expected second=remote_state_backend_type local, got %+v", sf.BackendTypes[1])
	}
}

func TestExtractSettingsDependsOn(t *testing.T) {
	yaml := `components:
  terraform:
    vpc:
      settings:
        depends_on:
          1:
            component: db
`
	sf := ParseYAMLContent("test.yaml", []byte(yaml))
	if len(sf.SettingsDeps) != 1 {
		t.Fatalf("expected 1 settings dep, got %d", len(sf.SettingsDeps))
	}
	if sf.SettingsDeps[0].Key != "1" {
		t.Fatalf("expected key=1, got %q", sf.SettingsDeps[0].Key)
	}
	if sf.SettingsDeps[0].Component != "db" {
		t.Fatalf("expected component=db, got %q", sf.SettingsDeps[0].Component)
	}
}

func TestExtractVars(t *testing.T) {
	yaml := `vars:
  name: hello
  quoted: "world"
  number: 42
`
	sf := ParseYAMLContent("test.yaml", []byte(yaml))
	found := make(map[string]bool)
	quoted := make(map[string]bool)
	for _, v := range sf.Vars {
		found[v.Key] = true
		if v.IsQuoted {
			quoted[v.Key] = true
		}
	}
	if !found["name"] || !found["quoted"] || !quoted["quoted"] || !found["number"] {
		t.Fatalf("unexpected vars state: found=%v quoted=%v", found, quoted)
	}
}

func TestExtractComponentTypeVars(t *testing.T) {
	yaml := `terraform:
  vars:
    region: us-east-1
helmfile:
  vars:
    chart: nginx
`
	sf := ParseYAMLContent("test.yaml", []byte(yaml))
	if len(sf.TerraformVars) != 1 || sf.TerraformVars[0].Key != "region" {
		t.Fatalf("expected terraform.vars.region, got %+v", sf.TerraformVars)
	}
	if len(sf.HelmfileVars) != 1 || sf.HelmfileVars[0].Key != "chart" {
		t.Fatalf("expected helmfile.vars.chart, got %+v", sf.HelmfileVars)
	}
}

func TestExtractOverrides(t *testing.T) {
	yaml := `overrides:
  vars:
    env: prod
`
	sf := ParseYAMLContent("test.yaml", []byte(yaml))
	if len(sf.OverridesVars) != 1 {
		t.Fatalf("expected 1 override var, got %d", len(sf.OverridesVars))
	}
	if sf.OverridesVars[0].Key != "env" {
		t.Fatalf("expected key=env, got %q", sf.OverridesVars[0].Key)
	}
}
