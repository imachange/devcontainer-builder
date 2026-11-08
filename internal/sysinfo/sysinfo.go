// Package sysinfo は、スクリプトに渡す DCB_* 環境変数の元になる情報を、実行環境から判定する。
//
// 判定は fs.FS を通して行うので、testing/fstest で、各ディストリビューションの構成を再現できる。
// 判定できなかったものは、空文字で返す (エラーにはしない)。
package sysinfo

import (
	"bufio"
	"io"
	"io/fs"
	"os"
	"os/user"
	"path"
	"runtime"
	"strings"
)

// Info は、判定結果。
type Info struct {
	Phase  string // pre / post
	OSID   string // os-release の ID
	Family string // apt / dnf / apk / zypper
	PkgMgr string // apt-get / dnf / microdnf / yum / apk / zypper
	Arch   string // amd64 / arm64 など
	User   string // 開発ユーザー
	Home   string // 開発ユーザーのホーム (ユーザーが未作成なら空)
	Lang   string // 実行時点の LANG (ベースイメージのもの)

	// Found は、見つかったパッケージマネージャのコマンド (診断用)。
	Found []string
}

// Env は、DCB_* の環境変数を "KEY=VALUE" の形式で返す。判定できなかったものは空文字になる。
func (i Info) Env() []string {
	return []string{
		"DCB_PHASE=" + i.Phase,
		"DCB_OS_ID=" + i.OSID,
		"DCB_OS_FAMILY=" + i.Family,
		"DCB_PKGMGR=" + i.PkgMgr,
		"DCB_ARCH=" + i.Arch,
		"DCB_USER=" + i.User,
		"DCB_HOME=" + i.Home,
		"DCB_LANG=" + i.Lang,
	}
}

// Probe は、判定に使う入力。テストでは差し替える。
type Probe struct {
	FS         fs.FS
	PathEnv    string
	Arch       string
	Getenv     func(string) string
	LookupUser func(string) (*user.User, error)
}

// System は、実行中のシステムを対象にした Probe を返す。
func System() Probe {
	return Probe{
		FS:         os.DirFS("/"),
		PathEnv:    os.Getenv("PATH"),
		Arch:       runtime.GOARCH,
		Getenv:     os.Getenv,
		LookupUser: lookupUser,
	}
}

func lookupUser(name string) (*user.User, error) {
	if name != "" && strings.Trim(name, "0123456789") == "" {
		return user.LookupId(name)
	}
	return user.Lookup(name)
}

// Detect は、環境を判定する。userNames は、開発ユーザーの候補 (優先順)。
func (p Probe) Detect(phase string, userNames ...string) Info {
	id, like := p.osRelease()
	cmd, family, found := p.pkgMgr(familyFromOS(id, like))
	name, home := p.devUser(userNames)

	lang := ""
	if p.Getenv != nil {
		lang = p.Getenv("LANG")
	}
	return Info{
		Phase:  phase,
		OSID:   id,
		Family: family,
		PkgMgr: cmd,
		Arch:   p.Arch,
		User:   name,
		Home:   home,
		Lang:   lang,
		Found:  found,
	}
}

// ---- OS

var legacyRelease = []struct{ file, id string }{
	{"etc/alpine-release", "alpine"},
	{"etc/debian_version", "debian"},
	{"etc/SuSE-release", "opensuse"},
	{"etc/redhat-release", "rhel"},
}

var idFamily = map[string]string{
	"debian": "apt", "ubuntu": "apt",
	"alpine": "apk",
	"fedora": "dnf", "rhel": "dnf", "centos": "dnf", "rocky": "dnf",
	"almalinux": "dnf", "amzn": "dnf", "ol": "dnf",
	"opensuse": "zypper", "opensuse-leap": "zypper", "opensuse-tumbleweed": "zypper",
	"suse": "zypper", "sles": "zypper", "sled": "zypper",
}

func (p Probe) osRelease() (id, like string) {
	for _, f := range []string{"etc/os-release", "usr/lib/os-release"} {
		b, err := fs.ReadFile(p.FS, f)
		if err != nil {
			continue
		}
		m := parseOSRelease(strings.NewReader(string(b)))
		id = strings.ToLower(m["ID"])
		if id == "" {
			id = "linux" // 仕様上の既定値
		}
		return id, strings.ToLower(m["ID_LIKE"])
	}
	for _, l := range legacyRelease {
		if _, err := fs.Stat(p.FS, l.file); err == nil {
			return l.id, ""
		}
	}
	return "", ""
}

func parseOSRelease(r io.Reader) map[string]string {
	m := map[string]string{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		m[strings.TrimSpace(k)] = unquote(strings.TrimSpace(v))
	}
	return m
}

var doubleQuoteEscapes = strings.NewReplacer(`\"`, `"`, `\\`, `\`, `\$`, `$`, "\\`", "`")

func unquote(v string) string {
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
		q := v[0]
		v = v[1 : len(v)-1]
		if q == '"' {
			v = doubleQuoteEscapes.Replace(v)
		}
	}
	return v
}

func familyFromOS(id, like string) string {
	for _, t := range append([]string{id}, strings.Fields(like)...) {
		if f, ok := idFamily[t]; ok {
			return f
		}
	}
	return ""
}

// ---- パッケージマネージャ (コマンドの有無で判定する)

var managers = []struct{ Family, Cmd string }{
	{"apt", "apt-get"},
	{"dnf", "dnf"},
	{"dnf", "microdnf"},
	{"dnf", "yum"},
	{"apk", "apk"},
	{"zypper", "zypper"},
}

var stdDirs = []string{
	"/usr/local/sbin", "/usr/local/bin", "/usr/sbin", "/usr/bin", "/sbin", "/bin",
}

// pkgMgr は、PATH と標準のディレクトリから、パッケージマネージャのコマンドを探す。
// 複数見つかったときは、os-release の系統に合うものを選び、無ければ先頭を選ぶ。
// 1つも見つからないときは、cmd を空にして、系統だけ os-release のものを返す。
func (p Probe) pkgMgr(osFamily string) (cmd, family string, found []string) {
	dirs := searchDirs(p.PathEnv)

	type cand struct{ family, cmd string }
	var cands []cand
	for _, m := range managers {
		if p.has(dirs, m.Cmd) {
			cands = append(cands, cand{m.Family, m.Cmd})
			found = append(found, m.Cmd)
		}
	}
	if len(cands) == 0 {
		return "", osFamily, nil
	}
	pick := cands[0]
	for _, c := range cands {
		if c.family == osFamily {
			pick = c
			break
		}
	}
	return pick.cmd, pick.family, found
}

// searchDirs は、PATH の絶対パスの要素と標準のディレクトリを、重複なく返す。
// 空の要素と相対パス (".") は無視する。
func searchDirs(pathEnv string) []string {
	seen := map[string]bool{}
	var out []string
	for _, d := range append(strings.Split(pathEnv, ":"), stdDirs...) {
		if !strings.HasPrefix(d, "/") {
			continue
		}
		d = path.Clean(d)
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	return out
}

// has は、実行可能な通常ファイルがあるかを返す。シンボリックリンクは辿る。
func (p Probe) has(dirs []string, name string) bool {
	for _, d := range dirs {
		fi, err := fs.Stat(p.FS, path.Join(strings.TrimPrefix(d, "/"), name))
		if err == nil && fi.Mode().IsRegular() && fi.Mode().Perm()&0o111 != 0 {
			return true
		}
	}
	return false
}

// ---- ユーザー

// normalizeUser は、"user:group" の形式から、ユーザー部分を取り出す。
func normalizeUser(s string) string {
	u, _, _ := strings.Cut(strings.TrimSpace(s), ":")
	return strings.TrimSpace(u)
}

// devUser は、候補のうち、最初に空でないものを開発ユーザーとし、そのホームを返す。
// 候補が無ければ root。ユーザーがまだ存在しなければ、ホームは空 (root を除く)。
func (p Probe) devUser(names []string) (name, home string) {
	for _, n := range names {
		if n = normalizeUser(n); n != "" {
			name = n
			break
		}
	}
	if name == "" {
		name = "root"
	}
	if p.LookupUser != nil {
		if u, err := p.LookupUser(name); err == nil {
			return u.Username, u.HomeDir
		}
	}
	if name == "root" || name == "0" {
		return "root", "/root"
	}
	return name, ""
}
