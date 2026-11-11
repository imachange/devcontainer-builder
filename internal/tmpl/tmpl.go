// Package tmpl は、`{{ VAR }}` 形式の変数展開を行う。
//
// files/ の *.tmpl と、packages の版指定 (name@{{ VERSION }}) が、同じ規則で展開されるように、
// 展開はこのパッケージに一本化している。
//
// 規則:
//
//   - 展開するのは、`{{ NAME }}` と `{{ .NAME }}` (先頭のドットは省略できる) だけ。
//     NAME は英数字とアンダースコアで、数字では始まらない。
//   - 未定義の変数は、置換せずにエラーにする (黙って空文字にしない)。
//   - 上の形に当てはまらない `{{ ... }}` (Go テンプレートの `{{ .Values.x }}` や
//     `{{ range .Items }}` など) は、そのまま残す。
//   - 展開は 1 回だけ行う。変数の値に `{{ }}` があっても、再展開しない。
//   - text/template のような、制御構文や関数呼び出しは持たない。
package tmpl

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

var varRe = regexp.MustCompile(`\{\{\s*\.?([A-Za-z_][A-Za-z0-9_]*)\s*\}\}`)

// Expand は、s の中の `{{ NAME }}` を、vars の値で置き換える。
// 未定義の変数があれば、(重複を除いた、名前順の) 一覧をエラーにして返す。
func Expand(s string, vars map[string]string) (string, error) {
	var missing []string
	out := varRe.ReplaceAllStringFunc(s, func(match string) string {
		name := varRe.FindStringSubmatch(match)[1]
		v, ok := vars[name]
		if !ok {
			missing = append(missing, name)
			return match
		}
		return v
	})
	if len(missing) > 0 {
		slices.Sort(missing)
		missing = slices.Compact(missing)
		return "", fmt.Errorf("undefined template variable(s): %s", strings.Join(missing, ", "))
	}
	return out, nil
}

// ExpandBytes は、Expand のバイト列版。
func ExpandBytes(b []byte, vars map[string]string) ([]byte, error) {
	s, err := Expand(string(b), vars)
	if err != nil {
		return nil, err
	}
	return []byte(s), nil
}
