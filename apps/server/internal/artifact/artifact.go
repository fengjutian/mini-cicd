package artifact

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/charlesfeng/mini-cicd/apps/server/internal/pipelineconfig"
	"github.com/charlesfeng/mini-cicd/apps/server/internal/workspace"
)

type Store struct{ root string }

func New(dataDir string) (*Store, error) {
	root := filepath.Join(dataDir, "artifacts")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	return &Store{root: root}, nil
}
func (s *Store) Path(projectID string, deploymentID int64) string {
	return filepath.Join(s.root, projectID, fmt.Sprint(deploymentID))
}

func (s *Store) Save(projectID string, deploymentID int64, source string, cfg pipelineconfig.ArtifactConfig) (string, error) {
	if len(cfg.Paths) == 0 {
		return "", nil
	}
	target := s.Path(projectID, deploymentID)
	if err := os.RemoveAll(target); err != nil {
		return "", err
	}
	if err := os.MkdirAll(target, 0o700); err != nil {
		return "", err
	}
	for _, name := range cfg.Paths {
		from, err := workspace.Resolve(source, name)
		if err != nil {
			return "", err
		}
		if _, err = os.Lstat(from); err != nil {
			return "", fmt.Errorf("artifact %s: %w", name, err)
		}
		to := filepath.Join(target, filepath.FromSlash(name))
		if err = copyTree(from, to); err != nil {
			return "", fmt.Errorf("artifact %s: %w", name, err)
		}
	}
	return target, nil
}

func (s *Store) Restore(sourcePath, target string, cfg pipelineconfig.ArtifactConfig) error {
	if sourcePath == "" || len(cfg.Paths) == 0 {
		return errors.New("artifact snapshot is unavailable")
	}
	for _, name := range cfg.Paths {
		from := filepath.Join(sourcePath, filepath.FromSlash(name))
		to, err := workspace.Resolve(target, name)
		if err != nil {
			return err
		}
		if err = copyTree(from, to); err != nil {
			return fmt.Errorf("restore artifact %s: %w", name, err)
		}
	}
	return nil
}

func copyTree(from, to string) error {
	info, err := os.Lstat(from)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("symbolic links are not allowed in artifacts")
	}
	if !info.IsDir() {
		return copyFile(from, to, info.Mode())
	}
	return filepath.WalkDir(from, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		dest := filepath.Join(to, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("symbolic links are not allowed in artifacts")
		}
		if d.IsDir() {
			return os.MkdirAll(dest, info.Mode().Perm())
		}
		return copyFile(path, dest, info.Mode())
	})
}
func copyFile(from, to string, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
		return err
	}
	src, err := os.Open(from)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.OpenFile(to, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode.Perm())
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(dst, src)
	closeErr := dst.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}
