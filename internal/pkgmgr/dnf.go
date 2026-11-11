package pkgmgr

import (
	"fmt"
	"strings"
)

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

// InstallSpec は、dnf/yum/microdnf install の仕様を返す。
func (m *DnfManager) InstallSpec(pkgs []string) CommandSpec {
	// microdnf は --allowerasing を非サポートのため、
	// プリインストールの curl-minimal / libcurl-minimal との競合を
	// RPM DB レベルで解除 (--justdb) してから microdnf install を実行する
	if m.Cmd == "microdnf" {
		cmdStr := fmt.Sprintf(
			"if rpm -q curl-minimal >/dev/null 2>&1; then rpm -e --nodeps --justdb curl-minimal libcurl-minimal 2>/dev/null || true; fi && microdnf install -y %s",
			strings.Join(pkgs, " "),
		)
		return CommandSpec{
			Cmd:  "sh",
			Args: []string{"-c", cmdStr},
		}
	}

	// dnf / yum は --allowerasing を付与してパッケージ置換を許可する
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
