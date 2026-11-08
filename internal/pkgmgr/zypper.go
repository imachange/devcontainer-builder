package pkgmgr

// ZypperManager は、zypper のコマンド仕様を定義する構造体。
type ZypperManager struct{}

// FormatPackage は、zypper 形式（name=version）の文字列を返す。
func (m *ZypperManager) FormatPackage(item Item) string {
	if item.Version == "" {
		return item.Name
	}
	return item.Name + "=" + item.Version
}

// PreInstallSpec は、事前処理が不要なため nil を返す。
func (m *ZypperManager) PreInstallSpec() *CommandSpec {
	return nil
}

// InstallSpec は、zypper install の仕様を返す。
func (m *ZypperManager) InstallSpec(pkgs []string) CommandSpec {
	args := append([]string{"--non-interactive", "install", "--no-recommends"}, pkgs...)
	return CommandSpec{
		Cmd:  "zypper",
		Args: args,
	}
}

// PostInstallCleanSpec は、zypper clean -a の仕様を返す。
func (m *ZypperManager) PostInstallCleanSpec() *CommandSpec {
	return &CommandSpec{
		Cmd:  "zypper",
		Args: []string{"clean", "-a"},
	}
}
