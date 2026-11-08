package apply_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/imachange/devcontainer-builder/internal/apply"
)

type mockRunner struct {
	installed []string
}

func (m *mockRunner) Run(ctx context.Context, cmd string, args []string, env []string) error {
	m.installed = append(m.installed, cmd+" "+strings.Join(args, " "))
	return nil
}

func TestApply_CheckOnly(t *testing.T) {
	ctxDir := t.TempDir()
	devJson := `{"build": {"args": {"BUILD_PACKAGES": "curl"}}}`
	if err := os.WriteFile(filepath.Join(ctxDir, "devcontainer.json"), []byte(devJson), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	opts := apply.Options{
		Check:   true,
		RootDir: t.TempDir(),
	}

	err := apply.Run(ctxDir, opts, &out)
	if err != nil {
		t.Fatalf("unexpected error on --check: %v", err)
	}

	logs := out.String()
	if !strings.Contains(logs, "==> plan: pre=0 packages=true files=- post=0") {
		t.Errorf("plan log mismatch: %s", logs)
	}
}

func TestApply_NothingToDo(t *testing.T) {
	ctxDir := t.TempDir()
	devJson := `{}`
	if err := os.WriteFile(filepath.Join(ctxDir, "devcontainer.json"), []byte(devJson), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	opts := apply.Options{
		Check:   true,
		RootDir: t.TempDir(),
	}

	err := apply.Run(ctxDir, opts, &out)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(out.String(), "==> nothing to do") {
		t.Errorf("expected 'nothing to do' output, got: %s", out.String())
	}
}

func TestApply_ExecutionFlow(t *testing.T) {
	ctxDir := t.TempDir()
	rootDir := t.TempDir()

	devJson := `{
  "build": {
    "args": {
      "MY_VAR": "World"
    }
  }
}`
	if err := os.WriteFile(filepath.Join(ctxDir, "devcontainer.json"), []byte(devJson), 0o644); err != nil {
		t.Fatal(err)
	}

	targetDir := filepath.Join(ctxDir, "target")
	_ = os.MkdirAll(filepath.Join(targetDir, "pre.d"), 0o755)
	_ = os.MkdirAll(filepath.Join(targetDir, "post.d"), 0o755)
	_ = os.MkdirAll(filepath.Join(targetDir, "files", "root", "etc"), 0o755)

	_ = os.WriteFile(filepath.Join(targetDir, "pre.d", "01_pre.sh"), []byte("#!/bin/sh\necho 'pre script running'\n"), 0o755)
	_ = os.WriteFile(filepath.Join(targetDir, "post.d", "01_post.sh"), []byte("#!/bin/sh\necho 'post script running'\n"), 0o755)
	_ = os.WriteFile(filepath.Join(targetDir, "files", "root", "etc", "hello.txt.tmpl"), []byte("Hello {{ .MY_VAR }}!\n"), 0o644)

	runner := &mockRunner{}
	var out bytes.Buffer
	opts := apply.Options{
		RootDir: rootDir,
		Runner:  runner,
	}

	err := apply.Run(ctxDir, opts, &out)
	if err != nil {
		t.Fatalf("apply execution failed: %v", err)
	}

	logs := out.String()
	if !strings.Contains(logs, "==> executing pre scripts...") {
		t.Errorf("missing pre scripts log: %s", logs)
	}
	if !strings.Contains(logs, "==> placing files...") {
		t.Errorf("missing placing files log: %s", logs)
	}
	if !strings.Contains(logs, "==> executing post scripts...") {
		t.Errorf("missing post scripts log: %s", logs)
	}
	if !strings.Contains(logs, "==> apply finished successfully") {
		t.Errorf("missing success log: %s", logs)
	}

	// テンプレート展開後のファイル生成結果を確認
	placedContent, err := os.ReadFile(filepath.Join(rootDir, "etc", "hello.txt"))
	if err != nil || string(placedContent) != "Hello World!\n" {
		t.Errorf("placed file content mismatch: err=%v, content=%q", err, string(placedContent))
	}
}

func TestApply_OverwriteForbidden(t *testing.T) {
	ctxDir := t.TempDir()
	rootDir := t.TempDir()

	// BUILD_OVERWRITE=false
	devJson := `{
  "build": {
    "args": {
      "BUILD_OVERWRITE": "false"
    }
  }
}`
	if err := os.WriteFile(filepath.Join(ctxDir, "devcontainer.json"), []byte(devJson), 0o644); err != nil {
		t.Fatal(err)
	}

	targetDir := filepath.Join(ctxDir, "target")
	_ = os.MkdirAll(filepath.Join(targetDir, "files", "root", "etc"), 0o755)
	_ = os.WriteFile(filepath.Join(targetDir, "files", "root", "etc", "config.txt"), []byte("new content"), 0o644)

	// 事前にターゲット上に既存ファイルを作成
	_ = os.MkdirAll(filepath.Join(rootDir, "etc"), 0o755)
	_ = os.WriteFile(filepath.Join(rootDir, "etc", "config.txt"), []byte("old content"), 0o644)

	var out bytes.Buffer
	opts := apply.Options{
		RootDir: rootDir,
	}

	err := apply.Run(ctxDir, opts, &out)
	if err == nil || !strings.Contains(err.Error(), "forbidden (BUILD_OVERWRITE=false)") {
		t.Fatalf("expected overwrite forbidden error, got: %v", err)
	}
}
