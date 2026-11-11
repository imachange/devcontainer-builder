package place

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/imachange/devcontainer-builder/internal/config"
	"github.com/imachange/devcontainer-builder/internal/layout"
	"github.com/imachange/devcontainer-builder/internal/sysinfo"
)

var lookupUser = user.Lookup

type resolvedHome struct {
	*user.User
	UID int
	GID int
}

// BuildPlan は、レイアウト、設定、およびシステム情報からファイル配置計画を構築する。
func BuildPlan(lay *layout.Layout, cfg *config.Settings, info sysinfo.Info, targetDir string, rootDir string) (*Plan, error) {
	if rootDir == "" {
		rootDir = "/"
	}
	rootDir = filepath.Clean(rootDir)
	plan := &Plan{
		TargetUser: info.User,
		RootDir:    rootDir,
		Overwrite:  cfg == nil || cfg.Overwrite,
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

	if hasTree(lay, "home") {
		switch {
		case info.User == "root":
			homeDir := "/root"
			plan.UserHome = filepath.Join(rootDir, strings.TrimPrefix(filepath.Clean(homeDir), string(filepath.Separator)))
			actions, err := scanDir(filepath.Join(targetDir, "files", "home"), plan.UserHome, homeDir, 0, 0)
			if err != nil {
				errs = append(errs, fmt.Errorf("files/home scan error: %w", err))
			} else {
				plan.Actions = append(plan.Actions, actions...)
			}
		case info.Home == "":
			plan.HomePending = true
			_, err := scanDir(filepath.Join(targetDir, "files", "home"), string(filepath.Separator), "", 0, 0)
			if err != nil {
				errs = append(errs, fmt.Errorf("files/home scan error: %w", err))
			}
		default:
			home, err := resolveHome(info.User, info.Home)
			if err != nil {
				errs = append(errs, err)
				break
			}
			plan.UserHome = filepath.Join(rootDir, strings.TrimPrefix(home.HomeDir, string(filepath.Separator)))
			plan.HomeUID, plan.HomeGID = home.UID, home.GID
			plan.HomeMode = homeModeFromFile(filepath.Join(rootDir, "etc", "login.defs"))
			homeInfo, err := os.Lstat(plan.UserHome)
			switch {
			case err == nil:
				if !homeInfo.IsDir() {
					errs = append(errs, fmt.Errorf("home path %s is not a directory", plan.UserHome))
				}
			case errors.Is(err, fs.ErrNotExist):
				plan.CreateHome = true
			case err != nil:
				errs = append(errs, fmt.Errorf("inspect home directory %s: %w", plan.UserHome, err))
			}
			actions, err := scanDir(filepath.Join(targetDir, "files", "home"), plan.UserHome, home.HomeDir, home.UID, home.GID)
			if err != nil {
				errs = append(errs, fmt.Errorf("files/home scan error: %w", err))
			} else {
				plan.Actions = append(plan.Actions, actions...)
			}
		}
	}

	if !plan.HomePending {
		if err := validateDestinations(plan.Actions); err != nil {
			errs = append(errs, err)
		}
		sort.SliceStable(plan.Actions, func(i, j int) bool {
			a, b := plan.Actions[i], plan.Actions[j]
			if a.IsDir != b.IsDir {
				return a.IsDir
			}
			if a.Mode.IsAppend() != b.Mode.IsAppend() {
				return !a.Mode.IsAppend()
			}
			return a.DestPath < b.DestPath
		})
	}

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}

	return plan, nil
}

func resolveHome(username, homeDir string) (*resolvedHome, error) {
	if !filepath.IsAbs(homeDir) || filepath.Clean(homeDir) == string(filepath.Separator) {
		return nil, fmt.Errorf("invalid home directory %q for user %s", homeDir, username)
	}
	u, err := lookupUser(username)
	if err != nil {
		return nil, fmt.Errorf("lookup user %s: %w", username, err)
	}
	if filepath.Clean(u.HomeDir) != filepath.Clean(homeDir) {
		return nil, fmt.Errorf("home directory %q does not match user %s home %q", homeDir, username, u.HomeDir)
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return nil, fmt.Errorf("invalid uid %q for user %s: %w", u.Uid, username, err)
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return nil, fmt.Errorf("invalid gid %q for user %s: %w", u.Gid, username, err)
	}
	return &resolvedHome{User: u, UID: uid, GID: gid}, nil
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

func isDeniedPath(relPath string) bool {
	cleaned := filepath.Clean("/" + relPath)
	denied := []string{
		"/etc/shadow",
		"/etc/shadow-",
		"/etc/passwd",
		"/etc/group",
		"/etc/gshadow",
		"/etc/sudoers",
		"/etc/ld.so.preload",
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
	cleaned := filepath.ToSlash(filepath.Clean(relPath))
	parts := strings.Split(cleaned, "/")
	hasComponent := func(name string) bool {
		for _, part := range parts {
			if part == name {
				return true
			}
		}
		return false
	}

	if isDir {
		if hasComponent(".ssh") || hasComponent(".gnupg") {
			return 0700
		}
		return 0755
	}

	if filepath.Base(cleaned) == ".netrc" {
		return 0600
	}
	if hasComponent(".gnupg") || (hasComponent(".ssh") && !strings.HasSuffix(cleaned, ".pub")) {
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
		targetRel, actionMode := rel, ModeCopy
		if !isDir {
			targetRel, actionMode, err = parseName(rel)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", rel, err))
				return nil
			}
		}

		relPathForCheck := filepath.Join(displayPrefix, targetRel)
		if isDeniedPath(relPathForCheck) {
			errs = append(errs, fmt.Errorf("access to denied path rejected: %s", relPathForCheck))
			return nil
		}

		destPath := filepath.Join(destBase, targetRel)
		permPath := targetRel
		if !filepath.IsAbs(displayPrefix) {
			permPath = filepath.Join(displayPrefix, targetRel)
		}
		perm := resolvePerm(permPath, isDir, mode.Perm())

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

func parseName(name string) (string, Mode, error) {
	mode := ModeCopy
	base := name
	switch {
	case strings.HasSuffix(name, ".tmpl.append"), strings.HasSuffix(name, ".append.tmpl"):
		mode = ModeTemplateAppend
		if strings.HasSuffix(name, ".tmpl.append") {
			base = strings.TrimSuffix(strings.TrimSuffix(name, ".append"), ".tmpl")
		} else {
			base = strings.TrimSuffix(strings.TrimSuffix(name, ".tmpl"), ".append")
		}
	case strings.HasSuffix(name, ".tmpl"):
		mode = ModeTemplate
		base = strings.TrimSuffix(name, ".tmpl")
	case strings.HasSuffix(name, ".append"):
		mode = ModeAppend
		base = strings.TrimSuffix(name, ".append")
	}
	if base == "" || strings.HasSuffix(base, string(filepath.Separator)) || filepath.Base(base) == "." || filepath.Base(base) == "" {
		return "", 0, fmt.Errorf("invalid or duplicated placement suffix")
	}
	if strings.HasSuffix(base, ".tmpl") || strings.HasSuffix(base, ".append") {
		return "", 0, fmt.Errorf("duplicated placement suffix")
	}
	return base, mode, nil
}

func validateDestinations(actions []Action) error {
	seen := make(map[string]map[bool]Action, len(actions))
	isDir := make(map[string]bool, len(actions))
	hasType := make(map[string]bool, len(actions))
	for _, action := range actions {
		if hasType[action.DestPath] && isDir[action.DestPath] != action.IsDir {
			return fmt.Errorf("destination %s is both a directory and a file", action.DestPath)
		}
		if seen[action.DestPath] == nil {
			seen[action.DestPath] = make(map[bool]Action)
		}
		category := action.Mode.IsAppend()
		if previous, ok := seen[action.DestPath][category]; ok {
			switch {
			case previous.IsDir != action.IsDir:
				return fmt.Errorf("destination %s is both a directory and a file", action.DestPath)
			case category:
				return fmt.Errorf("destination %s has both append actions", action.DestPath)
			default:
				return fmt.Errorf("destination %s has both replace actions", action.DestPath)
			}
		}
		seen[action.DestPath][category] = action
		isDir[action.DestPath] = action.IsDir
		hasType[action.DestPath] = true
	}
	for path, categories := range seen {
		for _, action := range categories {
			if action.IsDir {
				continue
			}
			for parent := filepath.Dir(path); parent != "." && parent != string(filepath.Separator); parent = filepath.Dir(parent) {
				if parentCategories, ok := seen[parent]; ok {
					for _, parentAction := range parentCategories {
						if !parentAction.IsDir {
							return fmt.Errorf("destination %s is both a file and a parent of %s", parent, path)
						}
					}
				}
			}
		}
	}
	return nil
}
