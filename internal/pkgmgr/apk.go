package pkgmgr

// ApkManager は、apk のコマンド仕様を定義する構造体。
type ApkManager struct{}

// FormatPackage は、apk 形式（name=version）の文字列を返す。
func (m *ApkManager) FormatPackage(item Item) string {
	if item.Version == "" {
		return item.Name
	}
	return item.Name + "=" + item.Version
}

// PreInstallSpec は、事前処理が不要なため nil を返す。
func (m *ApkManager) PreInstallSpec() *CommandSpec {
	return nil
}

// InstallSpec は、apk add --no-cache の仕様を返す。
func (m *ApkManager) InstallSpec(pkgs []string) CommandSpec {
	args := append([]string{"add", "--no-cache"}, pkgs...)
	return CommandSpec{
		Cmd:  "apk",
		Args: args,
	}
}

// PostInstallCleanSpec は、--no-cache を使用しているため不要であり nil を返す。
func (m *ApkManager) PostInstallCleanSpec() *CommandSpec {
	return nil
}
