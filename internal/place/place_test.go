package place

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/imachange/devcontainer-builder/internal/config"
	"github.com/imachange/devcontainer-builder/internal/layout"
	"github.com/imachange/devcontainer-builder/internal/sysinfo"
)

func TestBuildPlanAndExecute(t *testing.T) {
	stubUser(t, "alice")
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

// ---- ヘルパー

// stubUser は、passwd の代わりに、指定した 1 ユーザー (uid/gid 1000) だけを返す。
func stubUser(t *testing.T, name string) {
	t.Helper()
	orig := lookupUser
	lookupUser = func(n string) (*user.User, error) {
		if n == name {
			return &user.User{Username: name, Uid: "1000", Gid: "1000", HomeDir: "/home/" + name}, nil
		}
		return nil, errors.New("unknown user " + n)
	}
	t.Cleanup(func() { lookupUser = orig })
}

func mustWrite(t *testing.T, path, content string, perm os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), perm); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, perm); err != nil {
		t.Fatal(err)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func modeOf(t *testing.T, path string) os.FileMode {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}

// plant は、target/files/<tree>/ の下に、ファイルを作り、target のパスを返す。
func plant(t *testing.T, tree string, files map[string]string) string {
	t.Helper()
	target := filepath.Join(t.TempDir(), "target")
	base := filepath.Join(target, "files", tree)
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	for rel, content := range files {
		mustWrite(t, filepath.Join(base, filepath.FromSlash(rel)), content, 0o644)
	}
	return target
}

type result struct {
	root string
	plan *Plan
	log  *bytes.Buffer
	err  error
}

// execRoot は、files/root に files を置き、一時ディレクトリをルートにして、計画と実行を行う。
func execRoot(t *testing.T, files map[string]string, prep func(root string), cfg *config.Settings) result {
	t.Helper()
	target := plant(t, "root", files)
	root := t.TempDir()
	if prep != nil {
		prep(root)
	}
	if cfg == nil {
		cfg = &config.Settings{Overwrite: true, Vars: map[string]string{}}
	}
	plan, err := BuildPlan(&layout.Layout{FileTrees: []string{"root"}}, cfg, sysinfo.Info{User: "root"}, target, root)
	if err != nil {
		return result{root: root, err: err}
	}
	logBuf := &bytes.Buffer{}
	plan.Log = logBuf
	return result{root: root, plan: plan, log: logBuf, err: Execute(context.Background(), plan, cfg.Vars)}
}

// ---- 拒否リスト

func TestDeniedPathCannotBeBypassedWithSuffixes(t *testing.T) {
	// 拡張子 (.append / .tmpl) を付けても、拒否リストをすり抜けられない。
	for _, name := range []string{
		"etc/shadow.append", "etc/passwd.tmpl", "etc/group.tmpl.append",
		"etc/gshadow.append.tmpl", "etc/sudoers.append", "etc/ld.so.preload.tmpl",
		"etc/shadow-.append", "proc/x.append",
	} {
		t.Run(name, func(t *testing.T) {
			r := execRoot(t, map[string]string{name: "x\n"}, nil, nil)
			if r.err == nil || !strings.Contains(r.err.Error(), "denied path") {
				t.Fatalf("want denied path error, got %v", r.err)
			}
		})
	}
}

func TestSudoersDirectoryIsAllowed(t *testing.T) {
	r := execRoot(t, map[string]string{"etc/sudoers.d/dev": "dev ALL=(ALL) NOPASSWD:ALL\n"}, nil, nil)
	if r.err != nil {
		t.Fatal(r.err)
	}
	if got := modeOf(t, filepath.Join(r.root, "etc/sudoers.d/dev")); got != 0o440 {
		t.Errorf("mode = %o, want 0440", got)
	}
}

func TestSymlinkInSourceIsRejected(t *testing.T) {
	target := plant(t, "root", map[string]string{"etc/a": "x"})
	if err := os.Symlink("a", filepath.Join(target, "files", "root", "etc", "link")); err != nil {
		t.Skip("symlinks are not available")
	}
	_, err := BuildPlan(&layout.Layout{FileTrees: []string{"root"}}, nil, sysinfo.Info{User: "root"}, target, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "unsupported file type") {
		t.Fatalf("want unsupported file type, got %v", err)
	}
}

func TestSetuidSourceIsRejected(t *testing.T) {
	target := plant(t, "root", nil)
	p := filepath.Join(target, "files", "root", "tool")
	mustWrite(t, p, "x", 0o755)
	if err := os.Chmod(p, 0o755|os.ModeSetuid); err != nil {
		t.Skip("setuid bit is not supported here")
	}
	_, err := BuildPlan(&layout.Layout{FileTrees: []string{"root"}}, nil, sysinfo.Info{User: "root"}, target, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "setuid") {
		t.Fatalf("want setuid error, got %v", err)
	}
}

func TestDestinationSymlinkIsNotFollowed(t *testing.T) {
	r := execRoot(t, map[string]string{"etc/link": "pwned\n"}, func(root string) {
		mustWrite(t, filepath.Join(root, "etc/shadow"), "secret\n", 0o600)
		if err := os.Symlink("shadow", filepath.Join(root, "etc/link")); err != nil {
			t.Skip("symlinks are not available")
		}
	}, nil)
	if r.err == nil {
		t.Fatal("writing through a symlink must fail")
	}
	if got := mustRead(t, filepath.Join(r.root, "etc/shadow")); got != "secret\n" {
		t.Errorf("the symlink target was modified: %q", got)
	}
}

func TestParentSymlinkOutsideRootIsRejected(t *testing.T) {
	outside := t.TempDir()
	r := execRoot(t, map[string]string{"etc/x": "x\n"}, func(root string) {
		if err := os.Symlink(outside, filepath.Join(root, "etc")); err != nil {
			t.Skip("symlinks are not available")
		}
	}, nil)
	if r.err == nil || !strings.Contains(r.err.Error(), "outside the root") {
		t.Fatalf("want an outside-the-root error, got %v", r.err)
	}
	if ents, _ := os.ReadDir(outside); len(ents) != 0 {
		t.Errorf("nothing may be written outside the root: %v", ents)
	}
}

func TestMergedUsrSymlinkDirectoryIsAllowed(t *testing.T) {
	// /bin -> usr/bin のような、実体が安全な symlink のディレクトリには、置ける。
	r := execRoot(t, map[string]string{"bin/tool": "#!/bin/sh\n"}, func(root string) {
		if err := os.MkdirAll(filepath.Join(root, "usr/bin"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("usr/bin", filepath.Join(root, "bin")); err != nil {
			t.Skip("symlinks are not available")
		}
	}, nil)
	if r.err != nil {
		t.Fatal(r.err)
	}
	if got := mustRead(t, filepath.Join(r.root, "usr/bin/tool")); got != "#!/bin/sh\n" {
		t.Errorf("content = %q", got)
	}
}

// ---- 権限

func TestPermissionsInHomeTree(t *testing.T) {
	stubUser(t, "alice")
	target := plant(t, "home", map[string]string{
		".ssh/id_ed25519":     "KEY",
		".ssh/id_ed25519.pub": "PUB",
		".ssh/config":         "Host *",
		".gnupg/pubring.kbx":  "x",
		".netrc":              "machine x",
		".config/app/a.toml":  "a = 1",
		"notes.txt":           "n",
	})
	script := filepath.Join(target, "files", "home", "bin", "run.sh")
	mustWrite(t, script, "#!/bin/sh\n", 0o755)

	plan, err := BuildPlan(&layout.Layout{FileTrees: []string{"home"}}, nil,
		sysinfo.Info{User: "alice", Home: "/home/alice"}, target, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]os.FileMode{
		".ssh":                0o700,
		".ssh/id_ed25519":     0o600,
		".ssh/id_ed25519.pub": 0o644,
		".ssh/config":         0o600,
		".gnupg":              0o700,
		".gnupg/pubring.kbx":  0o600,
		".netrc":              0o600,
		".config":             0o755,
		".config/app/a.toml":  0o644,
		"notes.txt":           0o644,
		"bin":                 0o755,
		"bin/run.sh":          0o755,
	}
	got := map[string]os.FileMode{}
	for _, a := range plan.Actions {
		got[filepath.ToSlash(a.RelPath)] = a.Perm
		if a.UID != 1000 || a.GID != 1000 {
			t.Errorf("%s: owner = %d:%d, want 1000:1000", a.RelPath, a.UID, a.GID)
		}
	}
	for rel, perm := range want {
		if got[rel] != perm {
			t.Errorf("%s: perm = %04o, want %04o", rel, got[rel], perm)
		}
	}
}

func TestPermissionsForRootAndHomeDirectoriesInRootTree(t *testing.T) {
	target := plant(t, "root", map[string]string{
		"root/.ssh/authorized_keys": "k",
		"home/bob/.ssh/id_rsa":      "k",
		"etc/sudoers.d/x":           "x",
		"etc/hosts.d/y":             "y",
	})
	plan, err := BuildPlan(&layout.Layout{FileTrees: []string{"root"}}, nil, sysinfo.Info{User: "root"}, target, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]os.FileMode{
		"root/.ssh/authorized_keys": 0o600,
		"home/bob/.ssh/id_rsa":      0o600,
		"home/bob/.ssh":             0o700,
		"etc/sudoers.d/x":           0o440,
		"etc/hosts.d/y":             0o644,
	}
	for _, a := range plan.Actions {
		if w, ok := want[filepath.ToSlash(a.RelPath)]; ok && a.Perm != w {
			t.Errorf("%s: perm = %04o, want %04o", a.RelPath, a.Perm, w)
		}
	}
}

// ---- 順序と重複

func TestReplaceRunsBeforeAppend(t *testing.T) {
	// 辞書順では .append が .tmpl より先になるが、置換してから追記される。
	r := execRoot(t, map[string]string{
		"etc/app.conf.tmpl":   "base={{ A }}\n",
		"etc/app.conf.append": "extra\n",
	}, nil, &config.Settings{Overwrite: true, Vars: map[string]string{"A": "1"}})
	if r.err != nil {
		t.Fatal(r.err)
	}
	if got := mustRead(t, filepath.Join(r.root, "etc/app.conf")); got != "base=1\nextra\n" {
		t.Errorf("content = %q", got)
	}
}

func TestDuplicateDestinationsAreRejected(t *testing.T) {
	r := execRoot(t, map[string]string{"etc/x": "1", "etc/x.tmpl": "2"}, nil, nil)
	if r.err == nil || !strings.Contains(r.err.Error(), "both replace") {
		t.Errorf("want a duplicate error, got %v", r.err)
	}
	r = execRoot(t, map[string]string{"etc/y.append": "1", "etc/y.tmpl.append": "2"}, nil, nil)
	if r.err == nil || !strings.Contains(r.err.Error(), "both append") {
		t.Errorf("want a duplicate error, got %v", r.err)
	}
}

func TestDirectoryAndFileCollisionIsRejected(t *testing.T) {
	target := plant(t, "root", map[string]string{"etc/x/inner": "1"})
	mustWrite(t, filepath.Join(target, "files", "root", "etc", "x.tmpl"), "2", 0o644)
	_, err := BuildPlan(&layout.Layout{FileTrees: []string{"root"}}, nil, sysinfo.Info{User: "root"}, target, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "directory and a file") {
		t.Fatalf("want a collision error, got %v", err)
	}
}

// ---- 追記とマージ

func TestAppendKeepsExistingModeAndContent(t *testing.T) {
	r := execRoot(t, map[string]string{"etc/secret.append": "b\n"}, func(root string) {
		mustWrite(t, filepath.Join(root, "etc/secret"), "a\n", 0o600)
	}, nil)
	if r.err != nil {
		t.Fatal(r.err)
	}
	p := filepath.Join(r.root, "etc/secret")
	if got := mustRead(t, p); got != "a\nb\n" {
		t.Errorf("content = %q", got)
	}
	if got := modeOf(t, p); got != 0o600 {
		t.Errorf("appending must not loosen the mode: %04o", got)
	}
}

func TestAppendIsIdempotent(t *testing.T) {
	r := execRoot(t, map[string]string{"etc/rc.append": "export A=1\n"}, func(root string) {
		mustWrite(t, filepath.Join(root, "etc/rc"), "# rc\n", 0o644)
	}, nil)
	if r.err != nil {
		t.Fatal(r.err)
	}
	// 適用済みのイメージの上で、もう一度実行する。
	if err := Execute(context.Background(), r.plan, nil); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, filepath.Join(r.root, "etc/rc")); got != "# rc\nexport A=1\n" {
		t.Errorf("the second run must not append again: %q", got)
	}
}

func TestAppendDoesNotMatchInTheMiddleOfALine(t *testing.T) {
	r := execRoot(t, map[string]string{"etc/f.append": "foo\n"}, func(root string) {
		mustWrite(t, filepath.Join(root, "etc/f"), "xfoo\n", 0o644)
	}, nil)
	if r.err != nil {
		t.Fatal(r.err)
	}
	if got := mustRead(t, filepath.Join(r.root, "etc/f")); got != "xfoo\nfoo\n" {
		t.Errorf("content = %q", got)
	}
}

func TestAppendAddsMissingNewline(t *testing.T) {
	r := execRoot(t, map[string]string{"etc/f.append": "b\n"}, func(root string) {
		mustWrite(t, filepath.Join(root, "etc/f"), "a", 0o644)
	}, nil)
	if r.err != nil {
		t.Fatal(r.err)
	}
	if got := mustRead(t, filepath.Join(r.root, "etc/f")); got != "a\nb\n" {
		t.Errorf("content = %q", got)
	}
}

func TestAppendCreatesMissingFile(t *testing.T) {
	r := execRoot(t, map[string]string{"etc/new.append": "x\n"}, nil, nil)
	if r.err != nil {
		t.Fatal(r.err)
	}
	if got := mustRead(t, filepath.Join(r.root, "etc/new")); got != "x\n" {
		t.Errorf("content = %q", got)
	}
}

func TestStructuredAppendMergesAndIsIdempotent(t *testing.T) {
	r := execRoot(t, map[string]string{"etc/app.json.append": `{"b": 2}`}, func(root string) {
		mustWrite(t, filepath.Join(root, "etc/app.json"), `{"a": 1}`, 0o640)
	}, nil)
	if r.err != nil {
		t.Fatal(r.err)
	}
	p := filepath.Join(r.root, "etc/app.json")
	first := mustRead(t, p)
	if !strings.Contains(first, `"a": 1`) || !strings.Contains(first, `"b": 2`) {
		t.Fatalf("merge result = %q", first)
	}
	if got := modeOf(t, p); got != 0o640 {
		t.Errorf("mode = %04o, want 0640", got)
	}
	if err := Execute(context.Background(), r.plan, nil); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, p); got != first {
		t.Errorf("the second run must not change the file:\n%s\n---\n%s", first, got)
	}
}

func TestStructuredAppendRejectsInvalidPatchForNewFile(t *testing.T) {
	r := execRoot(t, map[string]string{"etc/new.json.append": "{not json"}, nil, nil)
	if r.err == nil {
		t.Fatal("an invalid patch must be rejected")
	}
	if _, err := os.Stat(filepath.Join(r.root, "etc/new.json")); err == nil {
		t.Error("an invalid file must not be created")
	}
}

func TestStructuredAppendFailsOnBrokenExistingFile(t *testing.T) {
	r := execRoot(t, map[string]string{"etc/c.yaml.append": "b: 2\n"}, func(root string) {
		mustWrite(t, filepath.Join(root, "etc/c.yaml"), "a: 1\n---\nz: 9\n", 0o644)
	}, nil)
	if r.err == nil || !strings.Contains(r.err.Error(), "multiple YAML documents") {
		t.Fatalf("want a multi-document error, got %v", r.err)
	}
	if got := mustRead(t, filepath.Join(r.root, "etc/c.yaml")); got != "a: 1\n---\nz: 9\n" {
		t.Errorf("the existing file must stay untouched: %q", got)
	}
}

// ---- 置換と上書き

func TestOverwriteForbiddenAndLogged(t *testing.T) {
	files := map[string]string{"etc/a": "new\n"}
	prep := func(root string) { mustWrite(t, filepath.Join(root, "etc/a"), "old\n", 0o644) }

	r := execRoot(t, files, prep, &config.Settings{Overwrite: false})
	if r.err == nil || !strings.Contains(r.err.Error(), "forbidden (BUILD_OVERWRITE=false)") {
		t.Fatalf("want a forbidden error, got %v", r.err)
	}
	if got := mustRead(t, filepath.Join(r.root, "etc/a")); got != "old\n" {
		t.Errorf("the file must stay untouched: %q", got)
	}

	r = execRoot(t, files, prep, &config.Settings{Overwrite: true})
	if r.err != nil {
		t.Fatal(r.err)
	}
	if got := mustRead(t, filepath.Join(r.root, "etc/a")); got != "new\n" {
		t.Errorf("content = %q", got)
	}
	if !strings.Contains(r.log.String(), "replaced existing file") {
		t.Errorf("a replacement should be logged: %q", r.log.String())
	}
}

func TestReplaceAppliesNewModeToExistingFile(t *testing.T) {
	r := execRoot(t, map[string]string{"etc/sudoers.d/dev": "x\n"}, func(root string) {
		mustWrite(t, filepath.Join(root, "etc/sudoers.d/dev"), "old\n", 0o666)
	}, nil)
	if r.err != nil {
		t.Fatal(r.err)
	}
	if got := modeOf(t, filepath.Join(r.root, "etc/sudoers.d/dev")); got != 0o440 {
		t.Errorf("a replaced file gets the new mode: %04o", got)
	}
}

func TestExistingDirectoriesAreLeftAlone(t *testing.T) {
	r := execRoot(t, map[string]string{"etc/app/x": "x\n"}, func(root string) {
		if err := os.MkdirAll(filepath.Join(root, "etc"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(filepath.Join(root, "etc"), 0o700); err != nil {
			t.Fatal(err)
		}
	}, nil)
	if r.err != nil {
		t.Fatal(r.err)
	}
	if got := modeOf(t, filepath.Join(r.root, "etc")); got != 0o700 {
		t.Errorf("an existing directory must keep its mode: %04o", got)
	}
}

func TestTemplateUndefinedVariableFails(t *testing.T) {
	r := execRoot(t, map[string]string{"etc/a.tmpl": "{{ NOPE }}"}, nil, nil)
	if r.err == nil || !strings.Contains(r.err.Error(), "undefined template variable") {
		t.Fatalf("want an undefined-variable error, got %v", r.err)
	}
}

func TestTemplateLeavesGoTemplateSyntaxAlone(t *testing.T) {
	r := execRoot(t, map[string]string{"etc/a.tmpl": "{{ .Values.x }} {{ V }}\n"}, nil,
		&config.Settings{Overwrite: true, Vars: map[string]string{"V": "1"}})
	if r.err != nil {
		t.Fatal(r.err)
	}
	if got := mustRead(t, filepath.Join(r.root, "etc/a")); got != "{{ .Values.x }} 1\n" {
		t.Errorf("content = %q", got)
	}
}

// ---- files/home とホームの作成

func TestHomePendingIsNotExecutable(t *testing.T) {
	target := plant(t, "home", map[string]string{".bashrc.append": "x\n"})
	plan, err := BuildPlan(&layout.Layout{FileTrees: []string{"home"}}, nil,
		sysinfo.Info{User: "alice"}, target, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !plan.HomePending {
		t.Fatal("HomePending should be set when the home is unknown")
	}
	if err := Execute(context.Background(), plan, nil); !errors.Is(err, ErrHomeUnresolved) {
		t.Fatalf("want ErrHomeUnresolved, got %v", err)
	}
}

func TestHomePendingStillValidatesNames(t *testing.T) {
	target := plant(t, "home", map[string]string{"x.append.append": "x\n"})
	_, err := BuildPlan(&layout.Layout{FileTrees: []string{"home"}}, nil, sysinfo.Info{User: "alice"}, target, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "duplicated") {
		t.Fatalf("want a naming error even when the home is pending, got %v", err)
	}
}

func TestCreateHomeLikeUseradd(t *testing.T) {
	stubUser(t, "bob")
	old := syscall.Umask(0o022)
	t.Cleanup(func() { syscall.Umask(old) })

	target := plant(t, "home", map[string]string{".profile.append": "export X=1\n"})
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "etc/login.defs"), "# comment\nHOME_MODE 0750\n", 0o644)
	mustWrite(t, filepath.Join(root, "etc/skel/.bashrc"), "# skel bashrc\n", 0o644)
	mustWrite(t, filepath.Join(root, "etc/skel/.config/keep"), "k\n", 0o600)

	plan, err := BuildPlan(&layout.Layout{FileTrees: []string{"home"}}, nil,
		sysinfo.Info{User: "bob", Home: "/home/bob"}, target, root)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.CreateHome {
		t.Fatal("CreateHome should be set")
	}
	if err := Execute(context.Background(), plan, nil); err != nil {
		t.Fatal(err)
	}

	home := filepath.Join(root, "home/bob")
	if got := modeOf(t, home); got != 0o750 {
		t.Errorf("home mode = %04o, want 0750 (HOME_MODE)", got)
	}
	if got := modeOf(t, filepath.Join(root, "home")); got != 0o755 {
		t.Errorf("the parent of the home must be 0755, got %04o", got)
	}
	if got := mustRead(t, filepath.Join(home, ".bashrc")); got != "# skel bashrc\n" {
		t.Errorf("skel was not copied: %q", got)
	}
	if got := modeOf(t, filepath.Join(home, ".config/keep")); got != 0o600 {
		t.Errorf("skel modes must be kept: %04o", got)
	}
	if got := mustRead(t, filepath.Join(home, ".profile")); got != "export X=1\n" {
		t.Errorf(".profile = %q", got)
	}
}

func TestCreateHomeRejectsParentSymlinkOutsideRoot(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "home")); err != nil {
		t.Skip("symlinks are not available")
	}
	plan := &Plan{
		RootDir:    root,
		UserHome:   filepath.Join(root, "home", "alice"),
		HomeMode:   0o755,
		CreateHome: true,
	}
	if err := ensureHome(plan); err == nil || !strings.Contains(err.Error(), "outside the root") {
		t.Fatalf("want an outside-the-root error, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "alice")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("home must not be created outside the root: %v", err)
	}
}

func TestPlanRejectsHomesThatAreNotRealHomes(t *testing.T) {
	stubUser(t, "svc")
	target := plant(t, "home", map[string]string{"x": "1"})
	for _, home := range []string{"/nonexistent", "/", "/dev/null", "relative/home"} {
		_, err := BuildPlan(&layout.Layout{FileTrees: []string{"home"}}, nil,
			sysinfo.Info{User: "svc", Home: home}, target, t.TempDir())
		if err == nil {
			t.Errorf("home %q must be rejected", home)
		}
	}
}

func TestPlanFailsForUnknownUser(t *testing.T) {
	stubUser(t, "alice")
	target := plant(t, "home", map[string]string{"x": "1"})
	_, err := BuildPlan(&layout.Layout{FileTrees: []string{"home"}}, nil,
		sysinfo.Info{User: "ghost", Home: "/home/ghost"}, target, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("want an unknown-user error, got %v", err)
	}
}

// ---- 単体

func TestParseName(t *testing.T) {
	tests := []struct {
		in       string
		wantName string
		wantMode Mode
		wantErr  bool
	}{
		{"a", "a", ModeCopy, false},
		{"a.tmpl", "a", ModeTemplate, false},
		{"a.append", "a", ModeAppend, false},
		{"a.tmpl.append", "a", ModeTemplateAppend, false},
		{"a.append.tmpl", "a", ModeTemplateAppend, false},
		{"dir/x.tar.tmpl", "dir/x.tar", ModeTemplate, false},
		{"a.append.append", "", 0, true},
		{"a.tmpl.tmpl", "", 0, true},
		{".append", "", 0, true},
		{"dir/.tmpl", "", 0, true},
	}
	for _, tt := range tests {
		name, mode, err := parseName(tt.in)
		if (err != nil) != tt.wantErr {
			t.Errorf("parseName(%q) error = %v, wantErr %v", tt.in, err, tt.wantErr)
			continue
		}
		if !tt.wantErr && (name != tt.wantName || mode != tt.wantMode) {
			t.Errorf("parseName(%q) = %q, %v; want %q, %v", tt.in, name, mode, tt.wantName, tt.wantMode)
		}
	}
}

func TestParseHomeMode(t *testing.T) {
	tests := []struct {
		name, in string
		want     os.FileMode
	}{
		{"HOME_MODE wins", "UMASK 077\nHOME_MODE 0700\n", 0o700},
		{"UMASK is used otherwise", "UMASK 027\n", 0o750},
		{"comments are ignored", "# HOME_MODE 0700\nUMASK 022\n", 0o755},
		{"default", "", 0o755},
		{"garbage falls back", "HOME_MODE rwx\n", 0o755},
		{"out of range falls back", "HOME_MODE 4755\n", 0o755},
	}
	for _, tt := range tests {
		if got := parseHomeMode(strings.NewReader(tt.in)); got != tt.want {
			t.Errorf("%s: got %04o, want %04o", tt.name, got, tt.want)
		}
	}
}

func TestContainsLines(t *testing.T) {
	tests := []struct {
		existing, patch string
		want            bool
	}{
		{"a\nb\n", "b\n", true},
		{"xb\n", "b\n", false},
		{"a\nb", "b", true},
		{"", "", true},
		{"a\n", "", true},
		{"ab\ncd\n", "b\nc", false},
		{"a\nb\nc\n", "b\nc\n", true},
		{"a\nb\nc\n", "c\nb\n", false},
	}
	for _, tt := range tests {
		if got := containsLines([]byte(tt.existing), []byte(tt.patch)); got != tt.want {
			t.Errorf("containsLines(%q, %q) = %v, want %v", tt.existing, tt.patch, got, tt.want)
		}
	}
}
