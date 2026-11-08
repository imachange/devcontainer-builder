package config

import (
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func mapfs(files map[string]string) fstest.MapFS {
	m := fstest.MapFS{}
	for k, v := range files {
		m[k] = &fstest.MapFile{Data: []byte(v), Mode: 0o644}
	}
	return m
}

func load(t *testing.T, json string) (*Settings, error) {
	t.Helper()
	return Load(mapfs(map[string]string{"devcontainer.json": json}), "devcontainer.json")
}

func hasDiag(s *Settings, lv Level, substr string) bool {
	for _, d := range s.Diagnostics {
		if d.Level == lv && strings.Contains(d.Msg, substr) {
			return true
		}
	}
	return false
}

func TestDiscover(t *testing.T) {
	t.Run("single at root", func(t *testing.T) {
		got, err := Discover(mapfs(map[string]string{"devcontainer.json": "{}"}), "")
		if err != nil || got != "devcontainer.json" {
			t.Fatalf("got %q, %v", got, err)
		}
	})
	t.Run("single under .devcontainer", func(t *testing.T) {
		got, err := Discover(mapfs(map[string]string{".devcontainer/devcontainer.json": "{}"}), "")
		if err != nil || got != ".devcontainer/devcontainer.json" {
			t.Fatalf("got %q, %v", got, err)
		}
	})
	t.Run("dotfile form", func(t *testing.T) {
		got, err := Discover(mapfs(map[string]string{".devcontainer.json": "{}"}), "")
		if err != nil || got != ".devcontainer.json" {
			t.Fatalf("got %q, %v", got, err)
		}
	})
	t.Run("none", func(t *testing.T) {
		if _, err := Discover(mapfs(nil), ""); err == nil || !strings.Contains(err.Error(), "not found") {
			t.Fatalf("want not found, got %v", err)
		}
	})
	t.Run("multiple is ambiguous", func(t *testing.T) {
		fsys := mapfs(map[string]string{
			"devcontainer.json":               "{}",
			".devcontainer/devcontainer.json": "{}",
		})
		if _, err := Discover(fsys, ""); err == nil || !strings.Contains(err.Error(), "multiple") {
			t.Fatalf("want multiple error, got %v", err)
		}
	})
	t.Run("explicit wins even with multiple", func(t *testing.T) {
		fsys := mapfs(map[string]string{
			"devcontainer.json":               "{}",
			".devcontainer/devcontainer.json": "{}",
			"images/a/devcontainer.json":      "{}",
		})
		got, err := Discover(fsys, "images/a/devcontainer.json")
		if err != nil || got != "images/a/devcontainer.json" {
			t.Fatalf("got %q, %v", got, err)
		}
	})
	t.Run("explicit outside context is rejected", func(t *testing.T) {
		if _, err := Discover(mapfs(nil), "../x.json"); err == nil {
			t.Fatal("want error for ..")
		}
	})
	t.Run("explicit missing", func(t *testing.T) {
		if _, err := Discover(mapfs(nil), "nope.json"); err == nil {
			t.Fatal("want error")
		}
	})
}

func TestLoadClassifiesArgs(t *testing.T) {
	s, err := load(t, `{
		// JSONC のコメントも読める
		"remoteUser": "vscode",
		"containerUser": "app",
		"build": {
			"dockerfile": "../Dockerfile.devcontainer",
			"args": {
				"BUILD_BASE_IMAGE": "debian:12-slim@sha256:abc",
				"BUILD_USER": "node",
				"BUILD_OVERWRITE": "false",
				"BUILD_SCRIPT_TIMEOUT": "15m",
				"BUILD_PACKAGES": "git, curl\njq@1.6",
				"BUILD_PACKAGES_APT": "build-essential",
				"JQ_VERSION": "1.6-2.1",
				"APP_ENV": "development"
			}
		}
	}`)
	if err != nil {
		t.Fatal(err)
	}
	if s.Overwrite {
		t.Error("Overwrite should be false")
	}
	if s.ScriptTimeout != 15*time.Minute {
		t.Errorf("ScriptTimeout = %v", s.ScriptTimeout)
	}
	if s.BuildUser != "node" || s.RemoteUser != "vscode" || s.ContainerUser != "app" {
		t.Errorf("users = %q %q %q", s.BuildUser, s.RemoteUser, s.ContainerUser)
	}
	if got := strings.Join(s.UserCandidates(), ","); got != "vscode,app,node" {
		t.Errorf("UserCandidates = %q", got)
	}
	if got := strings.Join(s.Packages["default"], ","); got != "git,curl,jq@1.6" {
		t.Errorf("default packages = %q", got)
	}
	if got := strings.Join(s.Packages["apt"], ","); got != "build-essential" {
		t.Errorf("apt packages = %q", got)
	}
	if len(s.Vars) != 2 || s.Vars["JQ_VERSION"] != "1.6-2.1" || s.Vars["APP_ENV"] != "development" {
		t.Errorf("Vars = %v (BUILD_* must not become template variables)", s.Vars)
	}
	if s.Dir != "." || s.Path != "devcontainer.json" {
		t.Errorf("Path/Dir = %q/%q", s.Path, s.Dir)
	}
}

func TestLoadDefaults(t *testing.T) {
	s, err := load(t, `{"build": {"dockerfile": "Dockerfile"}}`)
	if err != nil {
		t.Fatal(err)
	}
	if !s.Overwrite {
		t.Error("Overwrite must default to true")
	}
	if s.ScriptTimeout != DefaultScriptTimeout {
		t.Errorf("ScriptTimeout = %v", s.ScriptTimeout)
	}
	if len(s.Vars) != 0 || len(s.Packages) != 0 {
		t.Errorf("Vars/Packages should be empty: %v %v", s.Vars, s.Packages)
	}
}

func TestLoadDirFromNestedPath(t *testing.T) {
	fsys := mapfs(map[string]string{"images/py/.devcontainer/devcontainer.json": `{}`})
	s, err := Load(fsys, "images/py/.devcontainer/devcontainer.json")
	if err != nil {
		t.Fatal(err)
	}
	if s.Dir != "images/py/.devcontainer" {
		t.Errorf("Dir = %q", s.Dir)
	}
}

func TestLoadErrors(t *testing.T) {
	tests := []struct {
		name string
		json string
		want string
	}{
		{"reserved DCB_ prefix",
			`{"build":{"args":{"DCB_USER":"x"}}}`, "reserved"},
		{"substitution in template variable",
			`{"build":{"args":{"TOKEN":"${localEnv:TOKEN}"}}}`, "${...}"},
		{"substitution in packages",
			`{"build":{"args":{"BUILD_PACKAGES":"jq@${localEnv:V}"}}}`, "${...}"},
		{"substitution in BUILD_USER",
			`{"build":{"args":{"BUILD_USER":"${localEnv:U}"}}}`, "${...}"},
		{"substitution in remoteUser",
			`{"remoteUser":"${localEnv:U}"}`, "remoteUser"},
		{"unknown package family",
			`{"build":{"args":{"BUILD_PACKAGES_PACMAN":"git"}}}`, "unknown key"},
		{"typo of BUILD_PACKAGES",
			`{"build":{"args":{"BUILD_PACKAGESX":"git"}}}`, "unknown key"},
		{"bad boolean",
			`{"build":{"args":{"BUILD_OVERWRITE":"maybe"}}}`, "boolean"},
		{"bad duration",
			`{"build":{"args":{"BUILD_SCRIPT_TIMEOUT":"soon"}}}`, "duration"},
		{"non-positive duration",
			`{"build":{"args":{"BUILD_SCRIPT_TIMEOUT":"0s"}}}`, "duration"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := load(t, tt.json)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("want error containing %q, got %v", tt.want, err)
			}
		})
	}
}

func TestLoadAllowsSubstitutionInImages(t *testing.T) {
	// BUILD_BASE_IMAGE と BUILD_APPLY_IMAGE は Dockerfile が使う。apply は読まないので、${...} があってもよい。
	_, err := load(t, `{"build":{"args":{
		"BUILD_BASE_IMAGE":"${localEnv:BASE}",
		"BUILD_APPLY_IMAGE":"${localEnv:APPLY}"}}}`)
	if err != nil {
		t.Fatal(err)
	}
}

func TestLoadCollectsMultipleErrors(t *testing.T) {
	_, err := load(t, `{"build":{"args":{"DCB_A":"1","BUILD_OVERWRITE":"maybe"}}}`)
	if err == nil {
		t.Fatal("want error")
	}
	if !strings.Contains(err.Error(), "reserved") || !strings.Contains(err.Error(), "boolean") {
		t.Errorf("both problems should be reported, got %v", err)
	}
}

func TestLoadWarnings(t *testing.T) {
	t.Run("features", func(t *testing.T) {
		s, err := load(t, `{
			"build":{"dockerfile":"Dockerfile"},
			"features":{"ghcr.io/devcontainers/features/common-utils:2":{}},
			"overrideFeatureInstallOrder":["ghcr.io/devcontainers/features/common-utils"]
		}`)
		if err != nil {
			t.Fatal(err)
		}
		if !hasDiag(s, LevelWarn, "features (ghcr.io/devcontainers/features/common-utils:2)") {
			t.Errorf("want features warning, got %v", s.Diagnostics)
		}
		if !hasDiag(s, LevelWarn, "overrideFeatureInstallOrder") {
			t.Errorf("want overrideFeatureInstallOrder warning, got %v", s.Diagnostics)
		}
	})
	t.Run("dockerComposeFile", func(t *testing.T) {
		s, err := load(t, `{"dockerComposeFile":"compose.yaml","service":"app"}`)
		if err != nil {
			t.Fatal(err)
		}
		if !hasDiag(s, LevelWarn, "dockerComposeFile") {
			t.Errorf("want dockerComposeFile warning, got %v", s.Diagnostics)
		}
	})
	t.Run("deprecated BUILD_LANG", func(t *testing.T) {
		s, err := load(t, `{"build":{"args":{"BUILD_LANG":"ja_JP.UTF-8"}}}`)
		if err != nil {
			t.Fatal(err)
		}
		if !hasDiag(s, LevelWarn, "BUILD_LANG") {
			t.Errorf("want BUILD_LANG warning, got %v", s.Diagnostics)
		}
	})
	t.Run("unknown BUILD_ key", func(t *testing.T) {
		s, err := load(t, `{"build":{"args":{"BUILD_OVERWRTE":"false"}}}`)
		if err != nil {
			t.Fatal(err)
		}
		if !hasDiag(s, LevelWarn, "BUILD_OVERWRTE") {
			t.Errorf("want unknown key warning, got %v", s.Diagnostics)
		}
		if !s.Overwrite {
			t.Error("a typo must not change the setting")
		}
	})
	t.Run("invalid variable name", func(t *testing.T) {
		s, err := load(t, `{"build":{"args":{"my-var":"x"}}}`)
		if err != nil {
			t.Fatal(err)
		}
		if !hasDiag(s, LevelWarn, "my-var") || len(s.Vars) != 0 {
			t.Errorf("want warning and no variable, got %v / %v", s.Diagnostics, s.Vars)
		}
	})
}

func TestLoadSyntaxError(t *testing.T) {
	if _, err := load(t, `{ not json`); err == nil {
		t.Fatal("want parse error")
	}
}

func TestLoadRejectsHugeFile(t *testing.T) {
	big := `{"name":"` + strings.Repeat("a", maxConfigSize) + `"}`
	if _, err := load(t, big); err == nil || !strings.Contains(err.Error(), "larger") {
		t.Fatalf("want size error, got %v", err)
	}
}
