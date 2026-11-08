package pkgmgr

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/imachange/devcontainer-builder/internal/config"
	"github.com/imachange/devcontainer-builder/internal/layout"
	"github.com/imachange/devcontainer-builder/internal/sysinfo"
)

type MockRunner struct {
	Calls []string
	Fail  map[string]int
	count map[string]int
}

func (m *MockRunner) Run(ctx context.Context, cmd string, args []string, env []string) error {
	full := cmd + " " + strings.Join(args, " ")
	m.Calls = append(m.Calls, full)
	if m.Fail != nil {
		for prefix, maxFail := range m.Fail {
			if strings.HasPrefix(full, prefix) {
				if m.count == nil {
					m.count = map[string]int{}
				}
				m.count[prefix]++
				if m.count[prefix] <= maxFail {
					return errors.New("mock failure: " + full)
				}
			}
		}
	}
	return nil
}

func TestExpandVars(t *testing.T) {
	vars := map[string]string{"JQ_VER": "1.6", "ENV": "prod"}

	got, err := ExpandVars("jq@{{ JQ_VER }}", vars)
	if err != nil || got != "jq@1.6" {
		t.Fatalf("got %q, err %v", got, err)
	}

	_, err = ExpandVars("git@{{ MISSING }}", vars)
	if err == nil || !strings.Contains(err.Error(), "undefined template variable") {
		t.Fatalf("want error, got %v", err)
	}
}

func TestParseLine(t *testing.T) {
	tests := []struct {
		input   string
		want    Item
		wantErr bool
	}{
		{"git", Item{Name: "git", Version: ""}, false},
		{"curl@7.68.0", Item{Name: "curl", Version: "7.68.0"}, false},
		{"  jq@1.6  # comment ", Item{Name: "jq", Version: "1.6"}, false},
		{"# full comment", Item{}, false},
		{"@1.0", Item{}, true},
	}
	for _, tt := range tests {
		got, err := ParseLine(tt.input)
		if (err != nil) != tt.wantErr {
			t.Fatalf("ParseLine(%q) err = %v, wantErr = %v", tt.input, err, tt.wantErr)
		}
		if got != tt.want {
			t.Errorf("ParseLine(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestBuildPlanPriority(t *testing.T) {
	lay := &layout.Layout{
		Packages: map[string][]string{
			"default": {"git@1.0", "curl"},
			"apt":     {"build-essential", "git@2.0"},
		},
	}
	cfg := &config.Settings{
		Packages: map[string][]string{
			"default": {"jq@1.6"},
			"apt":     {"git@2.5"},
		},
		Vars: map[string]string{},
	}
	info := sysinfo.Info{Family: "apt", PkgMgr: "apt-get"}

	plan, err := BuildPlan(lay, cfg, info)
	if err != nil {
		t.Fatalf("BuildPlan error: %v", err)
	}

	wantNames := []string{"git", "curl", "build-essential", "jq"}
	var gotNames []string
	var gotGitVer string
	for _, item := range plan.Items {
		gotNames = append(gotNames, item.Name)
		if item.Name == "git" {
			gotGitVer = item.Version
		}
	}

	if !reflect.DeepEqual(gotNames, wantNames) {
		t.Errorf("Items order = %v, want %v", gotNames, wantNames)
	}
	if gotGitVer != "2.5" {
		t.Errorf("git version = %q, want 2.5", gotGitVer)
	}
}

func TestBuildPlanRejectDowngrade(t *testing.T) {
	lay := &layout.Layout{
		Packages: map[string][]string{
			"default": {"jq@1.6"},
		},
	}
	cfg := &config.Settings{
		Packages: map[string][]string{
			"default": {"jq@1.5"},
		},
	}
	info := sysinfo.Info{Family: "apt", PkgMgr: "apt-get"}

	_, err := BuildPlan(lay, cfg, info)
	if err == nil || !strings.Contains(err.Error(), "downgrade") {
		t.Fatalf("want downgrade rejection error, got %v", err)
	}
}

func TestManagerSpecs(t *testing.T) {
	item := Item{Name: "git", Version: "2.34.1"}

	apt := &AptManager{}
	if got := apt.FormatPackage(item); got != "git=2.34.1" {
		t.Errorf("AptManager format = %q", got)
	}
	if spec := apt.PreInstallSpec(); spec == nil || spec.Cmd != "apt-get" {
		t.Errorf("AptManager PreInstallSpec failed")
	}

	dnf := &DnfManager{Cmd: "dnf"}
	if got := dnf.FormatPackage(item); got != "git-2.34.1" {
		t.Errorf("DnfManager format = %q", got)
	}
	if spec := dnf.PreInstallSpec(); spec != nil {
		t.Errorf("DnfManager should not have PreInstallSpec")
	}

	apk := &ApkManager{}
	if got := apk.FormatPackage(item); got != "git=2.34.1" {
		t.Errorf("ApkManager format = %q", got)
	}
	if spec := apk.PostInstallCleanSpec(); spec != nil {
		t.Errorf("ApkManager should not have PostInstallCleanSpec")
	}

	zypper := &ZypperManager{}
	if got := zypper.FormatPackage(item); got != "git=2.34.1" {
		t.Errorf("ZypperManager format = %q", got)
	}
}

func TestExecutorWithRetry(t *testing.T) {
	plan := &Plan{
		Family: "apt",
		PkgMgr: "apt-get",
		Items:  []Item{{Name: "git", Version: "2.0"}, {Name: "curl"}},
	}
	runner := &MockRunner{
		Fail: map[string]int{
			"apt-get install": 1,
		},
	}

	err := Install(context.Background(), plan, runner)
	if err != nil {
		t.Fatalf("Install failed: %v", err)
	}

	wantCalls := []string{
		"apt-get update",
		"apt-get install -y --no-install-recommends git=2.0 curl",
		"apt-get install -y --no-install-recommends git=2.0 curl",
		"apt-get clean",
	}
	if !reflect.DeepEqual(runner.Calls, wantCalls) {
		t.Errorf("Calls = %v, want %v", runner.Calls, wantCalls)
	}
}
