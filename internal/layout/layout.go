// Package layout は、target/ ディレクトリの構成を読み取り、規約に反するものを拒否する。
//
// 副作用は無い。fs.FS だけを受け取るので、testing/fstest でテストできる。
//
//	target/
//	├── pre.d/       パッケージ導入の前に実行するスクリプト
//	├── packages/    default.txt apt.txt dnf.txt apk.txt zypper.txt
//	├── files/
//	│   ├── root/    / に重ねるファイル
//	│   └── home/    開発ユーザーのホームに重ねるファイル
//	└── post.d/      ファイル配置のあとに実行するスクリプト
package layout

import (
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"strings"
)

const maxPackageFileSize = 1 << 20

// Layout は、target/ の走査結果。
type Layout struct {
	// Pre と Post は、実行するスクリプトの、target からの相対パス (名前順)。
	Pre, Post []string
	// Packages は、packages/*.txt の行 (生のテキスト)。キーは default / apt / dnf / apk / zypper。
	Packages map[string][]string
	// FileTrees は、files/ の下にあるツリー (root / home)。
	FileTrees []string
}

// Empty は、実行することが何も無いかを返す。
func (l *Layout) Empty() bool {
	return len(l.Pre) == 0 && len(l.Post) == 0 && len(l.Packages) == 0 && len(l.FileTrees) == 0
}

var (
	topDirs     = map[string]bool{"pre.d": true, "packages": true, "files": true, "post.d": true}
	pkgFiles    = map[string]bool{"default.txt": true, "apt.txt": true, "dnf.txt": true, "apk.txt": true, "zypper.txt": true}
	fileTrees   = map[string]bool{"root": true, "home": true}
	scriptName  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	legacyHints = map[string]string{
		"image":         "renamed to files/ (use files/root/ for / and files/home/ for the user's home)",
		"scripts":       "use pre.d/ (before packages) or post.d/ (after files)",
		"manifest.json": "manifest.json is not used",
	}
)

// Scan は、fsys (target ディレクトリ) を走査する。複数の問題は、まとめてエラーにする。
func Scan(fsys fs.FS) (*Layout, error) {
	top, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, err
	}

	l := &Layout{Packages: map[string][]string{}}
	var errs []error

	for _, e := range top {
		name := e.Name()
		if strings.HasPrefix(name, ".") { // .gitkeep など
			continue
		}
		if !topDirs[name] {
			if hint, ok := legacyHints[name]; ok {
				errs = append(errs, fmt.Errorf("%s: %s", name, hint))
			} else {
				errs = append(errs, fmt.Errorf("%s: unexpected (allowed: pre.d, packages, files, post.d)", name))
			}
			continue
		}
		if !e.IsDir() {
			errs = append(errs, fmt.Errorf("%s: must be a directory", name))
			continue
		}

		var err error
		switch name {
		case "pre.d":
			l.Pre, err = scripts(fsys, name)
		case "post.d":
			l.Post, err = scripts(fsys, name)
		case "packages":
			err = l.readPackages(fsys)
		case "files":
			l.FileTrees, err = trees(fsys)
		}
		if err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return l, nil
}

// scripts は、dir の中のスクリプトを、名前順に返す。
// "." か "_" で始まる名前は、部品用 (実行しない) として読み飛ばす。
func scripts(fsys fs.FS, dir string) ([]string, error) {
	ents, err := fs.ReadDir(fsys, dir) // ReadDir は名前順
	if err != nil {
		return nil, err
	}
	var out []string
	var errs []error
	for _, e := range ents {
		n := e.Name()
		if strings.HasPrefix(n, ".") || strings.HasPrefix(n, "_") {
			continue
		}
		if !e.Type().IsRegular() {
			errs = append(errs, fmt.Errorf("%s/%s: must be a regular file (no directories or symlinks)", dir, n))
			continue
		}
		if !scriptName.MatchString(n) {
			errs = append(errs, fmt.Errorf("%s/%s: invalid name (allowed: letters, digits, '.', '_', '-')", dir, n))
			continue
		}
		out = append(out, dir+"/"+n)
	}
	return out, errors.Join(errs...)
}

func (l *Layout) readPackages(fsys fs.FS) error {
	ents, err := fs.ReadDir(fsys, "packages")
	if err != nil {
		return err
	}
	var errs []error
	for _, e := range ents {
		n := e.Name()
		if strings.HasPrefix(n, ".") {
			continue
		}
		if !pkgFiles[n] || !e.Type().IsRegular() {
			errs = append(errs, fmt.Errorf("packages/%s: unexpected (allowed regular files: "+
				"default.txt, apt.txt, dnf.txt, apk.txt, zypper.txt)", n))
			continue
		}
		b, err := fs.ReadFile(fsys, "packages/"+n)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if len(b) > maxPackageFileSize {
			errs = append(errs, fmt.Errorf("packages/%s: larger than %d bytes", n, maxPackageFileSize))
			continue
		}
		text := strings.ReplaceAll(string(b), "\r\n", "\n")
		l.Packages[strings.TrimSuffix(n, ".txt")] = strings.Split(text, "\n")
	}
	return errors.Join(errs...)
}

// trees は、files/ の下に置けるツリー (root と home) を返す。
func trees(fsys fs.FS) ([]string, error) {
	ents, err := fs.ReadDir(fsys, "files")
	if err != nil {
		return nil, err
	}
	var out []string
	var errs []error
	for _, e := range ents {
		n := e.Name()
		if strings.HasPrefix(n, ".") {
			continue
		}
		if !fileTrees[n] || !e.IsDir() {
			errs = append(errs, fmt.Errorf("files/%s: unexpected; put files under files/root/ (-> /) "+
				"or files/home/ (-> the user's home)", n))
			continue
		}
		out = append(out, n)
	}
	return out, errors.Join(errs...)
}
