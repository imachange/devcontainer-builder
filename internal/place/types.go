// Package place は、ターゲットディレクトリへの安全なファイル配置、テンプレート展開、権限設定、およびホームディレクトリの自動作成を行う。
package place

import (
	"errors"
	"io"
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

// IsAppend は、追記モードかどうかを返す。
func (m Mode) IsAppend() bool {
	return m == ModeAppend || m == ModeTemplateAppend
}

// ErrHomeUnresolved は、対象ユーザーのホームがまだ解決されていない計画を実行したときのエラー。
var ErrHomeUnresolved = errors.New("development user home is unresolved")

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
	TargetUser  string
	UserHome    string
	RootDir     string
	CreateHome  bool
	HomePending bool
	HomeMode    os.FileMode
	HomeUID     int
	HomeGID     int
	Overwrite   bool
	Actions     []Action
	Diags       []config.Diagnostic
	Log         io.Writer
}
