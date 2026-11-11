package apply_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/imachange/devcontainer-builder/internal/apply"
	"github.com/imachange/devcontainer-builder/internal/sysinfo"
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

// newContext は、一時ディレクトリに、devcontainer.json と target/ の中身を作り、コンテキストのパスを返す。
func newContext(t *testing.T, devJSON string, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write("devcontainer.json", devJSON)
	for rel, content := range files {
		write("target/"+rel, content)
	}
	return dir
}

func TestApply_InvalidPackageSpecFailsBeforeAnyScriptRuns(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran")
	ctxDir := newContext(t, `{"build":{"args":{"MARKER":"`+marker+`"}}}`, map[string]string{
		"pre.d/10-a.sh":        "#!/bin/sh\ntouch \"$MARKER\"\n",
		"packages/default.txt": "-oAPT::Update::Pre-Invoke::=id\n",
	})
	var out bytes.Buffer
	err := apply.Run(ctxDir, apply.Options{RootDir: t.TempDir()}, &out)
	if err == nil || !strings.Contains(err.Error(), "invalid package name") {
		t.Fatalf("want an invalid-package error, got %v", err)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Error("no script may run when the package specification is invalid")
	}
}

func TestApply_DeniedPlacementFailsBeforeAnyScriptRuns(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran")
	ctxDir := newContext(t, `{"build":{"args":{"MARKER":"`+marker+`"}}}`, map[string]string{
		"pre.d/10-a.sh":                "#!/bin/sh\ntouch \"$MARKER\"\n",
		"files/root/etc/shadow.append": "evil::0:0:::::\n",
	})
	var out bytes.Buffer
	err := apply.Run(ctxDir, apply.Options{RootDir: t.TempDir()}, &out)
	if err == nil || !strings.Contains(err.Error(), "denied path") {
		t.Fatalf("want a denied-path error, got %v", err)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Error("no script may run when the file placement plan is invalid")
	}
}

func TestApply_CheckAlsoValidatesFilePlacement(t *testing.T) {
	ctxDir := newContext(t, `{}`, map[string]string{"files/root/etc/passwd.tmpl": "x"})
	var out bytes.Buffer
	err := apply.Run(ctxDir, apply.Options{Check: true}, &out)
	if err == nil || !strings.Contains(err.Error(), "denied path") {
		t.Fatalf("--check must report an unsafe placement, got %v", err)
	}
}

func TestApply_FailedPreScriptStopsEverything(t *testing.T) {
	rootDir := t.TempDir()
	ctxDir := newContext(t, `{}`, map[string]string{
		"pre.d/10-fail.sh":         "#!/bin/sh\nexit 1\n",
		"files/root/etc/hello.txt": "hello\n",
		"post.d/10-post.sh":        "#!/bin/sh\necho post\n",
	})
	var out bytes.Buffer
	err := apply.Run(ctxDir, apply.Options{RootDir: rootDir}, &out)
	if err == nil || !strings.Contains(err.Error(), "pre scripts failed") {
		t.Fatalf("want a pre-scripts error, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(rootDir, "etc", "hello.txt")); statErr == nil {
		t.Error("files must not be placed after a failed pre script")
	}
	if strings.Contains(out.String(), "post") && strings.Contains(out.String(), "executing post") {
		t.Errorf("post scripts must not run:\n%s", out.String())
	}
}

func TestApply_ScriptsCanUseHelperLibrary(t *testing.T) {
	ctxDir := newContext(t, `{}`, map[string]string{
		"post.d/_lib.sh":   "say() { echo \"lib says $1\"; }\n",
		"post.d/10-use.sh": "#!/bin/sh\n. \"$(dirname \"$0\")/_lib.sh\"\nsay hi\n",
	})
	var out bytes.Buffer
	if err := apply.Run(ctxDir, apply.Options{RootDir: t.TempDir()}, &out); err != nil {
		t.Fatalf("apply failed: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "lib says hi") {
		t.Errorf("the helper library was not sourced:\n%s", out.String())
	}
}

func TestApply_ScriptsSeeDCBVariables(t *testing.T) {
	ctxDir := newContext(t, `{"build":{"args":{"APP_ENV":"dev"}}}`, map[string]string{
		"post.d/10-env.sh": "#!/bin/sh\necho \"phase=$DCB_PHASE env=$APP_ENV arch=$DCB_ARCH\"\n",
	})
	var out bytes.Buffer
	if err := apply.Run(ctxDir, apply.Options{RootDir: t.TempDir()}, &out); err != nil {
		t.Fatalf("apply failed: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "phase=post env=dev arch=") {
		t.Errorf("unexpected script output:\n%s", out.String())
	}
}

func TestApply_FilesHomeForRootUser(t *testing.T) {
	// 開発ユーザーが root のとき、files/home は /root に置かれ、ホームが無ければ作られる。
	rootDir := t.TempDir()
	ctxDir := newContext(t, `{}`, map[string]string{
		"files/home/.profile.append": "export FROM_HOME=1\n",
		"files/home/.ssh/config":     "Host *\n",
	})
	var out bytes.Buffer
	if err := apply.Run(ctxDir, apply.Options{RootDir: rootDir}, &out); err != nil {
		t.Fatalf("apply failed: %v\n%s", err, out.String())
	}
	b, err := os.ReadFile(filepath.Join(rootDir, "root", ".profile"))
	if err != nil || string(b) != "export FROM_HOME=1\n" {
		t.Errorf(".profile: %v, %q", err, b)
	}
	fi, err := os.Stat(filepath.Join(rootDir, "root", ".ssh", "config"))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf(".ssh/config must be 0600: %v, %v", err, fi)
	}
}

func TestApply_PackagesGoThroughTheRunner(t *testing.T) {
	if sysinfo.System().Detect("pre").PkgMgr == "" {
		t.Skip("no package manager on this machine")
	}
	ctxDir := newContext(t, `{"build":{"args":{"BUILD_PACKAGES":"curl jq@1.6"}}}`, nil)
	runner := &mockRunner{}
	var out bytes.Buffer
	if err := apply.Run(ctxDir, apply.Options{RootDir: t.TempDir(), Runner: runner}, &out); err != nil {
		t.Fatalf("apply failed: %v\n%s", err, out.String())
	}
	joined := strings.Join(runner.installed, "\n")
	if !strings.Contains(joined, "curl") || !strings.Contains(joined, "jq") {
		t.Errorf("packages were not passed to the package manager:\n%s", joined)
	}
	if !strings.Contains(out.String(), "==> installing packages...") {
		t.Errorf("missing log:\n%s", out.String())
	}
}

func TestApply_CancellationStopsRunningScripts(t *testing.T) {
	ctxDir := newContext(t, `{}`, map[string]string{
		"post.d/10-sleep.sh": "#!/bin/sh\nsleep 30\n",
	})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()

	var out bytes.Buffer
	start := time.Now()
	err := apply.RunContext(ctx, ctxDir, apply.Options{RootDir: t.TempDir()}, &out)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("cancellation took too long: %v", time.Since(start))
	}
}

func TestApply_OverwriteIsAllowedByDefault(t *testing.T) {
	rootDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(rootDir, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rootDir, "etc", "a"), []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctxDir := newContext(t, `{}`, map[string]string{"files/root/etc/a": "new\n"})

	var out bytes.Buffer
	if err := apply.Run(ctxDir, apply.Options{RootDir: rootDir}, &out); err != nil {
		t.Fatalf("apply failed: %v\n%s", err, out.String())
	}
	b, _ := os.ReadFile(filepath.Join(rootDir, "etc", "a"))
	if string(b) != "new\n" {
		t.Errorf("content = %q", b)
	}
	if !strings.Contains(out.String(), "replaced existing file") {
		t.Errorf("the replacement should be logged:\n%s", out.String())
	}
}
