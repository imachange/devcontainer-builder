package merge_test

import (
	"strings"
	"testing"

	"github.com/imachange/devcontainer-builder/internal/merge"
)

func TestJSONMerge(t *testing.T) {
	base := []byte(`{
  // ベース側のコメント (JSONC)
  "name": "myapp",
  "settings": {
    "theme": "dark",
    "fontSize": 12
  },
  "tags": ["a", "b"]
}`)

	patch := []byte(`{
  /* パッチ側のコメント (JSONC) */
  "settings": {
    "fontSize": 14,
    "lineHeight": 1.5, // 末尾カンマ
  },
  "tags": ["c"],
  "version": "1.0",
}`)

	got, err := merge.Merge(merge.FormatJSON, base, patch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	str := string(got)
	if !strings.Contains(str, `"fontSize": 14`) || !strings.Contains(str, `"lineHeight": 1.5`) {
		t.Errorf("nested merge failed: %s", str)
	}
	if !strings.Contains(str, `"tags": [
    "c"
  ]`) {
		t.Errorf("array overwrite failed: %s", str)
	}
}

func TestYAMLMerge(t *testing.T) {
	base := []byte("a: 1\nb:\n  c: 2\n")
	patch := []byte("b:\n  d: 3\ne: 4\n")

	got, err := merge.Merge(merge.FormatYAML, base, patch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	str := string(got)
	if !strings.Contains(str, "c: 2") || !strings.Contains(str, "d: 3") {
		t.Errorf("YAML merge failed: %s", str)
	}
}

func TestTOMLMerge(t *testing.T) {
	base := []byte("[owner]\nname = \"Tom\"\n")
	patch := []byte("[owner]\nbio = \"Developer\"\n")

	got, err := merge.Merge(merge.FormatTOML, base, patch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	str := string(got)
	if !strings.Contains(str, "name = 'Tom'") && !strings.Contains(str, `name = "Tom"`) {
		t.Errorf("TOML merge failed: %s", str)
	}
}

func TestDetectFormat(t *testing.T) {
	tests := []struct {
		path    string
		want    merge.Format
		wantErr bool
	}{
		{"config.json", merge.FormatJSON, false},
		{"config.yaml", merge.FormatYAML, false},
		{"config.yml", merge.FormatYAML, false},
		{"config.toml", merge.FormatTOML, false},
		{"config.txt", "", true},
	}

	for _, tt := range tests {
		got, err := merge.DetectFormat(tt.path)
		if (err != nil) != tt.wantErr {
			t.Errorf("DetectFormat(%q) error = %v, wantErr %v", tt.path, err, tt.wantErr)
		}
		if got != tt.want {
			t.Errorf("DetectFormat(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}
