package pkgmgr

// DnfManager は、dnf / microdnf / yum のコマンド仕様を定義する構造体。
type DnfManager struct {
	Cmd string // dnf, microdnf, yum
}

// FormatPackage は、dnf 形式（name-version）の文字列を返す。
func (m *DnfManager) FormatPackage(item Item) string {
	if item.Version == "" {
		return item.Name
	}
	return item.Name + "-" + item.Version
}

// PreInstallSpec は、事前処理が不要なため nil を返す。
func (m *DnfManager) PreInstallSpec() *CommandSpec {
	return nil
}

// InstallSpec は、dnf/yum install の仕様を返す。
func (m *DnfManager) InstallSpec(pkgs []string) CommandSpec {
	args := append([]string{"install", "-y", "--allowerasing"}, pkgs...)
	return CommandSpec{
		Cmd:  m.Cmd,
		Args: args,
	}
}

// PostInstallCleanSpec は、clean all の仕様を返す。
func (m *DnfManager) PostInstallCleanSpec() *CommandSpec {
	return &CommandSpec{
		Cmd:  m.Cmd,
		Args: []string{"clean", "all"},
	}
}
