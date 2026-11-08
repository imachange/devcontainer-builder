package place

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"text/template"

	"github.com/imachange/devcontainer-builder/internal/merge"
)

func renderTemplate(srcPath string, vars map[string]string) ([]byte, error) {
	raw, err := os.ReadFile(srcPath)
	if err != nil {
		return nil, err
	}

	tmpl, err := template.New(filepath.Base(srcPath)).Option("missingkey=error").Parse(string(raw))
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, vars); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

func appendToFile(destPath string, data []byte, perm os.FileMode) error {
	format, err := merge.DetectFormat(destPath)
	if err == nil {
		existing, readErr := os.ReadFile(destPath)
		if readErr == nil && len(bytes.TrimSpace(existing)) > 0 {
			merged, mergeErr := merge.Merge(format, existing, data)
			if mergeErr == nil {
				return os.WriteFile(destPath, merged, perm)
			}
			return mergeErr
		}
	}

	existing, err := os.ReadFile(destPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	if len(existing) > 0 {
		// 冪等性チェック: 既に末尾に同じ内容が追記されている場合はスキップ
		if bytes.HasSuffix(existing, data) {
			return nil
		}

		var buf bytes.Buffer
		buf.Write(existing)
		// 改行で終わらない既存の末尾には改行を補正
		if existing[len(existing)-1] != '\n' {
			buf.WriteByte('\n')
		}
		buf.Write(data)
		return os.WriteFile(destPath, buf.Bytes(), perm)
	}

	return os.WriteFile(destPath, data, perm)
}

func ensureHomeDir(homeDir string) error {
	if err := os.MkdirAll(homeDir, 0700); err != nil {
		return err
	}

	skelDir := "/etc/skel"
	if fi, err := os.Stat(skelDir); err == nil && fi.IsDir() {
		_ = filepath.WalkDir(skelDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || path == skelDir {
				return nil
			}
			rel, err := filepath.Rel(skelDir, path)
			if err != nil {
				return nil
			}
			target := filepath.Join(homeDir, rel)
			if d.IsDir() {
				_ = os.MkdirAll(target, 0755)
			} else {
				data, err := os.ReadFile(path)
				if err == nil {
					_ = os.WriteFile(target, data, 0644)
				}
			}
			return nil
		})
	}
	return nil
}

func setOwner(path string, uid, gid int) {
	if uid > 0 || gid > 0 {
		_ = os.Chown(path, uid, gid)
	}
}
