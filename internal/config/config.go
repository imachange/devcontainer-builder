// Package config は、devcontainer.json を探して読み込み、build.args を分類・検証する。
//
// 読み込みは fs.FS を通して行う (本番では os.Root の FS を渡し、コンテキストの外に出られない)。
// parser には、読み込んだバイト列を渡す。
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	parser "github.com/imachange/devcontainer-parser"
)

// Level は、診断メッセージの重要度。
type Level string

const (
	LevelInfo Level = "INFO"
	LevelWarn Level = "WARN"
)

// Diagnostic は、ビルドログに出す診断メッセージ。
type Diagnostic struct {
	Level Level
	Msg   string
}

func (d Diagnostic) String() string { return "[" + string(d.Level) + "] " + d.Msg }

// DefaultScriptTimeout は、BUILD_SCRIPT_TIMEOUT を省略したときの、スクリプト1つあたりの timeout。
const DefaultScriptTimeout = 10 * time.Minute

const maxConfigSize = 1 << 20

// Settings は、devcontainer.json から読み取った、apply の設定。
type Settings struct {
	// Path は、devcontainer.json の、コンテキストからの相対パス (スラッシュ区切り)。
	Path string
	// Dir は、Path のあるディレクトリ。コンテキスト直下のときは "."。
	Dir string

	// Args は、build.args の全体。
	Args map[string]string
	// Vars は、テンプレート変数とスクリプトの環境変数になる build.args (BUILD_ で始まらないもの)。
	Vars map[string]string
	// Packages は、BUILD_PACKAGES* の項目。キーは default / apt / dnf / apk / zypper。
	Packages map[string][]string

	Overwrite     bool          // BUILD_OVERWRITE (既定 true)
	ScriptTimeout time.Duration // BUILD_SCRIPT_TIMEOUT (既定 10m)

	BuildUser     string // BUILD_USER
	RemoteUser    string // remoteUser
	ContainerUser string // containerUser

	Diagnostics []Diagnostic
}

// UserCandidates は、開発ユーザーの候補を優先順で返す。
// 仕様の既定の連鎖 (remoteUser、containerUser、イメージの USER) に合わせている。
func (s *Settings) UserCandidates() []string {
	return []string{s.RemoteUser, s.ContainerUser, s.BuildUser}
}

func (s *Settings) warn(format string, a ...any) {
	s.Diagnostics = append(s.Diagnostics, Diagnostic{LevelWarn, fmt.Sprintf(format, a...)})
}

func (s *Settings) info(format string, a ...any) {
	s.Diagnostics = append(s.Diagnostics, Diagnostic{LevelInfo, fmt.Sprintf(format, a...)})
}

var candidates = []string{
	"devcontainer.json",
	".devcontainer/devcontainer.json",
	".devcontainer.json",
}

// Discover は、コンテキストの中から、devcontainer.json の場所を決める。
// explicit が空でなければ、コンテキストからの相対パスとして、それを使う。
// 複数見つかったときは、曖昧なのでエラーにする。
func Discover(fsys fs.FS, explicit string) (string, error) {
	if explicit != "" {
		p := path.Clean(strings.TrimPrefix(filepath.ToSlash(explicit), "/"))
		if !fs.ValidPath(p) {
			return "", fmt.Errorf("--config %q must stay inside the context", explicit)
		}
		if fi, err := fs.Stat(fsys, p); err != nil {
			return "", fmt.Errorf("--config %s: %w", explicit, err)
		} else if !fi.Mode().IsRegular() {
			return "", fmt.Errorf("--config %s is not a regular file", explicit)
		}
		return p, nil
	}

	var found []string
	for _, c := range candidates {
		if fi, err := fs.Stat(fsys, c); err == nil && fi.Mode().IsRegular() {
			found = append(found, c)
		}
	}
	switch len(found) {
	case 0:
		return "", fmt.Errorf("devcontainer.json not found (searched: %s)", strings.Join(candidates, ", "))
	case 1:
		return found[0], nil
	}
	return "", fmt.Errorf("multiple devcontainer.json found (%s); use --config", strings.Join(found, ", "))
}

var (
	identRe   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	pkgArgRe  = regexp.MustCompile(`^BUILD_PACKAGES(?:_(APT|DNF|APK|ZYPPER))?$`)
	knownKeys = map[string]bool{
		"BUILD_BASE_IMAGE":     true,
		"BUILD_APPLY_IMAGE":    true,
		"BUILD_USER":           true,
		"BUILD_OVERWRITE":      true,
		"BUILD_SCRIPT_TIMEOUT": true,
	}
)

// hasSubstitution は、dev container tools が置換する "${...}" を含むかを返す。
func hasSubstitution(v string) bool { return strings.Contains(v, "${") }

// Load は、devcontainer.json を読み込み、設定を組み立てる。
// 検証に失敗した項目は、まとめてエラーにする。
func Load(fsys fs.FS, p string) (*Settings, error) {
	fi, err := fs.Stat(fsys, p)
	if err != nil {
		return nil, err
	}
	if fi.Size() > maxConfigSize {
		return nil, fmt.Errorf("%s is larger than %d bytes", p, maxConfigSize)
	}
	data, err := fs.ReadFile(fsys, p)
	if err != nil {
		return nil, err
	}

	// parser が INFO にするのは、「使わないキー」。name など、ほぼ全員が持つキーまで INFO になるので、
	// このツールが読むキーを渡して、ノイズを減らす。
	res, err := parser.Parse(data, parser.WithSupportedKeys("build", "remoteUser", "containerUser"))
	if err != nil {
		return nil, err
	}
	cfg := res.Config

	s := &Settings{
		Path:          p,
		Dir:           path.Dir(p),
		Args:          map[string]string{},
		Vars:          map[string]string{},
		Packages:      map[string][]string{},
		Overwrite:     true,
		ScriptTimeout: DefaultScriptTimeout,
		RemoteUser:    cfg.RemoteUser,
		ContainerUser: cfg.ContainerUser,
	}
	if cfg.Build != nil {
		maps.Copy(s.Args, cfg.Build.Args)
	}

	for _, d := range res.Diagnostics {
		lv := LevelInfo
		if d.Level == parser.LevelWarning {
			lv = LevelWarn
		}
		msg := d.Message
		if d.Key != "" {
			msg = d.Key + ": " + msg
		}
		s.Diagnostics = append(s.Diagnostics, Diagnostic{lv, msg})
	}

	var errs []error

	if len(cfg.Features) > 0 {
		s.warn("features (%s) are ignored by devcontainer-builder, but dev container tools install them "+
			"on top of this image, so the resulting images differ. "+
			"Put users and tools in the base image, or create them in pre.d/.",
			strings.Join(slices.Sorted(maps.Keys(cfg.Features)), ", "))
	}
	if len(cfg.OverrideFeatureInstallOrder) > 0 {
		s.warn("overrideFeatureInstallOrder has no effect without features")
	}
	if len(cfg.DockerComposeFile) > 0 {
		s.warn("dockerComposeFile is ignored by devcontainer-builder")
	}

	for _, u := range []struct{ key, val string }{
		{"remoteUser", cfg.RemoteUser},
		{"containerUser", cfg.ContainerUser},
	} {
		if hasSubstitution(u.val) {
			errs = append(errs, fmt.Errorf("%s contains ${...}; use a literal value "+
				"(devcontainer-builder reads the raw file and cannot resolve it)", u.key))
		}
	}

	for _, k := range slices.Sorted(maps.Keys(s.Args)) {
		v := s.Args[k]
		switch {
		case strings.HasPrefix(k, "DCB_"):
			errs = append(errs, fmt.Errorf("build.args.%s: the DCB_ prefix is reserved", k))

		case k == "BUILD_LANG":
			s.warn("build.args.BUILD_LANG is no longer used; the base image's LANG is kept as it is")

		case strings.HasPrefix(k, "BUILD_PACKAGES"):
			fam, err := packageFamily(k)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			if hasSubstitution(v) {
				errs = append(errs, errSubstitution(k))
				continue
			}
			s.Packages[fam] = splitItems(v)

		case strings.HasPrefix(k, "BUILD_"):
			if !knownKeys[k] {
				s.warn("build.args.%s: unknown BUILD_ key (ignored)", k)
				continue
			}
			if err := s.applyControl(k, v); err != nil {
				errs = append(errs, err)
			}

		default:
			if !identRe.MatchString(k) {
				s.warn("build.args.%s: not a valid variable name; not available to templates and scripts", k)
				continue
			}
			if hasSubstitution(v) {
				errs = append(errs, errSubstitution(k))
				continue
			}
			s.Vars[k] = v
		}
	}

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return s, nil
}

func errSubstitution(key string) error {
	return fmt.Errorf("build.args.%s contains ${...}; use a literal value "+
		"(dev container tools substitute it, but devcontainer-builder reads the raw file)", key)
}

// applyControl は、制御用の BUILD_* キーのうち、apply が値を使うものを反映する。
func (s *Settings) applyControl(k, v string) error {
	switch k {
	case "BUILD_USER":
		if hasSubstitution(v) {
			return errSubstitution(k)
		}
		s.BuildUser = v
	case "BUILD_OVERWRITE":
		if hasSubstitution(v) {
			return errSubstitution(k)
		}
		b, err := strconv.ParseBool(strings.TrimSpace(v))
		if err != nil {
			return fmt.Errorf("build.args.%s: %q is not a boolean", k, v)
		}
		s.Overwrite = b
	case "BUILD_SCRIPT_TIMEOUT":
		if hasSubstitution(v) {
			return errSubstitution(k)
		}
		d, err := time.ParseDuration(strings.TrimSpace(v))
		if err != nil || d <= 0 {
			return fmt.Errorf("build.args.%s: %q is not a positive duration (e.g. 10m)", k, v)
		}
		s.ScriptTimeout = d
	default:
		// BUILD_BASE_IMAGE と BUILD_APPLY_IMAGE は Dockerfile が使うので、apply は何もしない。
	}
	return nil
}

// packageFamily は、BUILD_PACKAGES* のキーから、系統 (default / apt / ...) を返す。
func packageFamily(key string) (string, error) {
	m := pkgArgRe.FindStringSubmatch(key)
	if m == nil {
		return "", fmt.Errorf("build.args.%s: unknown key (allowed: BUILD_PACKAGES, "+
			"BUILD_PACKAGES_APT, BUILD_PACKAGES_DNF, BUILD_PACKAGES_APK, BUILD_PACKAGES_ZYPPER)", key)
	}
	if m[1] == "" {
		return "default", nil
	}
	return strings.ToLower(m[1]), nil
}

// splitItems は、空白かカンマで区切られた項目に分ける。
func splitItems(v string) []string {
	return strings.FieldsFunc(v, func(r rune) bool { return r == ',' || unicode.IsSpace(r) })
}
