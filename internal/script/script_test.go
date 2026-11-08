package script_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/imachange/devcontainer-builder/internal/script"
)

func TestRunDir_SuccessAndOrdering(t *testing.T) {
	tmpDir := t.TempDir()

	// 実行されるスクリプト (名前順: 01_a.sh -> 02_b.sh)
	s1 := "#!/bin/sh\necho 'step 1'\n"
	s2 := "#!/bin/sh\necho 'step 2'\n"
	// スキップされるスクリプト
	sSkip1 := "#!/bin/sh\necho 'skip dot'\n"
	sSkip2 := "#!/bin/sh\necho 'skip underscore'\n"

	if err := os.WriteFile(filepath.Join(tmpDir, "02_b.sh"), []byte(s2), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "01_a.sh"), []byte(s1), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, ".hidden.sh"), []byte(sSkip1), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "_lib.sh"), []byte(sSkip2), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	cfg := script.Config{
		Stdout: &stdout,
	}

	err := script.RunDir(context.Background(), tmpDir, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := stdout.String()
	want := "step 1\nstep 2\n"
	if got != want {
		t.Errorf("got stdout:\n%q\nwant:\n%q", got, want)
	}
}

func TestRunFile_MissingShebang(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "noshebang.sh")
	if err := os.WriteFile(path, []byte("echo 'no shebang'"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := script.RunFile(context.Background(), path, script.Config{})
	if err == nil || !errors.Is(err, script.ErrMissingShebang) {
		t.Fatalf("expected ErrMissingShebang, got %v", err)
	}
}

func TestRunFile_EnvironmentVariables(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "env.sh")

	scriptBody := `#!/bin/sh
echo "DCB_PHASE=$DCB_PHASE"
echo "MY_VAR=$MY_VAR"
echo "BUILD_USER=$BUILD_USER"
echo "PATH=$PATH"
`
	if err := os.WriteFile(path, []byte(scriptBody), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	cfg := script.Config{
		DCBEnv: map[string]string{
			"DCB_PHASE": "pre",
		},
		BuildArgs: map[string]string{
			"MY_VAR":     "hello",
			"BUILD_USER": "ignored",   // BUILD_* は除外される
			"PATH":       "/bad/path", // PATH の上書きは不可
		},
		BaseEnv: []string{"PATH=/custom/bin"},
		Stdout:  &stdout,
	}

	if err := script.RunFile(context.Background(), path, cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "DCB_PHASE=pre") {
		t.Errorf("missing DCB_PHASE in output: %s", out)
	}
	if !strings.Contains(out, "MY_VAR=hello") {
		t.Errorf("missing MY_VAR in output: %s", out)
	}
	if strings.Contains(out, "BUILD_USER=ignored") {
		t.Errorf("BUILD_USER should be filtered out: %s", out)
	}
	if strings.Contains(out, "PATH=/bad/path") {
		t.Errorf("PATH override should be blocked: %s", out)
	}
}

func TestRunFile_Timeout(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "sleep.sh")
	scriptBody := "#!/bin/sh\nsleep 10\n"
	if err := os.WriteFile(path, []byte(scriptBody), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := script.Config{
		Timeout: 100 * time.Millisecond,
	}

	start := time.Now()
	err := script.RunFile(context.Background(), path, cfg)
	elapsed := time.Since(start)

	if err == nil || !errors.Is(err, script.ErrTimeout) {
		t.Fatalf("expected ErrTimeout, got %v", err)
	}
	if elapsed > 2*time.Second {
		t.Errorf("script execution took too long: %v", elapsed)
	}
}
