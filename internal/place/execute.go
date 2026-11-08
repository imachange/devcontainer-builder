package place

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// Execute は、計画されたファイル配置・権限設定・ホーム作成を実行する。
func Execute(ctx context.Context, plan *Plan, vars map[string]string) error {
	if plan == nil {
		return nil
	}

	if plan.CreateHome && plan.UserHome != "" {
		if err := ensureHomeDir(plan.UserHome); err != nil {
			return fmt.Errorf("failed to create home directory for %s: %w", plan.TargetUser, err)
		}
	}

	for _, action := range plan.Actions {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if action.IsDir {
			if err := os.MkdirAll(action.DestPath, action.Perm); err != nil {
				return fmt.Errorf("failed to create directory %s: %w", action.DestPath, err)
			}
			setOwner(action.DestPath, action.UID, action.GID)
			continue
		}

		parentDir := filepath.Dir(action.DestPath)
		if err := os.MkdirAll(parentDir, 0755); err != nil {
			return fmt.Errorf("failed to create parent directory for %s: %w", action.DestPath, err)
		}

		var content []byte
		var err error

		switch action.Mode {
		case ModeCopy:
			content, err = os.ReadFile(action.SourcePath)
			if err != nil {
				return fmt.Errorf("failed to read source %s: %w", action.SourcePath, err)
			}
			err = os.WriteFile(action.DestPath, content, action.Perm)

		case ModeTemplate:
			content, err = renderTemplate(action.SourcePath, vars)
			if err != nil {
				return fmt.Errorf("failed to render template %s: %w", action.SourcePath, err)
			}
			err = os.WriteFile(action.DestPath, content, action.Perm)

		case ModeAppend:
			content, err = os.ReadFile(action.SourcePath)
			if err != nil {
				return fmt.Errorf("failed to read source %s: %w", action.SourcePath, err)
			}
			err = appendToFile(action.DestPath, content, action.Perm)

		case ModeTemplateAppend:
			content, err = renderTemplate(action.SourcePath, vars)
			if err != nil {
				return fmt.Errorf("failed to render template %s: %w", action.SourcePath, err)
			}
			err = appendToFile(action.DestPath, content, action.Perm)
		}

		if err != nil {
			return fmt.Errorf("failed to write %s: %w", action.DestPath, err)
		}

		_ = os.Chmod(action.DestPath, action.Perm)
		setOwner(action.DestPath, action.UID, action.GID)
	}

	return nil
}
