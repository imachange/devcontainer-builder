package merge

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/imachange/devcontainer-parser/jsonc"
)

// UnmarshalJSON は、devcontainer-parser/jsonc を使用して
// JSONC (コメント・末尾カンマ・BOM入り) を標準 JSON に変換してから解析する。
func UnmarshalJSON(data []byte) (map[string]any, error) {
	stdJSON, err := jsonc.Standardize(data)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidSyntax, err)
	}

	dec := json.NewDecoder(bytes.NewReader(stdJSON))
	dec.UseNumber()

	var result map[string]any
	if err := dec.Decode(&result); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidSyntax, err)
	}

	if dec.More() {
		return nil, fmt.Errorf("%w: extra tokens after JSON payload", ErrInvalidSyntax)
	}

	return normalizeMap(result), nil
}

// MarshalJSONSorted は、キーを辞書順に整列してインデント付き JSON を出力する。
func MarshalJSONSorted(val map[string]any) ([]byte, error) {
	buf := &bytes.Buffer{}
	enc := json.NewEncoder(buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)

	if err := enc.Encode(val); err != nil {
		return nil, err
	}

	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

func normalizeMap(m map[string]any) map[string]any {
	res := make(map[string]any, len(m))
	for k, v := range m {
		res[k] = normalizeValue(v)
	}
	return res
}

func normalizeValue(val any) any {
	switch v := val.(type) {
	case map[string]any:
		return normalizeMap(v)
	case map[any]any:
		res := make(map[string]any, len(v))
		for k, item := range v {
			res[fmt.Sprint(k)] = normalizeValue(item)
		}
		return res
	case []any:
		res := make([]any, len(v))
		for i, item := range v {
			res[i] = normalizeValue(item)
		}
		return res
	default:
		return v
	}
}
