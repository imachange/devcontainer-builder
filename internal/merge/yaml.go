package merge

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

func unmarshalYAML(data []byte) (map[string]any, error) {
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidSyntax, err)
	}
	if raw == nil {
		return make(map[string]any), nil
	}
	return normalizeMap(raw), nil
}

func marshalYAML(val map[string]any) ([]byte, error) {
	node := &yaml.Node{}
	if err := node.Encode(val); err != nil {
		return nil, err
	}
	sortYAMLKeys(node)
	return yaml.Marshal(node)
}

func sortYAMLKeys(node *yaml.Node) {
	if node == nil {
		return
	}
	switch node.Kind {
	case yaml.MappingNode:
		type pair struct {
			key *yaml.Node
			val *yaml.Node
		}
		var pairs []pair
		for i := 0; i < len(node.Content); i += 2 {
			sortYAMLKeys(node.Content[i+1])
			pairs = append(pairs, pair{key: node.Content[i], val: node.Content[i+1]})
		}
		for i := 0; i < len(pairs); i++ {
			for j := i + 1; j < len(pairs); j++ {
				if pairs[i].key.Value > pairs[j].key.Value {
					pairs[i], pairs[j] = pairs[j], pairs[i]
				}
			}
		}
		newContent := make([]*yaml.Node, 0, len(node.Content))
		for _, p := range pairs {
			newContent = append(newContent, p.key, p.val)
		}
		node.Content = newContent

	case yaml.SequenceNode, yaml.DocumentNode:
		for _, child := range node.Content {
			sortYAMLKeys(child)
		}
	}
}
