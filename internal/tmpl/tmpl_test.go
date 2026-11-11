package tmpl

import (
	"strings"
	"testing"
)

func TestExpand(t *testing.T) {
	vars := map[string]string{"A": "1", "B_2": "two", "EMPTY": ""}
	tests := []struct {
		name, in, want string
	}{
		{"plain", "no variables", "no variables"},
		{"simple", "x={{ A }}", "x=1"},
		{"no spaces", "x={{A}}", "x=1"},
		{"leading dot is accepted", "x={{ .A }}", "x=1"},
		{"underscore and digit", "{{ B_2 }}", "two"},
		{"empty value is defined", "[{{ EMPTY }}]", "[]"},
		{"several on one line", "{{ A }}-{{ B_2 }}", "1-two"},
		{"go template syntax is left alone", "{{ .Values.x }} {{ range .Items }}", "{{ .Values.x }} {{ range .Items }}"},
		{"single braces are left alone", "${A} {A}", "${A} {A}"},
		{"values are not expanded again", "{{ A }}", "1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Expand(tt.in, vars)
			if err != nil || got != tt.want {
				t.Fatalf("Expand(%q) = %q, %v; want %q", tt.in, got, err, tt.want)
			}
		})
	}
}

func TestExpandDoesNotReexpandValues(t *testing.T) {
	got, err := Expand("{{ A }}", map[string]string{"A": "{{ B }}", "B": "boom"})
	if err != nil || got != "{{ B }}" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestExpandUndefined(t *testing.T) {
	_, err := Expand("{{ Z }} {{ A }} {{ Z }} {{ Y }}", map[string]string{"A": "1"})
	if err == nil {
		t.Fatal("want error")
	}
	if !strings.Contains(err.Error(), "undefined template variable") {
		t.Errorf("unexpected message: %v", err)
	}
	if !strings.Contains(err.Error(), "Y, Z") {
		t.Errorf("names should be sorted and unique: %v", err)
	}
}

func TestExpandBytes(t *testing.T) {
	got, err := ExpandBytes([]byte("v={{ V }}\n"), map[string]string{"V": "9"})
	if err != nil || string(got) != "v=9\n" {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := ExpandBytes([]byte("{{ V }}"), nil); err == nil {
		t.Fatal("want error for undefined variable")
	}
}
