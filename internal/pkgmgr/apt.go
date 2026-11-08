package pkgmgr

// AptManager は、apt-get のコマンド仕様を定義する構造体。
type AptManager struct{}

// FormatPackage は、apt-get 形式（name=version）の文字列を返す。
func (m *AptManager) FormatPackage(item Item) string {
	if item.Version == "" {
		return item.Name
	}
	return item.Name + "=" + item.Version
}

// PreInstallSpec は、apt-get update の仕様を返す。
func (m *AptManager) PreInstallSpec() *CommandSpec {
	return &CommandSpec{
		Cmd:  "apt-get",
		Args: []string{"update"},
		Env:  []string{"DEBIAN_FRONTEND=noninteractive"},
	}
}

// InstallSpec は、apt-get install の仕様を返す。
func (m *AptManager) InstallSpec(pkgs []string) CommandSpec {
	args := append([]string{"install", "-y", "--no-install-recommends"}, pkgs...)
	return CommandSpec{
		Cmd:  "apt-get",
		Args: args,
		Env:  []string{"DEBIAN_FRONTEND=noninteractive"},
	}
}

// PostInstallCleanSpec は、apt-get clean の仕様を返す。
func (m *AptManager) PostInstallCleanSpec() *CommandSpec {
	return &CommandSpec{
		Cmd:  "apt-get",
		Args: []string{"clean"},
		Env:  []string{"DEBIAN_FRONTEND=noninteractive"},
	}
}
