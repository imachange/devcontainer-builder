//go:build unix

package apply

import "syscall"

// setUmask は、umask を 022 にする。作成するファイルとディレクトリの権限が、
// 実行環境の umask に左右されないようにするため。
func setUmask() { syscall.Umask(0o022) }
