// Package apply は、apply サブコマンドの流れをまとめる。
//
// 流れは、次のとおり。
//
//  1. 設定の探索と読み込み、target の走査と規約の検査 (書き込みなし)
//  2. 事前検証: パッケージの書式、ファイル配置の計画 (書き込みなし)
//  3. pre.d のスクリプト
//  4. パッケージ導入
//  5. ファイル配置
//  6. post.d のスクリプト
//
// ユーザーやパッケージマネージャは、pre.d やパッケージ導入で作られることがある。
// そのため、ファイル配置の計画は、事前検証と、配置の直前の 2 回作る。
// 事前検証の時点で、files/home の配置先が決まらなくても、エラーにはしない。
// DCB_* 環境変数は、スクリプトを実行する直前ごとに、判定し直す。
package apply

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/imachange/devcontainer-builder/internal/config"
	"github.com/imachange/devcontainer-builder/internal/layout"
	"github.com/imachange/devcontainer-builder/internal/pkgmgr"
	"github.com/imachange/devcontainer-builder/internal/place"
	"github.com/imachange/devcontainer-builder/internal/script"
	"github.com/imachange/devcontainer-builder/internal/sysinfo"
)

// Options は、apply の設定。
type Options struct {
	Config  string // --config (コンテキストからの相対パス)
	Target  string // --target (コンテキストからの相対パス)
	Check   bool   // --check (検証だけを行う。root も Linux も要らない)
	Verbose bool   // -v (INFO の診断も表示する)

	// テスト用のフィールド。省略時は、ルートディレクトリ "/" と、本物のパッケージマネージャを使う。
	// RootDir を指定したときは、root 権限と Linux の確認を行わない。
	RootDir string
	Runner  pkgmgr.Runner
}

// Run は、ctxDir (ビルドコンテキスト) を対象に、apply を実行する。ログは w に出す。
// RunContext に、キャンセルされないコンテキストを渡すのと同じ。
func Run(ctxDir string, o Options, w io.Writer) error {
	return RunContext(context.Background(), ctxDir, o, w)
}

// RunContext は、Run と同じだが、ctx がキャンセルされると、実行中のスクリプトとコマンドを止めて、戻る。
func RunContext(ctx context.Context, ctxDir string, o Options, w io.Writer) error {
	if !o.Check && o.RootDir == "" {
		if runtime.GOOS != "linux" {
			return errors.New("apply runs on Linux only (use --check to validate elsewhere)")
		}
		if os.Geteuid() != 0 {
			return errors.New("apply must run as root (use --check to validate without root)")
		}
	}
	if !o.Check {
		setUmask() // 出力の権限を、実行環境の umask に左右されないようにする
	}

	// 入力はすべて、コンテキストの中に閉じ込める (.. や symlink で外に出られない)。
	root, err := os.OpenRoot(ctxDir)
	if err != nil {
		return err
	}
	defer root.Close()
	fsys := root.FS()

	cfgPath, err := config.Discover(fsys, o.Config)
	if err != nil {
		return err
	}
	s, err := config.Load(fsys, cfgPath)
	if err != nil {
		return fmt.Errorf("%s: %w", cfgPath, err)
	}
	printDiagnostics(w, s.Diagnostics, o.Verbose)

	lay, targetRel, err := loadLayout(fsys, s, o, w)
	if err != nil {
		return err
	}
	targetDir := filepath.Join(ctxDir, filepath.FromSlash(targetRel))

	detect := func(phase string) sysinfo.Info {
		return sysinfo.System().Detect(phase, s.UserCandidates()...)
	}
	info := detect("pre")
	fmt.Fprintf(w, "==> detected os=%s family=%s pkgmgr=%s arch=%s user=%s home=%s\n",
		orDash(info.OSID), orDash(info.Family), orDash(info.PkgMgr), orDash(info.Arch),
		orDash(info.User), orDash(info.Home))
	if len(info.Found) > 1 {
		fmt.Fprintf(w, "[WARN] multiple package managers found (%s); using %s\n",
			strings.Join(info.Found, ", "), info.PkgMgr)
	}

	hasPackages := len(lay.Packages) > 0 || len(s.Packages) > 0
	if lay.Empty() && !hasPackages {
		fmt.Fprintln(w, "==> nothing to do")
		return nil
	}

	// ---- 事前検証 (書き込みなし)
	if err := pkgmgr.Validate(lay, s); err != nil {
		return fmt.Errorf("packages: %w", err)
	}
	prePlan, err := place.BuildPlan(lay, s, info, targetDir, o.RootDir)
	if err != nil {
		return fmt.Errorf("files: %w", err)
	}
	if prePlan.HomePending {
		fmt.Fprintln(w, "[INFO] files/home: the development user does not exist yet; "+
			"it will be resolved after pre.d and packages")
	}
	if !s.Overwrite {
		// 早く失敗するための確認。配置の直前にも、あらためて確認する。
		for _, a := range prePlan.Actions {
			if a.IsDir || a.Mode.IsAppend() {
				continue
			}
			if _, err := os.Lstat(a.DestPath); err == nil {
				return fmt.Errorf("overwriting existing file %s is forbidden (BUILD_OVERWRITE=false)", a.DestPath)
			}
		}
	}

	fmt.Fprintf(w, "==> plan: pre=%d packages=%t files=%s post=%d\n",
		len(lay.Pre), hasPackages, treeList(lay.FileTrees), len(lay.Post))
	if o.Check {
		return nil
	}

	scriptCfg := func(phase string) script.Config {
		return script.Config{
			Timeout:   s.ScriptTimeout,
			BuildArgs: s.Vars,
			BaseEnv:   os.Environ(),
			Stdout:    w,
			Stderr:    w,
			DCBEnvFunc: func() map[string]string {
				return envMap(detect(phase).Env())
			},
		}
	}

	// ---- 1. pre
	if len(lay.Pre) > 0 {
		fmt.Fprintln(w, "==> executing pre scripts...")
		if err := script.RunDir(ctx, filepath.Join(targetDir, "pre.d"), scriptCfg("pre")); err != nil {
			return fmt.Errorf("pre scripts failed: %w", err)
		}
	}

	// ---- 2. packages (pre.d が、パッケージマネージャを用意したかもしれないので、判定し直す)
	if hasPackages {
		fmt.Fprintln(w, "==> installing packages...")
		pkgPlan, err := pkgmgr.BuildPlan(lay, s, detect("pre"))
		if err != nil {
			return fmt.Errorf("packages: %w", err)
		}
		printDiagnostics(w, pkgPlan.Diags, o.Verbose)
		if err := pkgmgr.Install(ctx, pkgPlan, o.Runner); err != nil {
			return fmt.Errorf("package installation failed: %w", err)
		}
	}

	// ---- 3. files (pre.d やパッケージが、ユーザーを作ったかもしれないので、判定し直す)
	if len(lay.FileTrees) > 0 {
		fmt.Fprintln(w, "==> placing files...")
		plan, err := place.BuildPlan(lay, s, detect("post"), targetDir, o.RootDir)
		if err != nil {
			return fmt.Errorf("file placement planning failed: %w", err)
		}
		plan.Log = w
		if err := place.Execute(ctx, plan, s.Vars); err != nil {
			return fmt.Errorf("file placement failed: %w", err)
		}
	}

	// ---- 4. post
	if len(lay.Post) > 0 {
		fmt.Fprintln(w, "==> executing post scripts...")
		if err := script.RunDir(ctx, filepath.Join(targetDir, "post.d"), scriptCfg("post")); err != nil {
			return fmt.Errorf("post scripts failed: %w", err)
		}
	}

	fmt.Fprintln(w, "==> apply finished successfully")
	return nil
}

// loadLayout は、target ディレクトリを探して、走査する。target の、コンテキストからの相対パスも返す。
// 既定の場所 (devcontainer.json の隣の target/) が無ければ、空として扱う。
// --target で明示した場所が無ければ、エラーにする。
func loadLayout(fsys fs.FS, s *config.Settings, o Options, w io.Writer) (*layout.Layout, string, error) {
	explicit := o.Target != ""
	target := path.Join(s.Dir, "target")
	if explicit {
		target = path.Clean(strings.TrimPrefix(filepath.ToSlash(o.Target), "/"))
		if !fs.ValidPath(target) {
			return nil, "", fmt.Errorf("--target %q must stay inside the context", o.Target)
		}
	}

	fi, err := fs.Stat(fsys, target)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if explicit {
			return nil, "", fmt.Errorf("--target %s: %w", o.Target, err)
		}
		fmt.Fprintf(w, "[INFO] no %s directory; only build.args are used\n", target)
		return &layout.Layout{}, target, nil
	case err != nil:
		return nil, "", err
	case !fi.IsDir():
		return nil, "", fmt.Errorf("%s is not a directory", target)
	}

	sub, err := fs.Sub(fsys, target)
	if err != nil {
		return nil, "", err
	}
	lay, err := layout.Scan(sub)
	if err != nil {
		return nil, "", fmt.Errorf("%s: %w", target, err)
	}
	return lay, target, nil
}

func printDiagnostics(w io.Writer, diags []config.Diagnostic, verbose bool) {
	for _, d := range diags {
		if d.Level == config.LevelInfo && !verbose {
			continue
		}
		fmt.Fprintln(w, d)
	}
}

// envMap は、"KEY=VALUE" の一覧を、map にする。
func envMap(env []string) map[string]string {
	m := make(map[string]string, len(env))
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok {
			m[k] = v
		}
	}
	return m
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func treeList(trees []string) string {
	if len(trees) == 0 {
		return "-"
	}
	return strings.Join(trees, "+")
}
