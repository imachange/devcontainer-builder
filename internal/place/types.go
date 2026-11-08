// Package place は、ターゲットディレクトリへの安全なファイル配置、テンプレート展開、権限設定、およびホームディレクトリの自動作成を行う。
package place

import (
	"os"

	"github.com/imachange/devcontainer-builder/internal/config"
)

// Mode は、ファイル処理の動作モードを表す。
type Mode int

const (
	ModeCopy Mode = iota
	ModeTemplate
	ModeAppend
	ModeTemplateAppend
)

// Action は、個別のファイル配置アクションを表す構造体。
type Action struct {
	SourcePath string
	RelPath    string
	DestPath   string
	Mode       Mode
	IsDir      bool
	Perm       os.FileMode
	UID        int
	GID        int
}

// Plan は、ファイル配置の事前計画を表す構造体。
type Plan struct {
	TargetUser string
	UserHome   string
	CreateHome bool
	Actions    []Action
	Diags      []config.Diagnostic
}
