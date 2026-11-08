package place

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/imachange/devcontainer-builder/internal/config"
	"github.com/imachange/devcontainer-builder/internal/layout"
	"github.com/imachange/devcontainer-builder/internal/sysinfo"
)

func TestBuildPlanAndExecute(t *testing.T) {
	srcDir := t.TempDir()
	rootDir := t.TempDir()

	targetDir := filepath.Join(srcDir, "target")
	filesRoot := filepath.Join(targetDir, "files", "root")
	filesHome := filepath.Join(targetDir, "files", "home")

	_ = os.MkdirAll(filepath.Join(filesRoot, "etc"), 0755)
	_ = os.WriteFile(filepath.Join(filesRoot, "etc", "config.conf"), []byte("key=value\n"), 0644)

	_ = os.MkdirAll(filepath.Join(filesRoot, "etc", "sudoers.d"), 0755)
	_ = os.WriteFile(filepath.Join(filesRoot, "etc", "sudoers.d", "dev"), []byte("dev ALL=(ALL) NOPASSWD:ALL\n"), 0644)

	_ = os.MkdirAll(filesHome, 0755)
	_ = os.WriteFile(filepath.Join(filesHome, "greeting.txt.tmpl"), []byte("Hello {{ .USER }}!\n"), 0644)
	_ = os.WriteFile(filepath.Join(filesHome, "notes.txt.append"), []byte("line 2\n"), 0644)

	lay := &layout.Layout{
		FileTrees: []string{"root", "home"},
	}

	cfg := &config.Settings{
		Vars: map[string]string{"USER": "alice"},
	}

	info := sysinfo.Info{
		User: "alice",
		Home: "/home/alice",
	}

	plan, err := BuildPlan(lay, cfg, info, targetDir, rootDir)
	if err != nil {
		t.Fatalf("BuildPlan error: %v", err)
	}

	if len(plan.Actions) == 0 {
		t.Fatalf("expected plan actions, got 0")
	}

	userHomeTarget := filepath.Join(rootDir, "home", "alice")
	_ = os.MkdirAll(userHomeTarget, 0755)
	_ = os.WriteFile(filepath.Join(userHomeTarget, "notes.txt"), []byte("line 1\n"), 0644)

	err = Execute(context.Background(), plan, cfg.Vars)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	confContent, err := os.ReadFile(filepath.Join(rootDir, "etc", "config.conf"))
	if err != nil || string(confContent) != "key=value\n" {
		t.Errorf("config.conf content mismatch: %v, %q", err, string(confContent))
	}

	sudoersFi, err := os.Stat(filepath.Join(rootDir, "etc", "sudoers.d", "dev"))
	if err != nil || sudoersFi.Mode().Perm() != 0440 {
		t.Errorf("sudoers.d/dev perm = %o, want 0440", sudoersFi.Mode().Perm())
	}

	greetingContent, err := os.ReadFile(filepath.Join(userHomeTarget, "greeting.txt"))
	if err != nil || string(greetingContent) != "Hello alice!\n" {
		t.Errorf("greeting.txt content mismatch: %v, %q", err, string(greetingContent))
	}

	notesContent, err := os.ReadFile(filepath.Join(userHomeTarget, "notes.txt"))
	if err != nil || string(notesContent) != "line 1\nline 2\n" {
		t.Errorf("notes.txt content mismatch: %v, %q", err, string(notesContent))
	}
}

func TestSecurityRejections(t *testing.T) {
	srcDir := t.TempDir()
	rootDir := t.TempDir()

	targetDir := filepath.Join(srcDir, "target")
	filesRoot := filepath.Join(targetDir, "files", "root")
	_ = os.MkdirAll(filepath.Join(filesRoot, "etc"), 0755)
	_ = os.WriteFile(filepath.Join(filesRoot, "etc", "shadow"), []byte("secret"), 0644)

	lay := &layout.Layout{FileTrees: []string{"root"}}
	info := sysinfo.Info{User: "root"}

	_, err := BuildPlan(lay, nil, info, targetDir, rootDir)
	if err == nil || !strings.Contains(err.Error(), "denied path") {
		t.Fatalf("want denied path error, got %v", err)
	}
}
