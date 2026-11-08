package merge

import (
	"fmt"

	"github.com/pelletier/go-toml/v2"
)

func unmarshalTOML(data []byte) (map[string]any, error) {
	var raw map[string]any
	if err := toml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidSyntax, err)
	}
	if raw == nil {
		return make(map[string]any), nil
	}
	return normalizeMap(raw), nil
}

func marshalTOML(val map[string]any) ([]byte, error) {
	return toml.Marshal(val)
}
