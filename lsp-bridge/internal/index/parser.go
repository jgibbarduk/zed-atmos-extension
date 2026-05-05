package index

import (
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

func parseYAMLFile(path string) (*StackFile, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseYAMLContent(path, content), nil
}

func ParseYAMLContent(path string, content []byte) *StackFile {
	sf := &StackFile{Path: path}

	var doc yaml.Node
	if err := yaml.Unmarshal(content, &doc); err != nil {
		return sf
	}

	if len(doc.Content) == 0 {
		return sf
	}

	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return sf
	}

	for i := 0; i < len(root.Content)-1; i += 2 {
		key := root.Content[i]
		val := root.Content[i+1]

		keyStr := key.Value

		if keyStr == "import" && val != nil {
			sf.Imports = extractImports(val)
		}

		if keyStr == "vars" && val != nil {
			extractVars(val, sf, "")
		}

		if keyStr == "components" && val != nil && val.Kind == yaml.MappingNode {
			extractComponents(val, sf)
		}

		if keyStr == "terraform" && val != nil && val.Kind == yaml.MappingNode {
			extractBackendTypes(val, sf, "")
		}
	}

	extractTerraformStateTags(root, sf)

	return sf
}

func nodeRange(n *yaml.Node) Range {
	if n == nil {
		return Range{}
	}
	if n.Line == 0 || n.Column == 0 {
		return Range{}
	}
	length := len(n.Value)
	if n.Style == yaml.DoubleQuotedStyle || n.Style == yaml.SingleQuotedStyle {
		length += 2
	}
	return Range{
		StartLine: uint32(n.Line - 1),
		StartChar: uint32(n.Column - 1),
		EndLine:   uint32(n.Line - 1),
		EndChar:   uint32(n.Column - 1 + length),
	}
}

func extractImports(node *yaml.Node) []ImportNode {
	var imports []ImportNode

	if node.Kind == yaml.SequenceNode {
		for _, item := range node.Content {
			if item.Kind == yaml.ScalarNode {
				imports = append(imports, ImportNode{
					RawPath: item.Value,
					Range:   nodeRange(item),
				})
			}
		}
	}

	return imports
}

func extractComponents(node *yaml.Node, sf *StackFile) {
	for i := 0; i < len(node.Content)-1; i += 2 {
		compTypeKey := node.Content[i]
		compTypeVal := node.Content[i+1]

		if compTypeVal == nil || compTypeVal.Kind != yaml.MappingNode {
			continue
		}

		compType := compTypeKey.Value
		if compType != "terraform" && compType != "helmfile" {
			continue
		}

		for j := 0; j < len(compTypeVal.Content)-1; j += 2 {
			compKey := compTypeVal.Content[j]

			compName := compKey.Value
			if compName == "" || compName == "vars" || compName == "settings" ||
				compName == "metadata" || compName == "env" {
				continue
			}

			sf.Comps = append(sf.Comps, CompNode{
				Name:  compName,
				Range: nodeRange(compKey),
			})

			if compTypeVal != nil && j+1 < len(compTypeVal.Content) {
				compVal := compTypeVal.Content[j+1]
				if compVal != nil && compVal.Kind == yaml.MappingNode {
					extractMetadata(compVal, sf)
					extractDependencies(compVal, sf)
					extractBackendTypes(compVal, sf, compName)
					extractSettingsDependsOn(compVal, sf, compName)
					for k := 0; k < len(compVal.Content)-1; k += 2 {
						cvKey := compVal.Content[k]
						cvVal := compVal.Content[k+1]
						if cvKey.Value == "vars" {
							extractVars(cvVal, sf, compName)
						}
					}
				}
			}
		}
	}
}

func extractMetadata(compNode *yaml.Node, sf *StackFile) {
	if compNode == nil || compNode.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i < len(compNode.Content)-1; i += 2 {
		key := compNode.Content[i]
		val := compNode.Content[i+1]
		if key.Value != "metadata" || val == nil || val.Kind != yaml.MappingNode {
			continue
		}
		meta := MetadataNode{
			Range: Range{
				StartLine: uint32(val.Line - 1),
				StartChar: uint32(val.Column - 1),
				EndLine:   uint32(val.Line - 1),
				EndChar:   uint32(val.Column - 1),
			},
		}
		for j := 0; j < len(val.Content)-1; j += 2 {
			metaKey := val.Content[j]
			metaVal := val.Content[j+1]
			switch metaKey.Value {
			case "component":
				meta.Component = metaVal.Value
				meta.ComponentRange = nodeRange(metaVal)
			case "inherits":
				if metaVal.Kind == yaml.ScalarNode {
					meta.Inherits = metaVal.Value
					meta.InheritsRange = nodeRange(metaVal)
				} else if metaVal.Kind == yaml.SequenceNode && len(metaVal.Content) > 0 {
					meta.Inherits = metaVal.Content[0].Value
					meta.InheritsRange = nodeRange(metaVal.Content[0])
				}
			case "type":
				meta.Type = metaVal.Value
			}
		}
		sf.Metadata = append(sf.Metadata, meta)
	}
}

func extractDependencies(compNode *yaml.Node, sf *StackFile) {
	if compNode == nil || compNode.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i < len(compNode.Content)-1; i += 2 {
		key := compNode.Content[i]
		val := compNode.Content[i+1]
		if key.Value != "dependencies" || val == nil || val.Kind != yaml.MappingNode {
			continue
		}
		for j := 0; j < len(val.Content)-1; j += 2 {
			depKey := val.Content[j]
			depVal := val.Content[j+1]
			if depKey.Value != "components" || depVal == nil || depVal.Kind != yaml.SequenceNode {
				continue
			}
			for _, item := range depVal.Content {
				if item.Kind == yaml.ScalarNode {
					sf.Deps = append(sf.Deps, DepNode{
						Component: item.Value,
						Range:     nodeRange(item),
					})
				}
			}
		}
	}
}

func extractTerraformStateTags(node *yaml.Node, sf *StackFile) {
	if node == nil {
		return
	}
	if node.Tag == "!terraform.state" && node.Kind == yaml.ScalarNode {
		parts := strings.Fields(node.Value)
		ref := TerraformStateRef{
			Range: nodeRange(node),
		}
		if len(parts) > 0 {
			ref.Component = parts[0]
			if len(parts) > 1 {
				ref.JQExpr = strings.Join(parts[1:], " ")
			}
		}
		sf.TerraformState = append(sf.TerraformState, ref)
	}
	for _, child := range node.Content {
		extractTerraformStateTags(child, sf)
	}
}

func extractBackendTypes(node *yaml.Node, sf *StackFile, componentName string) {
	if node == nil || node.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i < len(node.Content)-1; i += 2 {
		key := node.Content[i]
		val := node.Content[i+1]
		if val == nil || val.Kind != yaml.ScalarNode {
			continue
		}
		switch key.Value {
		case "backend_type":
			sf.BackendTypes = append(sf.BackendTypes, BackendTypeNode{
				Component: componentName,
				Type:      val.Value,
				Kind:      "backend_type",
				Range:     nodeRange(val),
			})
		case "remote_state_backend_type":
			sf.BackendTypes = append(sf.BackendTypes, BackendTypeNode{
				Component: componentName,
				Type:      val.Value,
				Kind:      "remote_state_backend_type",
				Range:     nodeRange(val),
			})
		}
	}
}

func extractSettingsDependsOn(node *yaml.Node, sf *StackFile, componentName string) {
	if node == nil || node.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i < len(node.Content)-1; i += 2 {
		key := node.Content[i]
		val := node.Content[i+1]
		if key.Value != "settings" || val == nil || val.Kind != yaml.MappingNode {
			continue
		}
		for j := 0; j < len(val.Content)-1; j += 2 {
			subKey := val.Content[j]
			subVal := val.Content[j+1]
			if subKey.Value != "depends_on" || subVal == nil || subVal.Kind != yaml.MappingNode {
				continue
			}
			for k := 0; k < len(subVal.Content)-1; k += 2 {
				depKey := subVal.Content[k]
				depVal := subVal.Content[k+1]
				if depVal == nil || depVal.Kind != yaml.MappingNode {
					continue
				}
				depName := depKey.Value
				var depComponent string
				for m := 0; m < len(depVal.Content)-1; m += 2 {
					dk := depVal.Content[m]
					dv := depVal.Content[m+1]
					if dk.Value == "component" && dv != nil && dv.Kind == yaml.ScalarNode {
						depComponent = dv.Value
						break
					}
				}
				if depComponent != "" {
					sf.SettingsDeps = append(sf.SettingsDeps, SettingsDependsOnNode{
						Key:       depName,
						Component: depComponent,
						Range:     nodeRange(depKey),
					})
				}
			}
		}
	}
}

func extractVars(node *yaml.Node, sf *StackFile, componentName string) {
	if node == nil || node.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i < len(node.Content)-1; i += 2 {
		k := node.Content[i]
		v := node.Content[i+1]
		if k.Kind != yaml.ScalarNode {
			continue
		}
		var valueStr string
		if v != nil && v.Kind == yaml.ScalarNode {
			valueStr = v.Value
		}
		isQuoted := false
		if v != nil {
			isQuoted = v.Style == yaml.DoubleQuotedStyle || v.Style == yaml.SingleQuotedStyle
		}
		sf.Vars = append(sf.Vars, VarNode{
			Key:       k.Value,
			Value:     valueStr,
			Range:     nodeRange(v),
			IsQuoted:  isQuoted,
			Component: componentName,
		})
	}
}
