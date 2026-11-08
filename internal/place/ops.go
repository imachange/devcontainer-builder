package place

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"text/template"
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
	f, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, perm)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.Write(data)
	return err
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
