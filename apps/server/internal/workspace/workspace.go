package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type Manager struct {
	root string
	mode os.FileMode
}

func New(dataDir string) (*Manager, error) {
	return NewRoot(filepath.Join(dataDir, "workspaces"), 0700)
}
func NewRoot(path string, mode os.FileMode) (*Manager, error) {
	root, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(root, mode); err != nil {
		return nil, err
	}
	if runtime.GOOS != "windows" {
		if resolved, e := filepath.EvalSymlinks(root); e == nil {
			root = resolved
		} else {
			return nil, e
		}
	}
	return &Manager{root: root, mode: mode}, nil
}
func (m *Manager) Create(projectID string, deploymentID int64) (string, error) {
	if !safeID(projectID) {
		return "", errors.New("invalid project ID")
	}
	path := filepath.Join(m.root, projectID, fmt.Sprint(deploymentID), "source")
	if !within(m.root, path) {
		return "", errors.New("workspace escaped root")
	}
	if entries, err := os.ReadDir(path); err == nil && len(entries) > 0 {
		return "", errors.New("workspace already exists and is not empty")
	}
	if err := os.MkdirAll(path, m.mode); err != nil {
		return "", err
	}
	return filepath.Abs(path)
}
func Resolve(root, relative string) (string, error) {
	if filepath.IsAbs(relative) || filepath.VolumeName(relative) != "" || strings.ContainsRune(relative, 0) {
		return "", errors.New("working directory must be relative")
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	target, err := filepath.Abs(filepath.Join(rootAbs, filepath.Clean(relative)))
	if err != nil || !within(rootAbs, target) {
		return "", errors.New("working directory escapes workspace")
	}
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		return "", err
	}
	if !within(rootAbs, resolved) {
		return "", errors.New("working directory symlink escapes workspace")
	}
	return resolved, nil
}

// MakeShared grants the isolated job user's shared group write access without
// following repository symlinks. It must only be used on a validated workspace.
func MakeShared(root string) error {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	return filepath.WalkDir(rootAbs, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !within(rootAbs, path) {
			return errors.New("workspace permission target escaped root")
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		mode := os.FileMode(0o660)
		if entry.IsDir() || info.Mode()&0o111 != 0 {
			mode = 0o770
		}
		return os.Chmod(path, mode)
	})
}
func (m *Manager) Remove(projectID string, deploymentID int64) error {
	if !safeID(projectID) {
		return errors.New("invalid project ID")
	}
	target := filepath.Join(m.root, projectID, fmt.Sprint(deploymentID))
	if !within(m.root, target) {
		return errors.New("refusing unsafe cleanup")
	}
	return os.RemoveAll(target)
}
func within(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && !filepath.IsAbs(rel)
}
func safeID(v string) bool {
	if v == "" || v == "." || v == ".." {
		return false
	}
	return !strings.ContainsAny(v, "/\\:")
}
