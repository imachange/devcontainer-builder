// Package merge は、.json, .toml, .yaml, .yml ファイルに対するキー単位の構造化マージを行う。
package merge

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// Format は、マージ対象のデータ形式を表す。
type Format string

const (
	FormatJSON Format = "json"
	FormatYAML Format = "yaml"
	FormatTOML Format = "toml"
)

var (
	ErrUnsupportedFormat = errors.New("unsupported file format for merge")
	ErrInvalidSyntax     = errors.New("syntax error in configuration file")
)

// DetectFormat は、ファイルパスの拡張子からデータ形式を判定する。
func DetectFormat(path string) (Format, error) {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".json":
		return FormatJSON, nil
	case ".yaml", ".yml":
		return FormatYAML, nil
	case ".toml":
		return FormatTOML, nil
	default:
		return "", fmt.Errorf("%w: %s", ErrUnsupportedFormat, ext)
	}
}
