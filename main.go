// devcontainer-builder は、Dockerfile の RUN の中で、ビルド時だけ動く CLI。
//
// devcontainer.json の build.args と、target/ ディレクトリの内容にもとづいて、
// パッケージ導入、ファイル配置、スクリプト実行を行う。
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/imachange/devcontainer-builder/internal/apply"
	"github.com/imachange/devcontainer-builder/internal/sysinfo"
)

// version は、ビルド時に -ldflags "-X main.version=..." で埋め込む。
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// usageError は、使い方の誤り (終了コード 2)。
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

const usageText = `usage:
  devcontainer-builder apply [--config PATH] [--target PATH] [--check] [-v] <context-dir>
  devcontainer-builder detect
  devcontainer-builder version

exit codes: 0 success, 1 failure, 2 usage error
`

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usageText)
		return 2
	}

	var err error
	switch args[0] {
	case "apply":
		err = cmdApply(args[1:], stdout, stderr)
	case "detect":
		err = cmdDetect(args[1:], stdout)
	case "version":
		fmt.Fprintln(stdout, version)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usageText)
	default:
		err = &usageError{fmt.Sprintf("unknown command %q", args[0])}
	}
	if err == nil {
		return 0
	}

	fmt.Fprintln(stderr, "devcontainer-builder:", err)
	var ue *usageError
	if errors.As(err, &ue) {
		fmt.Fprint(stderr, usageText)
		return 2
	}
	return 1
}

func cmdApply(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("apply", flag.ContinueOnError)
	flags.SetOutput(io.Discard) // エラーは、usageError として、run が表示する
	cfg := flags.String("config", os.Getenv("BUILD_CONFIG"), "devcontainer.json, relative to the context")
	target := flags.String("target", os.Getenv("BUILD_TARGET_DIR"), "target directory, relative to the context")
	check := flags.Bool("check", false, "validate only (no root or Linux required)")
	verbose := flags.Bool("v", false, "also show INFO diagnostics")

	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(stdout, usageText)
			return nil
		}
		return &usageError{err.Error()}
	}
	if flags.NArg() != 1 {
		return &usageError{"apply requires exactly one argument: <context-dir> (flags go before it)"}
	}

	return apply.Run(flags.Arg(0), apply.Options{
		Config:  *cfg,
		Target:  *target,
		Check:   *check,
		Verbose: *verbose,
	}, stderr)
}

// cmdDetect は、DCB_* の判定結果を、KEY=VALUE で出力する (診断用)。
func cmdDetect(args []string, stdout io.Writer) error {
	if len(args) != 0 {
		return &usageError{"detect takes no arguments"}
	}
	info := sysinfo.System().Detect("", os.Getenv("BUILD_USER"))
	for _, kv := range info.Env() {
		fmt.Fprintln(stdout, kv)
	}
	return nil
}
