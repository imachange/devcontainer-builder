package place

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/imachange/devcontainer-builder/internal/merge"
	"github.com/imachange/devcontainer-builder/internal/tmpl"
)

// readSource は、配置元を読み、テンプレートのモードなら、変数を展開して返す。
func readSource(a Action, vars map[string]string) ([]byte, error) {
	data, err := os.ReadFile(a.SourcePath)
	if err != nil {
		return nil, err
	}
	if a.Mode == ModeTemplate || a.Mode == ModeTemplateAppend {
		out, err := tmpl.ExpandBytes(data, vars)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", a.SourcePath, err)
		}
		return out, nil
	}
	return data, nil
}

// chownIfNeeded は、所有者が違うときだけ、path の所有者を変える (シンボリックリンクは辿らない)。
// root 以外で実行しているとき (テスト) は、所有者を変えられないので、失敗を無視する。
// root で変えられなければ、ファイルが、意図しない所有者のまま残るので、エラーにする。
func chownIfNeeded(path string, uid, gid int) error {
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && int(st.Uid) == uid && int(st.Gid) == gid {
		return nil
	}
	if err := os.Lchown(path, uid, gid); err != nil {
		if os.Geteuid() != 0 {
			return nil
		}
		return fmt.Errorf("chown %s to %d:%d: %w", path, uid, gid, err)
	}
	return nil
}

// resolveExisting は、path の、実在する最も深い祖先の symlink を解決し、残りの要素を付け足して返す。
func resolveExisting(path string) (string, error) {
	var rest []string
	cur := filepath.Clean(path)
	for {
		if _, err := os.Lstat(cur); err == nil {
			real, err := filepath.EvalSymlinks(cur)
			if err != nil {
				return "", err // 壊れた symlink など
			}
			for i := len(rest) - 1; i >= 0; i-- {
				real = filepath.Join(real, rest[i])
			}
			return real, nil
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", fmt.Errorf("no existing ancestor of %s", path)
		}
		rest = append(rest, filepath.Base(cur))
		cur = parent
	}
}

// guardDest は、書き込みの直前に、配置先の安全を確認する。
//
//   - symlink を解決した実体が、RootDir の中にあること (ルートの外へ出る symlink を拒否する)。
//   - 実体が、拒否リストに当たらないこと (symlink 経由の、すり抜けを防ぐ)。
//   - ファイルの最終要素が symlink なら、その先へは書き込まない (isDir が true のときは、許可する)。
//
// merged-usr の /bin -> usr/bin のような、symlink のディレクトリは、実体が安全なら許可する。
func guardDest(rootDir, dest string, isDir bool) error {
	realRoot, err := filepath.EvalSymlinks(rootDir)
	if err != nil {
		return err
	}
	real, err := resolveExisting(dest)
	if err != nil {
		return fmt.Errorf("refusing %s: %w", dest, err)
	}
	rel, err := filepath.Rel(realRoot, real)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("refusing %s: it resolves outside the root (%s)", dest, real)
	}
	if isDeniedPath("/" + filepath.ToSlash(rel)) {
		return fmt.Errorf("access to denied path rejected: %s resolves to /%s", dest, filepath.ToSlash(rel))
	}
	if !isDir {
		if fi, err := os.Lstat(dest); err == nil && fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to write through symlink %s", dest)
		}
	}
	return nil
}

// makeDir は、ディレクトリを新規作成する。既にあれば何もしない (権限も所有者も変えない)。
func makeDir(a Action) error {
	if fi, err := os.Stat(a.DestPath); err == nil {
		if !fi.IsDir() {
			return fmt.Errorf("%s exists and is not a directory", a.DestPath)
		}
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(a.DestPath), 0o755); err != nil {
		return err
	}
	if err := os.Mkdir(a.DestPath, a.Perm); err != nil {
		return err
	}
	if err := os.Chmod(a.DestPath, a.Perm); err != nil { // umask に左右されないように
		return err
	}
	return chownIfNeeded(a.DestPath, a.UID, a.GID)
}

// writeAtomic は、一時ファイルに書いてから rename する。権限と所有者は、新しいファイルのものになる。
func writeAtomic(dest string, data []byte, perm os.FileMode, uid, gid int) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".dcb-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name) // rename に成功したあとは、何もしない

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := chownIfNeeded(name, uid, gid); err != nil {
		return err
	}
	return os.Rename(name, dest)
}

// containsLines は、patch の内容が、既存のファイルの中に、行の境界に揃って、連続して含まれているかを返す。
// 行の途中の一致 ("xfoo" の中の "foo") は、含まれていないものとして扱う。
func containsLines(existing, patch []byte) bool {
	p := bytes.TrimRight(patch, "\r\n")
	if len(p) == 0 {
		return true
	}
	hay := append(append([]byte{'\n'}, existing...), '\n')
	needle := append(append([]byte{'\n'}, p...), '\n')
	return bytes.Contains(hay, needle)
}

// appendTo は、追記またはマージを行う。
//
// 配置先が無ければ新規作成する (権限と所有者は、アクションのもの)。
// 既存のファイルは、権限と所有者を変えずに、その場で書き換える。
// 構造化ファイル (.json / .toml / .yaml / .yml) は、キー単位でマージする。
// テキストは、同じ内容が既にあれば、何もしない (冪等)。
func appendTo(plan *Plan, a Action, content []byte) error {
	format, formatErr := merge.DetectFormat(a.DestPath)
	structured := formatErr == nil

	fi, err := os.Lstat(a.DestPath)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if structured { // 新規作成でも、構文は先に検証する
			if _, err := merge.Merge(format, nil, content); err != nil {
				return fmt.Errorf("invalid content for %s: %w", a.DestPath, err)
			}
		}
		return writeAtomic(a.DestPath, content, a.Perm, a.UID, a.GID)
	case err != nil:
		return err
	case !fi.Mode().IsRegular():
		return fmt.Errorf("%s is not a regular file", a.DestPath)
	}

	existing, err := os.ReadFile(a.DestPath)
	if err != nil {
		return err
	}

	if structured {
		if len(bytes.TrimSpace(existing)) == 0 {
			if _, err := merge.Merge(format, nil, content); err != nil {
				return fmt.Errorf("invalid content for %s: %w", a.DestPath, err)
			}
			return rewriteInPlace(a.DestPath, content)
		}
		merged, err := merge.Merge(format, existing, content)
		if err != nil {
			return fmt.Errorf("failed to merge into %s: %w", a.DestPath, err)
		}
		if bytes.Equal(merged, existing) {
			plan.logf("[INFO] already up to date, skipped: %s\n", a.DestPath)
			return nil
		}
		return rewriteInPlace(a.DestPath, merged)
	}

	if containsLines(existing, content) {
		plan.logf("[INFO] content already present, skipped: %s\n", a.DestPath)
		return nil
	}
	var addition bytes.Buffer
	if len(existing) > 0 && existing[len(existing)-1] != '\n' {
		addition.WriteByte('\n') // 行が連結されないように
	}
	addition.Write(content)
	if !bytes.HasSuffix(content, []byte("\n")) {
		addition.WriteByte('\n')
	}
	return appendInPlace(a.DestPath, addition.Bytes())
}

// appendInPlace は、既存のファイルの末尾に書き足す (権限、所有者、inode を変えない)。
func appendInPlace(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// rewriteInPlace は、既存のファイルの中身を置き換える (権限、所有者、inode を変えない)。
func rewriteInPlace(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// homeModeFromFile は、login.defs から、ホームの権限を決める。読めなければ 0755。
func homeModeFromFile(path string) os.FileMode {
	f, err := os.Open(path)
	if err != nil {
		return 0o755
	}
	defer f.Close()
	return parseHomeMode(f)
}

// parseHomeMode は、HOME_MODE を使い、無ければ UMASK から (0777 &^ UMASK) 求め、それも無ければ 0755 を返す。
// useradd -m と同じ結果にするための規則。
func parseHomeMode(r io.Reader) os.FileMode {
	var homeMode, umask string
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 || strings.HasPrefix(fields[0], "#") {
			continue
		}
		switch fields[0] {
		case "HOME_MODE":
			homeMode = fields[1]
		case "UMASK":
			umask = fields[1]
		}
	}
	if v, err := strconv.ParseUint(homeMode, 8, 32); err == nil && v <= 0o777 {
		return os.FileMode(v)
	}
	if v, err := strconv.ParseUint(umask, 8, 32); err == nil && v <= 0o777 {
		return 0o777 &^ os.FileMode(v)
	}
	return 0o755
}

// ensureHome は、開発ユーザーのホームを、useradd -m と同じ結果で作る。
//
//   - 親ディレクトリ (/home など) が無ければ、root:root の 0755 で作る。
//   - ホームは、login.defs の権限で作り、ユーザーとそのグループの所有にする。
//   - /etc/skel の、ディレクトリと通常ファイルを、権限を保って複製し、ユーザーの所有にする。
//
// ホームが既にあれば、何もしない。
func ensureHome(plan *Plan) error {
	home := plan.UserHome
	if err := guardDest(plan.RootDir, home, true); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(home), 0o755); err != nil {
		return err
	}
	if err := os.Mkdir(home, plan.HomeMode); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return nil
		}
		return err
	}
	if err := os.Chmod(home, plan.HomeMode); err != nil {
		return err
	}
	if err := chownIfNeeded(home, plan.HomeUID, plan.HomeGID); err != nil {
		return err
	}
	plan.logf("[INFO] created home directory %s (mode %04o)\n", home, plan.HomeMode)
	return copySkel(filepath.Join(plan.RootDir, "etc", "skel"), home, plan.HomeUID, plan.HomeGID)
}

// copySkel は、skel の内容を、home に複製する。既にあるものは上書きしない。symlink などは複製しない。
func copySkel(skel, home string, uid, gid int) error {
	if fi, err := os.Stat(skel); err != nil || !fi.IsDir() {
		return nil
	}
	return filepath.WalkDir(skel, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(skel, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		target := filepath.Join(home, rel)
		perm := info.Mode().Perm()

		switch {
		case d.IsDir():
			if err := os.Mkdir(target, perm); err != nil {
				if errors.Is(err, fs.ErrExist) {
					return nil
				}
				return err
			}
			if err := os.Chmod(target, perm); err != nil {
				return err
			}
			return chownIfNeeded(target, uid, gid)
		case d.Type().IsRegular():
			if _, err := os.Lstat(target); err == nil {
				return nil
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			if err := os.WriteFile(target, data, perm); err != nil {
				return err
			}
			if err := os.Chmod(target, perm); err != nil {
				return err
			}
			return chownIfNeeded(target, uid, gid)
		}
		return nil
	})
}

// logf は、Plan.Log があれば、そこへ書式付きで出力する。
func (p *Plan) logf(format string, a ...any) {
	if p.Log != nil {
		fmt.Fprintf(p.Log, format, a...)
	}
}
