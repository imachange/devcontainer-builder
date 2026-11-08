package merge

import (
	"bytes"
	"fmt"
)

// Merge は、指定されたフォーマットに従い既存データとパッチデータをキー単位で再帰的にマージする。
// 既存データ (existingData) が空の場合は、パッチデータのバリデーションおよび整形を行って返す。
func Merge(format Format, existingData, patchData []byte) ([]byte, error) {
	if len(bytes.TrimSpace(patchData)) == 0 {
		return existingData, nil
	}

	var err error
	var existingVal, patchVal map[string]any

	switch format {
	case FormatJSON:
		if len(bytes.TrimSpace(existingData)) > 0 {
			existingVal, err = UnmarshalJSON(existingData)
			if err != nil {
				return nil, fmt.Errorf("failed to parse existing JSON: %w", err)
			}
		}
		patchVal, err = UnmarshalJSON(patchData)
		if err != nil {
			return nil, fmt.Errorf("failed to parse patch JSON: %w", err)
		}

	case FormatYAML:
		if len(bytes.TrimSpace(existingData)) > 0 {
			existingVal, err = unmarshalYAML(existingData)
			if err != nil {
				return nil, fmt.Errorf("failed to parse existing YAML: %w", err)
			}
		}
		patchVal, err = unmarshalYAML(patchData)
		if err != nil {
			return nil, fmt.Errorf("failed to parse patch YAML: %w", err)
		}

	case FormatTOML:
		if len(bytes.TrimSpace(existingData)) > 0 {
			existingVal, err = unmarshalTOML(existingData)
			if err != nil {
				return nil, fmt.Errorf("failed to parse existing TOML: %w", err)
			}
		}
		patchVal, err = unmarshalTOML(patchData)
		if err != nil {
			return nil, fmt.Errorf("failed to parse patch TOML: %w", err)
		}

	default:
		return nil, ErrUnsupportedFormat
	}

	if existingVal == nil {
		existingVal = make(map[string]any)
	}

	merged := mergeMaps(existingVal, patchVal)

	switch format {
	case FormatJSON:
		return MarshalJSONSorted(merged)
	case FormatYAML:
		return marshalYAML(merged)
	case FormatTOML:
		return marshalTOML(merged)
	default:
		return nil, ErrUnsupportedFormat
	}
}

// mergeMaps は、2つのマップを再帰的にマージする。
// オブジェクト (map[string]any) は再帰マージ、配列・スカラ値は patch 側で上書き置換する。
func mergeMaps(base, patch map[string]any) map[string]any {
	result := make(map[string]any)

	for k, v := range base {
		result[k] = v
	}

	for k, patchVal := range patch {
		baseVal, exists := result[k]
		if !exists {
			result[k] = patchVal
			continue
		}

		baseMap, baseIsMap := baseVal.(map[string]any)
		patchMap, patchIsMap := patchVal.(map[string]any)

		if baseIsMap && patchIsMap {
			result[k] = mergeMaps(baseMap, patchMap)
		} else {
			// 配列・スカラ値は上書き置換
			result[k] = patchVal
		}
	}

	return result
}
