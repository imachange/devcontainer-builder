package sysinfo

import (
	"errors"
	"io/fs"
	"os/user"
	"strings"
	"testing"
	"testing/fstest"
)

func exe() *fstest.MapFile          { return &fstest.MapFile{Data: []byte("#!/bin/sh\n"), Mode: 0o755} }
func file(s string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(s), Mode: 0o644} }

func probe(fsys fs.FS) Probe {
	return Probe{
		FS:      fsys,
		PathEnv: "/usr/local/bin:/usr/bin:/bin",
		Arch:    "amd64",
		Getenv:  func(string) string { return "" },
		LookupUser: func(string) (*user.User, error) {
			return nil, errors.New("not found")
		},
	}
}

func TestDetectDistros(t *testing.T) {
	tests := []struct {
		name      string
		fsys      fstest.MapFS
		wantID    string
		wantFam   string
		wantMgr   string
		wantFound int
	}{
		{
			name: "debian",
			fsys: fstest.MapFS{
				"etc/os-release":  file("ID=debian\nVERSION_ID=\"12\"\n"),
				"usr/bin/apt-get": exe(),
			},
			wantID: "debian", wantFam: "apt", wantMgr: "apt-get", wantFound: 1,
		},
		{
			name: "alpine",
			fsys: fstest.MapFS{
				"etc/os-release": file("ID=alpine\n"),
				"sbin/apk":       exe(),
			},
			wantID: "alpine", wantFam: "apk", wantMgr: "apk", wantFound: 1,
		},
		{
			name: "ubi minimal (microdnf only)",
			fsys: fstest.MapFS{
				"etc/os-release":   file("ID=\"rhel\"\nID_LIKE=\"fedora\"\n"),
				"usr/bin/microdnf": exe(),
			},
			wantID: "rhel", wantFam: "dnf", wantMgr: "microdnf", wantFound: 1,
		},
		{
			name: "fedora (dnf and yum)",
			fsys: fstest.MapFS{
				"etc/os-release": file("ID=fedora\n"),
				"usr/bin/dnf":    exe(),
				"usr/bin/yum":    exe(),
			},
			wantID: "fedora", wantFam: "dnf", wantMgr: "dnf", wantFound: 2,
		},
		{
			name: "opensuse leap",
			fsys: fstest.MapFS{
				"etc/os-release": file("ID=\"opensuse-leap\"\nID_LIKE=\"suse opensuse\"\n"),
				"usr/bin/zypper": exe(),
			},
			wantID: "opensuse-leap", wantFam: "zypper", wantMgr: "zypper", wantFound: 1,
		},
		{
			name: "no os-release, legacy alpine-release",
			fsys: fstest.MapFS{
				"etc/alpine-release": file("3.20.0\n"),
				"sbin/apk":           exe(),
			},
			wantID: "alpine", wantFam: "apk", wantMgr: "apk", wantFound: 1,
		},
		{
			name: "os-release under usr/lib",
			fsys: fstest.MapFS{
				"usr/lib/os-release": file("ID=ubuntu\n"),
				"usr/bin/apt-get":    exe(),
			},
			wantID: "ubuntu", wantFam: "apt", wantMgr: "apt-get", wantFound: 1,
		},
		{
			name: "os-release without ID",
			fsys: fstest.MapFS{
				"etc/os-release":  file("NAME=Foo\n"),
				"usr/bin/apt-get": exe(),
			},
			wantID: "linux", wantFam: "apt", wantMgr: "apt-get", wantFound: 1,
		},
		{
			name: "ID_LIKE chain (linuxmint)",
			fsys: fstest.MapFS{
				"etc/os-release":  file("ID=linuxmint\nID_LIKE=\"ubuntu debian\"\n"),
				"usr/bin/apt-get": exe(),
			},
			wantID: "linuxmint", wantFam: "apt", wantMgr: "apt-get", wantFound: 1,
		},
		{
			name: "both apt-get and dnf, os-release says debian",
			fsys: fstest.MapFS{
				"etc/os-release":  file("ID=debian\n"),
				"usr/bin/apt-get": exe(),
				"usr/bin/dnf":     exe(),
			},
			wantID: "debian", wantFam: "apt", wantMgr: "apt-get", wantFound: 2,
		},
		{
			name: "both apt-get and dnf, os-release says fedora",
			fsys: fstest.MapFS{
				"etc/os-release":  file("ID=fedora\n"),
				"usr/bin/apt-get": exe(),
				"usr/bin/dnf":     exe(),
			},
			wantID: "fedora", wantFam: "dnf", wantMgr: "dnf", wantFound: 2,
		},
		{
			name: "apt-get without exec permission is ignored",
			fsys: fstest.MapFS{
				"etc/os-release":  file("ID=debian\n"),
				"usr/bin/apt-get": file("not executable"),
			},
			wantID: "debian", wantFam: "apt", wantMgr: "", wantFound: 0,
		},
		{
			name:   "distroless-like (nothing at all)",
			fsys:   fstest.MapFS{},
			wantID: "", wantFam: "", wantMgr: "", wantFound: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := probe(tt.fsys).Detect("pre")
			if got.OSID != tt.wantID {
				t.Errorf("OSID = %q, want %q", got.OSID, tt.wantID)
			}
			if got.Family != tt.wantFam {
				t.Errorf("Family = %q, want %q", got.Family, tt.wantFam)
			}
			if got.PkgMgr != tt.wantMgr {
				t.Errorf("PkgMgr = %q, want %q", got.PkgMgr, tt.wantMgr)
			}
			if len(got.Found) != tt.wantFound {
				t.Errorf("Found = %v, want %d entries", got.Found, tt.wantFound)
			}
			if got.Phase != "pre" || got.Arch != "amd64" {
				t.Errorf("Phase/Arch = %q/%q", got.Phase, got.Arch)
			}
		})
	}
}

func TestSearchDirsIgnoresRelativeAndEmpty(t *testing.T) {
	fsys := fstest.MapFS{"relative/apt-get": exe()}
	p := probe(fsys)
	p.PathEnv = "relative::."
	if got := p.Detect("pre"); got.PkgMgr != "" {
		t.Errorf("relative PATH entry must be ignored, got %q", got.PkgMgr)
	}

	fsys = fstest.MapFS{"opt/tools/apt-get": exe()}
	p = probe(fsys)
	p.PathEnv = "/opt/tools"
	if got := p.Detect("pre"); got.PkgMgr != "apt-get" {
		t.Errorf("absolute PATH entry must be searched, got %q", got.PkgMgr)
	}
}

func TestDevUser(t *testing.T) {
	vscode := &user.User{Username: "vscode", Uid: "1000", Gid: "1000", HomeDir: "/home/vscode"}
	p := probe(fstest.MapFS{})
	p.LookupUser = func(name string) (*user.User, error) {
		if name == "vscode" {
			return vscode, nil
		}
		return nil, errors.New("not found")
	}

	tests := []struct {
		name     string
		cands    []string
		wantUser string
		wantHome string
	}{
		{"first non-empty wins", []string{"", "vscode", "node"}, "vscode", "/home/vscode"},
		{"user:group is normalized", []string{"vscode:vscode"}, "vscode", "/home/vscode"},
		{"nothing given falls back to root", nil, "root", "/root"},
		{"all empty falls back to root", []string{"", " "}, "root", "/root"},
		{"missing user keeps name, empty home", []string{"ghost"}, "ghost", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := p.Detect("post", tt.cands...)
			if got.User != tt.wantUser || got.Home != tt.wantHome {
				t.Errorf("User/Home = %q/%q, want %q/%q", got.User, got.Home, tt.wantUser, tt.wantHome)
			}
		})
	}
}

func TestLangComesFromEnvironment(t *testing.T) {
	p := probe(fstest.MapFS{})
	p.Getenv = func(k string) string {
		if k == "LANG" {
			return "ja_JP.UTF-8"
		}
		return ""
	}
	if got := p.Detect("pre"); got.Lang != "ja_JP.UTF-8" {
		t.Errorf("Lang = %q", got.Lang)
	}
	p.Getenv = func(string) string { return "" }
	if got := p.Detect("pre"); got.Lang != "" {
		t.Errorf("Lang must be empty when LANG is unset, got %q", got.Lang)
	}
}

func TestEnvAlwaysHasAllEightVariables(t *testing.T) {
	env := probe(fstest.MapFS{}).Detect("").Env()
	want := []string{
		"DCB_PHASE=", "DCB_OS_ID=", "DCB_OS_FAMILY=", "DCB_PKGMGR=",
		"DCB_ARCH=", "DCB_USER=", "DCB_HOME=", "DCB_LANG=",
	}
	if len(env) != len(want) {
		t.Fatalf("got %d variables, want %d: %v", len(env), len(want), env)
	}
	for i, prefix := range want {
		if !strings.HasPrefix(env[i], prefix) {
			t.Errorf("env[%d] = %q, want prefix %q", i, env[i], prefix)
		}
	}
}

func TestParseOSRelease(t *testing.T) {
	in := `# comment
ID=debian
ID_LIKE="a b"
PRETTY_NAME='Debian GNU/Linux'
ESCAPED="say \"hi\" \$HOME"

EMPTY=
`
	m := parseOSRelease(strings.NewReader(in))
	checks := map[string]string{
		"ID":          "debian",
		"ID_LIKE":     "a b",
		"PRETTY_NAME": "Debian GNU/Linux",
		"ESCAPED":     `say "hi" $HOME`,
		"EMPTY":       "",
	}
	for k, want := range checks {
		if got := m[k]; got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
}
