// Package pkgmgr は、packages/*.txt および build.args からパッケージ指定を抽出し、
// 優先順位に従って結合した上で適切なパッケージマネージャーを用いて導入を行う。
package pkgmgr

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/imachange/devcontainer-builder/internal/config"
	"github.com/imachange/devcontainer-builder/internal/layout"
	"github.com/imachange/devcontainer-builder/internal/sysinfo"
)

// Item は、パッケージ名およびバージョン指定を表す構造体。
type Item struct {
	Name    string
	Version string
}

func (i Item) String() string {
	if i.Version == "" {
		return i.Name
	}
	return i.Name + "@" + i.Version
}

// Plan は、優先順位に従ってマージされた導入対象パッケージの一覧および診断メッセージ。
type Plan struct {
	Family string
	PkgMgr string
	Items  []Item
	Diags  []config.Diagnostic
}

// CommandSpec は、実行するコマンド、引数、および環境変数の仕様を表す構造体。
type CommandSpec struct {
	Cmd  string
	Args []string
	Env  []string
}

// PackageManager は、各パッケージマネージャー固有のコマンド構成ルールを定義するインターフェース。
type PackageManager interface {
	// FormatPackage は、Item をパッケージマネージャー固有の識別形式（name=ver や name-ver）に変換する。
	FormatPackage(item Item) string

	// PreInstallSpec は、インストール前に行う準備コマンド（apt-get update 等）の仕様を返す。不要な場合は nil を返す。
	PreInstallSpec() *CommandSpec

	// InstallSpec は、パッケージ群をインストールするためのコマンド仕様を返す。
	InstallSpec(pkgs []string) CommandSpec

	// PostInstallCleanSpec は、インストール後に行うキャッシュ削除コマンドの仕様を返す。不要な場合は nil を返す。
	PostInstallCleanSpec() *CommandSpec
}

// Runner は、外部コマンド実行を抽象化するインターフェース。
type Runner interface {
	Run(ctx context.Context, cmd string, args []string, env []string) error
}

// DefaultRunner は、os/exec を使用して標準出力を接続しながらコマンドを実行する Runner 実装。
type DefaultRunner struct{}

// Run は、指定されたコマンドを実行する。
func (r DefaultRunner) Run(ctx context.Context, cmd string, args []string, env []string) error {
	c := exec.CommandContext(ctx, cmd, args...)
	if len(env) > 0 {
		c.Env = append(os.Environ(), env...)
	}
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}

// GetManager は、指定されたコマンド名に対応する PackageManager 実装を返す。
func GetManager(cmd string) (PackageManager, error) {
	switch cmd {
	case "apt-get":
		return &AptManager{}, nil
	case "dnf", "microdnf", "yum":
		return &DnfManager{Cmd: cmd}, nil
	case "apk":
		return &ApkManager{}, nil
	case "zypper":
		return &ZypperManager{}, nil
	default:
		return nil, fmt.Errorf("unsupported package manager: %q", cmd)
	}
}

// Executor は、PackageManager の仕様に基づき、リトライやログ出力を制御しながら順次コマンドを実行するエンジン。
type Executor struct {
	Runner      Runner
	MaxAttempts int
}

// Execute は、事前処理、パッケージ導入、事後クリーンアップの順で実行する。
func (e *Executor) Execute(ctx context.Context, mgr PackageManager, items []Item) error {
	formattedPkgs := make([]string, len(items))
	for i, item := range items {
		formattedPkgs[i] = mgr.FormatPackage(item)
	}

	if pre := mgr.PreInstallSpec(); pre != nil {
		if err := e.runWithRetry(ctx, *pre); err != nil {
			return fmt.Errorf("pre-install command failed (%s): %w", pre.Cmd, err)
		}
	}

	installSpec := mgr.InstallSpec(formattedPkgs)
	if err := e.runWithRetry(ctx, installSpec); err != nil {
		return fmt.Errorf("package installation failed (%s): %w", installSpec.Cmd, err)
	}

	if clean := mgr.PostInstallCleanSpec(); clean != nil {
		_ = e.Runner.Run(ctx, clean.Cmd, clean.Args, clean.Env)
	}

	return nil
}

func (e *Executor) runWithRetry(ctx context.Context, spec CommandSpec) error {
	var err error
	attempts := e.MaxAttempts
	if attempts <= 0 {
		attempts = 1
	}

	for attempt := 1; attempt <= attempts; attempt++ {
		err = e.Runner.Run(ctx, spec.Cmd, spec.Args, spec.Env)
		if err == nil {
			return nil
		}
		if attempt < attempts {
			time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
		}
	}
	return err
}

// Install は、Plan に基づいて適切な PackageManager を取得し、共通の実行エンジンを通してパッケージ導入を行う。
func Install(ctx context.Context, plan *Plan, runner Runner) error {
	if plan == nil || len(plan.Items) == 0 {
		return nil
	}
	if runner == nil {
		runner = DefaultRunner{}
	}

	mgr, err := GetManager(plan.PkgMgr)
	if err != nil {
		return err
	}

	exec := &Executor{Runner: runner, MaxAttempts: 3}
	return exec.Execute(ctx, mgr, plan.Items)
}

var varTmplRe = regexp.MustCompile(`\{\{\s*([A-Za-z_][A-Za-z0-9_]*)\s*\}\}`)
var packagePartRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9+_.:-]*$`)

// ExpandVars は、文字列内の {{ VAR }} を vars の値で展開する。
func ExpandVars(s string, vars map[string]string) (string, error) {
	var errs []error
	res := varTmplRe.ReplaceAllStringFunc(s, func(match string) string {
		sub := varTmplRe.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		varName := sub[1]
		val, ok := vars[varName]
		if !ok {
			errs = append(errs, fmt.Errorf("undefined template variable {{ %s }}", varName))
			return match
		}
		return val
	})
	if len(errs) > 0 {
		return "", errors.Join(errs...)
	}
	return res, nil
}

// ParseLine は、1行のテキストから "name" または "name@version" を抽出する。コメント（# 以降）は無視する。
func ParseLine(raw string) (Item, error) {
	raw = strings.TrimSpace(raw)
	if idx := strings.IndexByte(raw, '#'); idx != -1 {
		raw = strings.TrimSpace(raw[:idx])
	}
	if raw == "" {
		return Item{}, nil
	}
	name, ver, hasVer := strings.Cut(raw, "@")
	name = strings.TrimSpace(name)
	if !packagePartRe.MatchString(name) {
		return Item{}, fmt.Errorf("invalid package name: %q", name)
	}
	if hasVer {
		ver = strings.TrimSpace(ver)
		if !packagePartRe.MatchString(ver) {
			return Item{}, fmt.Errorf("invalid package version for %s: %q", name, ver)
		}
	}
	return Item{Name: name, Version: ver}, nil
}

// Validate は、パッケージマネージャーの実行前に、全系統の指定とテンプレート変数を検証する。
func Validate(lay *layout.Layout, cfg *config.Settings) error {
	vars := map[string]string{}
	if cfg != nil {
		vars = cfg.Vars
	}
	var errs []error
	validate := func(source string, lines []string) {
		for i, raw := range lines {
			expanded, err := ExpandVars(raw, vars)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s:%d: %w", source, i+1, err))
				continue
			}
			if _, err := ParseLine(expanded); err != nil {
				errs = append(errs, fmt.Errorf("%s:%d: %w", source, i+1, err))
			}
		}
	}
	if lay != nil {
		families := make([]string, 0, len(lay.Packages))
		for family := range lay.Packages {
			families = append(families, family)
		}
		sort.Strings(families)
		for _, family := range families {
			validate("packages/"+family+".txt", lay.Packages[family])
		}
	}
	if cfg != nil {
		families := make([]string, 0, len(cfg.Packages))
		for family := range cfg.Packages {
			families = append(families, family)
		}
		sort.Strings(families)
		for _, family := range families {
			lines := cfg.Packages[family]
			name := "BUILD_PACKAGES"
			if family != "default" {
				name += "_" + strings.ToUpper(family)
			}
			validate(name, lines)
		}
	}
	return errors.Join(errs...)
}

// BuildPlan は、レイアウト・設定・システム情報から優先順位に従って Plan を構築する。
func BuildPlan(lay *layout.Layout, cfg *config.Settings, info sysinfo.Info) (*Plan, error) {
	plan := &Plan{
		Family: info.Family,
		PkgMgr: info.PkgMgr,
	}

	if info.PkgMgr == "" {
		hasAnyPkg := false
		if lay != nil && len(lay.Packages) > 0 {
			hasAnyPkg = true
		}
		if cfg != nil && len(cfg.Packages) > 0 {
			hasAnyPkg = true
		}
		if hasAnyPkg {
			return nil, fmt.Errorf("package manager is not found on this system, but packages are specified")
		}
		return plan, nil
	}

	checkUnusedFamily := func(fam string, source string) {
		if fam != "default" && fam != info.Family {
			plan.Diags = append(plan.Diags, config.Diagnostic{
				Level: config.LevelInfo,
				Msg:   fmt.Sprintf("ignoring %s package specifications for %s (system is %s)", source, fam, info.Family),
			})
		}
	}

	if lay != nil {
		for f := range lay.Packages {
			checkUnusedFamily(f, "packages/*.txt")
		}
	}
	if cfg != nil {
		for f := range cfg.Packages {
			checkUnusedFamily(f, "BUILD_PACKAGES*")
		}
	}

	type pkgSource struct {
		name  string
		lines []string
	}
	var sources []pkgSource

	if lay != nil {
		if lines, ok := lay.Packages["default"]; ok {
			sources = append(sources, pkgSource{"packages/default.txt", lines})
		}
		if info.Family != "" {
			if lines, ok := lay.Packages[info.Family]; ok {
				sources = append(sources, pkgSource{"packages/" + info.Family + ".txt", lines})
			}
		}
	}

	if cfg != nil {
		if lines, ok := cfg.Packages["default"]; ok {
			sources = append(sources, pkgSource{"BUILD_PACKAGES", lines})
		}
		if info.Family != "" {
			if lines, ok := cfg.Packages[info.Family]; ok {
				sources = append(sources, pkgSource{"BUILD_PACKAGES_" + strings.ToUpper(info.Family), lines})
			}
		}
	}

	itemsMap := map[string]int{}
	var errs []error
	vars := map[string]string{}
	if cfg != nil {
		vars = cfg.Vars
	}

	for _, src := range sources {
		for _, rawLine := range src.lines {
			expanded, err := ExpandVars(rawLine, vars)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", src.name, err))
				continue
			}

			item, err := ParseLine(expanded)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", src.name, err))
				continue
			}
			if item.Name == "" {
				continue
			}

			idx, exists := itemsMap[item.Name]
			if !exists {
				itemsMap[item.Name] = len(plan.Items)
				plan.Items = append(plan.Items, item)
			} else {
				oldItem := plan.Items[idx]
				if item.Version != "" {
					if oldItem.Version != "" && isDowngrade(oldItem.Version, item.Version) {
						errs = append(errs, fmt.Errorf("package %s: version downgrade from %s to %s is rejected (%s)",
							item.Name, oldItem.Version, item.Version, src.name))
						continue
					}
					if oldItem.Version != item.Version {
						plan.Diags = append(plan.Diags, config.Diagnostic{
							Level: config.LevelInfo,
							Msg: fmt.Sprintf("package %s version updated from %s to %s (%s)",
								item.Name, oldItem.Version, item.Version, src.name),
						})
						plan.Items[idx].Version = item.Version
					}
				}
			}
		}
	}

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return plan, nil
}

func isDowngrade(oldVer, newVer string) bool {
	return compareVersions(newVer, oldVer) < 0
}

func compareVersions(v1, v2 string) int {
	if v1 == v2 {
		return 0
	}
	c1 := splitVersion(v1)
	c2 := splitVersion(v2)
	n := len(c1)
	if len(c2) < n {
		n = len(c2)
	}
	for i := 0; i < n; i++ {
		if c1[i].isNum && c2[i].isNum {
			if c1[i].num != c2[i].num {
				if c1[i].num < c2[i].num {
					return -1
				}
				return 1
			}
		} else {
			if c1[i].str != c2[i].str {
				if c1[i].str < c2[i].str {
					return -1
				}
				return 1
			}
		}
	}
	if len(c1) < len(c2) {
		return -1
	}
	if len(c1) > len(c2) {
		return 1
	}
	return 0
}

type vChunk struct {
	isNum bool
	num   uint64
	str   string
}

func splitVersion(v string) []vChunk {
	var chunks []vChunk
	var buf strings.Builder
	var isNum bool

	flush := func() {
		if buf.Len() == 0 {
			return
		}
		s := buf.String()
		buf.Reset()
		if isNum {
			var n uint64
			for _, r := range s {
				n = n*10 + uint64(r-'0')
			}
			chunks = append(chunks, vChunk{isNum: true, num: n})
		} else {
			chunks = append(chunks, vChunk{isNum: false, str: s})
		}
	}

	for i, r := range v {
		currIsNum := r >= '0' && r <= '9'
		if i == 0 {
			isNum = currIsNum
		} else if currIsNum != isNum {
			flush()
			isNum = currIsNum
		}
		buf.WriteRune(r)
	}
	flush()
	return chunks
}
