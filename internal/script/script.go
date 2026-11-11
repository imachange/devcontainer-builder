// Package script は、target/pre.d および target/post.d 配下のスクリプト実行を制御する。
package script

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"syscall"
	"time"
)

var (
	ErrMissingShebang  = errors.New("script missing shebang (#!) at start")
	ErrExecutionFailed = errors.New("script execution failed")
	ErrTimeout         = errors.New("script execution timed out")
)

// 禁止されている上書き不可環境変数
var protectedEnvKeys = map[string]bool{
	"PATH":            true,
	"LD_PRELOAD":      true,
	"LD_LIBRARY_PATH": true,
	"IFS":             true,
	"BASH_ENV":        true,
	"ENV":             true,
	"SHELLOPTS":       true,
}

// Config はスクリプト実行時の環境設定。
type Config struct {
	Timeout    time.Duration     // 1つのスクリプトあたりのタイムアウト（既定 10分）
	BuildArgs  map[string]string // build.args (BUILD_* 以外)
	DCBEnv     map[string]string // DCB_* 環境変数
	DCBEnvFunc func() map[string]string
	BaseEnv    []string // ベースイメージからの環境変数
	Stdout     io.Writer
	Stderr     io.Writer
}

// RunDir は指定ディレクトリ内のスクリプトを名前順に探索し実行する。
func RunDir(ctx context.Context, dir string, cfg Config) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to read script directory %s: %w", dir, err)
	}

	var scriptFiles []string
	for _, entry := range entries {
		name := entry.Name()
		// . や _ で始まるファイル・ディレクトリは部品用としてスキップ
		if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
			continue
		}
		if entry.Type().IsRegular() {
			scriptFiles = append(scriptFiles, name)
		}
	}

	sort.Strings(scriptFiles)

	for _, name := range scriptFiles {
		path := filepath.Join(dir, name)
		if err := RunFile(ctx, path, cfg); err != nil {
			return fmt.Errorf("script %s failed: %w", name, err)
		}
	}

	return nil
}

// RunFile は単一のスクリプトファイルを検証・一時複製した上で実行する。
func RunFile(ctx context.Context, scriptPath string, cfg Config) error {
	data, err := os.ReadFile(scriptPath)
	if err != nil {
		return fmt.Errorf("read script file error: %w", err)
	}

	// 1. Shebang チェック
	if !bytes.HasPrefix(data, []byte("#!")) {
		return fmt.Errorf("%w: %s", ErrMissingShebang, scriptPath)
	}

	// 2. 一時ディレクトリの作成とスクリプトの複製
	tmpDir, err := os.MkdirTemp("", "dcb-script-*")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	tmpScriptPath := filepath.Join(tmpDir, filepath.Base(scriptPath))
	if err := os.WriteFile(tmpScriptPath, data, 0o755); err != nil {
		return fmt.Errorf("failed to write temp script: %w", err)
	}
	if err := copyHelpers(filepath.Dir(scriptPath), tmpDir); err != nil {
		return fmt.Errorf("copy script helpers: %w", err)
	}

	// 3. タイムアウト設定
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(execCtx, tmpScriptPath)
	cmd.Stdout = cfg.Stdout
	cmd.Stderr = cfg.Stderr
	if cfg.DCBEnvFunc != nil {
		cfg.DCBEnv = cfg.DCBEnvFunc()
	}
	cmd.Env = prepareEnv(cfg)

	// プロセスグループを作成し、タイムアウト時に子プロセスごと全滅させる設定 (Unix)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start script: %w", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case <-execCtx.Done():
		if errors.Is(execCtx.Err(), context.DeadlineExceeded) {
			// プロセスグループ全体に SIGKILL を送信
			if cmd.Process != nil {
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			}
			return fmt.Errorf("%w: %s", ErrTimeout, filepath.Base(scriptPath))
		}
		return execCtx.Err()
	case err := <-done:
		if err != nil {
			return fmt.Errorf("%w: %v", ErrExecutionFailed, err)
		}
	}

	return nil
}

func copyHelpers(sourceDir, destDir string) error {
	entries, err := os.ReadDir(sourceDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if (!strings.HasPrefix(name, ".") && !strings.HasPrefix(name, "_")) || !entry.Type().IsRegular() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(filepath.Join(sourceDir, name))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(destDir, name), data, info.Mode().Perm()); err != nil {
			return err
		}
	}
	return nil
}

// prepareEnv はホワイトリスト・フィルタリングおよび DCB_* / build.args の注入を行う。
func prepareEnv(cfg Config) []string {
	envMap := make(map[string]string)

	// ベース環境変数から許可項目のみ抽出
	defaultPath := "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
	envMap["PATH"] = defaultPath
	envMap["DEBIAN_FRONTEND"] = "noninteractive"

	for _, e := range cfg.BaseEnv {
		kv := strings.SplitN(e, "=", 2)
		if len(kv) != 2 {
			continue
		}
		k, v := kv[0], kv[1]
		switch k {
		case "PATH", "HOME", "USER", "LOGNAME", "DEBIAN_FRONTEND":
			envMap[k] = v
		}
	}

	// build.args の追加（BUILD_* は除外、保護キーの上書き不可）
	for k, v := range cfg.BuildArgs {
		if strings.HasPrefix(k, "BUILD_") || protectedEnvKeys[k] {
			continue
		}
		envMap[k] = v
	}

	// DCB_* 変数の注入（保護キーの上書き不可）
	for k, v := range cfg.DCBEnv {
		if strings.HasPrefix(k, "DCB_") && !protectedEnvKeys[k] {
			envMap[k] = v
		}
	}

	var result []string
	for k, v := range envMap {
		result = append(result, fmt.Sprintf("%s=%s", k, v))
	}
	slices.Sort(result)
	return result
}
