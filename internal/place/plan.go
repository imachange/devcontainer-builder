package place

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/imachange/devcontainer-builder/internal/config"
	"github.com/imachange/devcontainer-builder/internal/layout"
	"github.com/imachange/devcontainer-builder/internal/sysinfo"
)

// BuildPlan は、レイアウト、設定、およびシステム情報からファイル配置計画を構築する。
func BuildPlan(lay *layout.Layout, cfg *config.Settings, info sysinfo.Info, targetDir string, rootDir string) (*Plan, error) {
	if rootDir == "" {
		rootDir = "/"
	}

	targetHome := info.Home
	if info.Home != "" {
		targetHome = filepath.Join(rootDir, strings.TrimPrefix(info.Home, "/"))
	}

	plan := &Plan{
		TargetUser: info.User,
		UserHome:   targetHome,
	}

	if info.User != "" && info.User != "root" && targetHome != "" {
		if _, err := os.Stat(targetHome); os.IsNotExist(err) {
			plan.CreateHome = true
		}
	}

	if lay == nil {
		return plan, nil
	}

	var errs []error

	if hasTree(lay, "root") {
		filesRoot := filepath.Join(targetDir, "files", "root")
		actions, err := scanDir(filesRoot, rootDir, "", 0, 0)
		if err != nil {
			errs = append(errs, fmt.Errorf("files/root scan error: %w", err))
		} else {
			plan.Actions = append(plan.Actions, actions...)
		}
	}

	if hasTree(lay, "home") && info.Home != "" {
		filesHome := filepath.Join(targetDir, "files", "home")
		destBase := filepath.Join(rootDir, strings.TrimPrefix(info.Home, "/"))
		uid, gid := resolveUserIDs(info.User)
		actions, err := scanDir(filesHome, destBase, info.Home, uid, gid)
		if err != nil {
			errs = append(errs, fmt.Errorf("files/home scan error: %w", err))
		} else {
			plan.Actions = append(plan.Actions, actions...)
		}
	}

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}

	return plan, nil
}

func hasTree(lay *layout.Layout, treeName string) bool {
	if lay == nil {
		return false
	}
	for _, t := range lay.FileTrees {
		if t == treeName {
			return true
		}
	}
	return false
}

func resolveUserIDs(username string) (int, int) {
	if username == "" || username == "root" {
		return 0, 0
	}
	u, err := user.Lookup(username)
	if err != nil {
		return 0, 0
	}
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)
	return uid, gid
}

func isDeniedPath(relPath string) bool {
	cleaned := filepath.Clean("/" + relPath)
	denied := []string{
		"/etc/shadow",
		"/etc/passwd",
		"/etc/gshadow",
		"/etc/sudoers",
		"/proc",
		"/sys",
		"/dev",
	}
	for _, d := range denied {
		if cleaned == d || strings.HasPrefix(cleaned, d+"/") {
			return true
		}
	}
	return false
}

func resolvePerm(relPath string, isDir bool, srcPerm os.FileMode) os.FileMode {
	cleaned := filepath.Clean(relPath)

	if isDir {
		if strings.HasPrefix(cleaned, ".ssh") || strings.HasPrefix(cleaned, ".gnupg") {
			return 0700
		}
		return 0755
	}

	if strings.HasPrefix(cleaned, ".ssh/") || strings.HasPrefix(cleaned, ".gnupg/") {
		return 0600
	}
	if strings.HasPrefix(cleaned, "etc/sudoers.d/") || strings.HasPrefix(cleaned, ".sudoers.d/") {
		return 0440
	}

	if srcPerm&0111 != 0 {
		return 0755
	}
	return 0644
}

func scanDir(srcRoot, destBase, displayPrefix string, uid, gid int) ([]Action, error) {
	var actions []Action
	var errs []error

	err := filepath.WalkDir(srcRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == srcRoot {
			return nil
		}

		rel, err := filepath.Rel(srcRoot, path)
		if err != nil {
			return err
		}

		relPathForCheck := filepath.Join(displayPrefix, rel)
		if isDeniedPath(relPathForCheck) {
			errs = append(errs, fmt.Errorf("access to denied path rejected: %s", relPathForCheck))
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return err
		}

		mode := info.Mode()
		if mode&os.ModeSymlink != 0 || mode&os.ModeDevice != 0 || mode&os.ModeNamedPipe != 0 || mode&os.ModeSocket != 0 {
			errs = append(errs, fmt.Errorf("unsupported file type (symlink/device/pipe): %s", path))
			return nil
		}
		if mode&os.ModeSetuid != 0 || mode&os.ModeSetgid != 0 {
			errs = append(errs, fmt.Errorf("setuid/setgid bits are rejected: %s", path))
			return nil
		}

		isDir := d.IsDir()
		targetRel := rel
		actionMode := ModeCopy

		if !isDir {
			if strings.HasSuffix(rel, ".tmpl.append") {
				actionMode = ModeTemplateAppend
				targetRel = strings.TrimSuffix(rel, ".tmpl.append")
			} else if strings.HasSuffix(rel, ".append") {
				actionMode = ModeAppend
				targetRel = strings.TrimSuffix(rel, ".append")
			} else if strings.HasSuffix(rel, ".tmpl") {
				actionMode = ModeTemplate
				targetRel = strings.TrimSuffix(rel, ".tmpl")
			}
		}

		destPath := filepath.Join(destBase, targetRel)
		perm := resolvePerm(filepath.Join(displayPrefix, targetRel), isDir, mode.Perm())

		actions = append(actions, Action{
			SourcePath: path,
			RelPath:    targetRel,
			DestPath:   destPath,
			Mode:       actionMode,
			IsDir:      isDir,
			Perm:       perm,
			UID:        uid,
			GID:        gid,
		})

		return nil
	})

	if err != nil {
		return nil, err
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}

	return actions, nil
}
