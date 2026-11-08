package layout

import (
	"io/fs"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
)

func f(s string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(s), Mode: 0o644} }
func dir() *fstest.MapFile       { return &fstest.MapFile{Mode: fs.ModeDir | 0o755} }

func TestScanDotfilesOnlyIsEmpty(t *testing.T) {
	l, err := Scan(fstest.MapFS{".gitkeep": f("")})
	if err != nil {
		t.Fatal(err)
	}
	if !l.Empty() {
		t.Errorf("a target with only dotfiles must be empty: %+v", l)
	}
}

func TestScanFull(t *testing.T) {
	fsys := fstest.MapFS{
		"pre.d/10-user.sh":          f("#!/bin/sh\n"),
		"pre.d/_lib.sh":             f("# helper\n"),
		"pre.d/.gitkeep":            f(""),
		"post.d/20-setup.sh":        f("#!/bin/sh\n"),
		"post.d/05-first.sh":        f("#!/bin/sh\n"),
		"packages/default.txt":      f("git\r\ncurl # comment\n"),
		"packages/apt.txt":          f("build-essential\n"),
		"files/root/etc/myapp.conf": f("x"),
		"files/home/.bashrc.append": f("x"),
	}
	l, err := Scan(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"pre.d/10-user.sh"}; !reflect.DeepEqual(l.Pre, want) {
		t.Errorf("Pre = %v, want %v (helpers starting with _ or . are skipped)", l.Pre, want)
	}
	if want := []string{"post.d/05-first.sh", "post.d/20-setup.sh"}; !reflect.DeepEqual(l.Post, want) {
		t.Errorf("Post = %v, want %v (name order)", l.Post, want)
	}
	if want := []string{"home", "root"}; !reflect.DeepEqual(l.FileTrees, want) {
		t.Errorf("FileTrees = %v, want %v", l.FileTrees, want)
	}
	if got := l.Packages["default"]; len(got) < 2 || got[0] != "git" || got[1] != "curl # comment" {
		t.Errorf("default packages = %q (CRLF must be normalized, comments kept for the parser)", got)
	}
	if _, ok := l.Packages["apt"]; !ok {
		t.Error("apt.txt should be read")
	}
	if l.Empty() {
		t.Error("Empty() must be false")
	}
}

func TestScanRejects(t *testing.T) {
	tests := []struct {
		name string
		fsys fstest.MapFS
		want string
	}{
		{"legacy image dir",
			fstest.MapFS{"image/etc/x": f("x")}, "renamed to files/"},
		{"legacy scripts dir",
			fstest.MapFS{"scripts/a.sh": f("x")}, "pre.d/"},
		{"manifest.json",
			fstest.MapFS{"manifest.json": f("{}")}, "not used"},
		{"unknown top-level entry",
			fstest.MapFS{"post.sh": f("x")}, "unexpected"},
		{"top-level name that should be a directory",
			fstest.MapFS{"post.d": f("x")}, "must be a directory"},
		{"files directly under files/",
			fstest.MapFS{"files/etc/x": f("x")}, "files/root/"},
		{"unknown file in packages",
			fstest.MapFS{"packages/pacman.txt": f("git")}, "unexpected"},
		{"directory inside packages",
			fstest.MapFS{"packages/default.txt/x": f("git")}, "unexpected"},
		{"script with a space in its name",
			fstest.MapFS{"post.d/bad name.sh": f("x")}, "invalid name"},
		{"directory inside post.d",
			fstest.MapFS{"post.d/sub/a.sh": f("x")}, "regular file"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Scan(tt.fsys)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("want error containing %q, got %v", tt.want, err)
			}
		})
	}
}

func TestScanReportsAllProblems(t *testing.T) {
	_, err := Scan(fstest.MapFS{
		"image/x":          f("x"),
		"post.d/bad name":  f("x"),
		"packages/foo.txt": f("x"),
	})
	if err == nil {
		t.Fatal("want error")
	}
	for _, want := range []string{"image", "bad name", "foo.txt"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q: %v", want, err)
		}
	}
}

func TestScanEmptyDirectoriesAreEmpty(t *testing.T) {
	l, err := Scan(fstest.MapFS{"pre.d": dir(), "post.d": dir(), "packages": dir(), "files": dir()})
	if err != nil {
		t.Fatal(err)
	}
	if !l.Empty() {
		t.Errorf("empty directories mean nothing to do: %+v", l)
	}
}
