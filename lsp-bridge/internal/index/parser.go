package index

import (
	"os"

	"gopkg.in/yaml.v3"
)

func parseYAMLFile(path string) (*StackFile, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	sf := &StackFile{Path: path}

	var doc yaml.Node
	if err := yaml.Unmarshal(content, &doc); err != nil {
		return sf, nil
	}

	if len(doc.Content) == 0 {
		return sf, nil
	}

	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return sf, nil
	}

	for i := 0; i < len(root.Content)-1; i += 2 {
		key := root.Content[i]
		val := root.Content[i+1]

		keyStr := key.Value

		if keyStr == "import" && val != nil {
			sf.Imports = extractImports(val)
		}

		if keyStr == "components" && val != nil && val.Kind == yaml.MappingNode {
			extractComponents(val, sf)
		}
	}

	return sf, nil
}

func extractImports(node *yaml.Node) []ImportNode {
	var imports []ImportNode

	if node.Kind == yaml.SequenceNode {
		for _, item := range node.Content {
			if item.Kind == yaml.ScalarNode {
				imports = append(imports, ImportNode{
					RawPath: item.Value,
					Range: Range{
						StartLine: uint32(item.Line - 1),
						StartChar: uint32(item.Column - 1),
						EndLine:   uint32(item.Line - 1),
						EndChar:   uint32(item.Column - 1 + len(item.Value)),
					},
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
				Name: compName,
				Range: Range{
					StartLine: uint32(compKey.Line - 1),
					StartChar: uint32(compKey.Column - 1),
					EndLine:   uint32(compKey.Line - 1),
					EndChar:   uint32(compKey.Column - 1 + len(compName)),
				},
			})
		}
	}
}
