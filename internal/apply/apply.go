// Package apply は、apply サブコマンドの流れをまとめる。
//
// 流れ: 設定の探索と読み込み → target の走査と規約の検査 → 環境の判定 → (実行)。
// 書き込みの前に検出できるエラーは、すべて、実行の前に出す。
//
// 実行 (pre → packages → files → post) は、まだ実装していない。
// 何かを実行すべき構成なのに、黙って成功することが無いように、明示的に失敗させる。
package apply

import (
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
}

// Run は、ctxDir (ビルドコンテキスト) を対象に、apply を実行する。ログは w に出す。
func Run(ctxDir string, o Options, w io.Writer) error {
	if !o.Check {
		if runtime.GOOS != "linux" {
			return errors.New("apply runs on Linux only (use --check to validate elsewhere)")
		}
		if os.Geteuid() != 0 {
			return errors.New("apply must run as root (use --check to validate without root)")
		}
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
	return ErrNotImplemented
}

// loadLayout は、target ディレクトリを探して、走査する。
// 既定の場所 (devcontainer.json の隣の target/) が無ければ、空として扱う。
// --target で明示した場所が無ければ、エラーにする。
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
