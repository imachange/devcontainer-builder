package place

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// Execute は、Plan のアクションを、順に実行する。
//
// 順序は、ディレクトリ、置換、追記。各ファイルは、書き込みの直前に、配置先の安全を確認する (guardDest)。
// Plan.CreateHome が true なら、先にホームを作る。Plan.HomePending のままの Plan は、実行できない。
// vars は、テンプレートの変数。
func Execute(ctx context.Context, plan *Plan, vars map[string]string) error {
	if plan == nil {
		return nil
	}
	if plan.HomePending {
		return ErrHomeUnresolved
	}
	if plan.CreateHome {
		if err := ensureHome(plan); err != nil {
			return fmt.Errorf("failed to create home directory %s: %w", plan.UserHome, err)
		}
	}

	for _, a := range plan.Actions {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := executeAction(plan, a, vars); err != nil {
			return fmt.Errorf("%s: %w", a.DestPath, err)
		}
	}
	return nil
}

func executeAction(plan *Plan, a Action, vars map[string]string) error {
	if err := guardDest(plan.RootDir, a.DestPath, a.IsDir); err != nil {
		return err
	}
	if a.IsDir {
		return makeDir(a)
	}

	content, err := readSource(a, vars)
	if err != nil {
		return err
	}
	if a.Mode.IsAppend() {
		return appendTo(plan, a, content)
	}

	fi, err := os.Lstat(a.DestPath)
	exists := err == nil
	switch {
	case exists && fi.IsDir():
		return errors.New("destination is a directory")
	case exists && !plan.Overwrite:
		return fmt.Errorf("overwriting existing file %s is forbidden (BUILD_OVERWRITE=false)", a.DestPath)
	case err != nil && !errors.Is(err, fs.ErrNotExist):
		return err
	}

	if err := writeAtomic(a.DestPath, content, a.Perm, a.UID, a.GID); err != nil {
		return err
	}
	if exists {
		plan.logf("[INFO] replaced existing file: %s\n", a.DestPath)
	}
	return nil
}
