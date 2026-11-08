// Package apply は、apply サブコマンドの流れをまとめる。
//
// 流れ: 設定の探索と読み込み → target の走査と規約の検査 → 環境の判定 →
// 安全策検証（上書きチェック） → pre.d 実行 → パッケージ導入 → ファイル配置 → post.d 実行。
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

// ErrNotImplemented は、実行フェーズが未実装であることを表す。
var ErrNotImplemented = errors.New("not implemented yet: executing pre / packages / files / post " +
	"(this build only validates; use --check to validate without root)")

// Options は、apply の設定。
type Options struct {
	Config  string // --config (コンテキストからの相対パス)
	Target  string // --target (コンテキストからの相対パス)
	Check   bool   // --check (検証だけを行う。root も Linux も要らない)
	Verbose bool   // -v (INFO の診断も表示する)

	// テスト用フィールド (省略時はルートディレクトリルート "/" およびデフォルト Runner)
	RootDir string
	Runner  pkgmgr.Runner
}

// Run は、ctxDir (ビルドコンテキスト) を対象に、apply を実行する。ログは w に出す。
func Run(ctxDir string, o Options, w io.Writer) error {
	return RunContext(context.Background(), ctxDir, o, w)
}

// RunContext は、コンテキスト付きで apply 処理を実行する。
func RunContext(ctx context.Context, ctxDir string, o Options, w io.Writer) error {
	if !o.Check && o.RootDir == "" {
		if runtime.GOOS != "linux" {
			return errors.New("apply runs on Linux only (use --check to validate elsewhere)")
		}
		if os.Geteuid() != 0 {
			return errors.New("apply must run as root (use --check to validate without root)")
		}
	}

	rootDir := o.RootDir
	if rootDir == "" {
		rootDir = "/"
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

	lay, err := loadLayout(fsys, s, o, w)
	if err != nil {
		return err
	}

	info := sysinfo.System().Detect("pre", s.UserCandidates()...)
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

	fmt.Fprintf(w, "==> plan: pre=%d packages=%t files=%s post=%d\n",
		len(lay.Pre), hasPackages, treeList(lay.FileTrees), len(lay.Post))

	if o.Check {
		return nil
	}

	// ----------------------------------------------------
	// 実行計画の構築とファイル上書き制御 (安全策チェック)
	// ----------------------------------------------------
	targetDir := filepath.Join(ctxDir, s.Dir, "target")
	if o.Target != "" {
		targetDir = filepath.Join(ctxDir, o.Target)
	}

	placePlan, err := place.BuildPlan(lay, s, info, targetDir, rootDir)
	if err != nil {
		return fmt.Errorf("failed to build file placement plan: %w", err)
	}

	// 上書き制御のチェック
	for _, action := range placePlan.Actions {
		if action.IsDir {
			continue
		}
		fi, err := os.Stat(action.DestPath)
		if err == nil && fi.Mode().IsRegular() {
			if !s.Overwrite && (action.Mode == place.ModeCopy || action.Mode == place.ModeTemplate) {
				return fmt.Errorf("overwriting existing file %s is forbidden (BUILD_OVERWRITE=false)", action.DestPath)
			}
			if o.Verbose || s.Overwrite {
				fmt.Fprintf(w, "[INFO] overwriting existing file: %s\n", action.DestPath)
			}
		}
	}

	// ----------------------------------------------------
	// Phase 1: pre.d スクリプト実行
	// ----------------------------------------------------
	if len(lay.Pre) > 0 {
		fmt.Fprintln(w, "==> executing pre scripts...")
		scriptCfg := script.Config{
			Timeout:   s.ScriptTimeout,
			BuildArgs: s.Vars,
			DCBEnv:    infoToMap(info),
			BaseEnv:   os.Environ(),
			Stdout:    w,
			Stderr:    w,
		}
		if err := script.RunDir(ctx, filepath.Join(targetDir, "pre.d"), scriptCfg); err != nil {
			return fmt.Errorf("pre script execution failed: %w", err)
		}
	}

	// ----------------------------------------------------
	// Phase 2: パッケージ導入 (pkgmgr)
	// ----------------------------------------------------
	if hasPackages {
		fmt.Fprintln(w, "==> installing packages...")
		pkgPlan, err := pkgmgr.BuildPlan(lay, s, info)
		if err != nil {
			return fmt.Errorf("failed to build package plan: %w", err)
		}
		printDiagnostics(w, pkgPlan.Diags, o.Verbose)
		if err := pkgmgr.Install(ctx, pkgPlan, o.Runner); err != nil {
			return fmt.Errorf("package installation failed: %w", err)
		}
	}

	// ----------------------------------------------------
	// Phase 3: ファイル配置 (place)
	// ----------------------------------------------------
	if len(lay.FileTrees) > 0 || placePlan.CreateHome {
		fmt.Fprintln(w, "==> placing files...")
		if err := place.Execute(ctx, placePlan, s.Vars); err != nil {
			return fmt.Errorf("file placement failed: %w", err)
		}
	}

	// ----------------------------------------------------
	// Phase 4: post.d スクリプト実行
	// ----------------------------------------------------
	if len(lay.Post) > 0 {
		fmt.Fprintln(w, "==> executing post scripts...")
		// スクリプト直前にシステム情報を再判定
		infoPost := sysinfo.System().Detect("post", s.UserCandidates()...)
		scriptCfgPost := script.Config{
			Timeout:   s.ScriptTimeout,
			BuildArgs: s.Vars,
			DCBEnv:    infoToMap(infoPost),
			BaseEnv:   os.Environ(),
			Stdout:    w,
			Stderr:    w,
		}
		if err := script.RunDir(ctx, filepath.Join(targetDir, "post.d"), scriptCfgPost); err != nil {
			return fmt.Errorf("post script execution failed: %w", err)
		}
	}

	fmt.Fprintln(w, "==> apply finished successfully")
	return nil
}

func loadLayout(fsys fs.FS, s *config.Settings, o Options, w io.Writer) (*layout.Layout, error) {
	explicit := o.Target != ""
	target := path.Join(s.Dir, "target")
	if explicit {
		target = path.Clean(strings.TrimPrefix(filepath.ToSlash(o.Target), "/"))
		if !fs.ValidPath(target) {
			return nil, fmt.Errorf("--target %q must stay inside the context", o.Target)
		}
	}

	fi, err := fs.Stat(fsys, target)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if explicit {
			return nil, fmt.Errorf("--target %s: %w", o.Target, err)
		}
		fmt.Fprintf(w, "[INFO] no %s directory; only build.args are used\n", target)
		return &layout.Layout{}, nil
	case err != nil:
		return nil, err
	case !fi.IsDir():
		return nil, fmt.Errorf("%s is not a directory", target)
	}

	sub, err := fs.Sub(fsys, target)
	if err != nil {
		return nil, err
	}
	lay, err := layout.Scan(sub)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", target, err)
	}
	return lay, nil
}

func printDiagnostics(w io.Writer, diags []config.Diagnostic, verbose bool) {
	for _, d := range diags {
		if d.Level == config.LevelInfo && !verbose {
			continue
		}
		fmt.Fprintln(w, d)
	}
}

func infoToMap(info sysinfo.Info) map[string]string {
	m := make(map[string]string)
	for _, kv := range info.Env() {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) == 2 {
			m[parts[0]] = parts[1]
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
