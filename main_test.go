package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// tree は、一時ディレクトリにファイルを作り、そのパスを返す。
func tree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func do(args ...string) (code int, stdout, stderr string) {
	var out, errb bytes.Buffer
	code = run(args, &out, &errb)
	return code, out.String(), errb.String()
}

func TestVersion(t *testing.T) {
	code, out, _ := do("version")
	if code != 0 || strings.TrimSpace(out) != version {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestUsageErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"no arguments", nil},
		{"unknown command", []string{"frobnicate"}},
		{"apply without context", []string{"apply"}},
		{"apply with unknown flag", []string{"apply", "--nope", "x"}},
		{"apply with two contexts", []string{"apply", "a", "b"}},
		{"detect with arguments", []string{"detect", "x"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if code, _, _ := do(tt.args...); code != 2 {
				t.Fatalf("exit code = %d, want 2", code)
			}
		})
	}
}

func TestDetectPrintsAllVariables(t *testing.T) {
	code, out, _ := do("detect")
	if code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 8 {
		t.Fatalf("want 8 lines, got %d: %q", len(lines), out)
	}
	for _, l := range lines {
		if !strings.HasPrefix(l, "DCB_") || !strings.Contains(l, "=") {
			t.Errorf("unexpected line %q", l)
		}
	}
}

func TestCheckNothingToDo(t *testing.T) {
	dir := tree(t, map[string]string{
		"devcontainer.json": `{"build":{"dockerfile":"Dockerfile","args":{"BUILD_BASE_IMAGE":"debian:12-slim"}}}`,
	})
	code, _, errb := do("apply", "--check", dir)
	if code != 0 {
		t.Fatalf("exit code = %d\n%s", code, errb)
	}
	for _, want := range []string{"no target directory", "nothing to do", "==> detected"} {
		if !strings.Contains(errb, want) {
			t.Errorf("log should contain %q:\n%s", want, errb)
		}
	}
}

func TestCheckPlan(t *testing.T) {
	dir := tree(t, map[string]string{
		".devcontainer/devcontainer.json":                `{"build":{"dockerfile":"../Dockerfile.devcontainer"}}`,
		".devcontainer/target/post.d/10-setup.sh":        "#!/bin/sh\n",
		".devcontainer/target/files/home/.bashrc.append": "alias ll='ls -l'\n",
		".devcontainer/target/packages/default.txt":      "git\n",
	})
	code, _, errb := do("apply", "--check", dir)
	if code != 0 {
		t.Fatalf("exit code = %d\n%s", code, errb)
	}
	if !strings.Contains(errb, "==> plan: pre=0 packages=true files=home post=1") {
		t.Errorf("unexpected plan:\n%s", errb)
	}
}

func TestCheckPackagesFromBuildArgsOnly(t *testing.T) {
	dir := tree(t, map[string]string{
		"devcontainer.json": `{"build":{"args":{"BUILD_PACKAGES":"git curl"}}}`,
	})
	code, _, errb := do("apply", "--check", dir)
	if code != 0 || !strings.Contains(errb, "packages=true") {
		t.Fatalf("code=%d\n%s", code, errb)
	}
}

func TestCheckRejectsLegacyLayout(t *testing.T) {
	dir := tree(t, map[string]string{
		"devcontainer.json":  `{}`,
		"target/image/etc/x": "x",
	})
	code, _, errb := do("apply", "--check", dir)
	if code != 1 || !strings.Contains(errb, "renamed to files/") {
		t.Fatalf("code=%d\n%s", code, errb)
	}
}

func TestCheckWarnsAboutFeatures(t *testing.T) {
	dir := tree(t, map[string]string{
		"devcontainer.json": `{"features":{"ghcr.io/devcontainers/features/common-utils:2":{}}}`,
	})
	code, _, errb := do("apply", "--check", dir)
	if code != 0 || !strings.Contains(errb, "[WARN] features") {
		t.Fatalf("code=%d\n%s", code, errb)
	}
}

func TestCheckHidesInfoUnlessVerbose(t *testing.T) {
	dir := tree(t, map[string]string{
		"devcontainer.json": `{"name":"x","forwardPorts":[3000]}`,
	})
	_, _, quiet := do("apply", "--check", dir)
	_, _, loud := do("apply", "--check", "-v", dir)
	if strings.Contains(quiet, "[INFO] name") {
		t.Errorf("INFO must be hidden by default:\n%s", quiet)
	}
	if !strings.Contains(loud, "[INFO]") {
		t.Errorf("INFO must be shown with -v:\n%s", loud)
	}
}

func TestExplicitTargetMustExist(t *testing.T) {
	dir := tree(t, map[string]string{"devcontainer.json": `{}`})
	code, _, errb := do("apply", "--check", "--target", "custom", dir)
	if code != 1 || !strings.Contains(errb, "--target custom") {
		t.Fatalf("code=%d\n%s", code, errb)
	}
}

func TestExplicitConfigAndTarget(t *testing.T) {
	dir := tree(t, map[string]string{
		"images/py/devcontainer.json":     `{}`,
		"images/py/target/post.d/10-a.sh": "#!/bin/sh\n",
		"devcontainer.json":               `{}`,
		".devcontainer/devcontainer.json": `{}`,
	})
	code, _, errb := do("apply", "--check", "--config", "images/py/devcontainer.json", dir)
	if code != 0 || !strings.Contains(errb, "post=1") {
		t.Fatalf("code=%d\n%s", code, errb)
	}
}

func TestAmbiguousConfig(t *testing.T) {
	dir := tree(t, map[string]string{
		"devcontainer.json":               `{}`,
		".devcontainer/devcontainer.json": `{}`,
	})
	code, _, errb := do("apply", "--check", dir)
	if code != 1 || !strings.Contains(errb, "multiple devcontainer.json") {
		t.Fatalf("code=%d\n%s", code, errb)
	}
}

func TestConfigErrorsAreReported(t *testing.T) {
	dir := tree(t, map[string]string{
		"devcontainer.json": `{"build":{"args":{"DCB_X":"1","TOKEN":"${localEnv:TOKEN}"}}}`,
	})
	code, _, errb := do("apply", "--check", dir)
	if code != 1 || !strings.Contains(errb, "reserved") || !strings.Contains(errb, "${...}") {
		t.Fatalf("code=%d\n%s", code, errb)
	}
}

// --check なしの apply は、まだ実行フェーズを持たない。何かを実行すべき構成で、黙って成功してはいけない。
func TestApplyWithoutCheckNeverSucceedsSilently(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("apply runs on Linux only")
	}
	dir := tree(t, map[string]string{
		"devcontainer.json":     `{}`,
		"target/post.d/10-a.sh": "#!/bin/sh\n",
	})
	code, _, errb := do("apply", dir)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", code, errb)
	}
	if os.Geteuid() == 0 {
		if !strings.Contains(errb, "not implemented") {
			t.Errorf("as root, want a not-implemented error:\n%s", errb)
		}
	} else if !strings.Contains(errb, "must run as root") {
		t.Errorf("as non-root, want a root error:\n%s", errb)
	}
}
