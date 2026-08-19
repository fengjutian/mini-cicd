package pipelinecache

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charlesfeng/mini-cicd/apps/server/internal/pipelineconfig"
)

type Store struct{ root string }

func New(dataDir string) (*Store, error) {
	root := filepath.Join(dataDir, "pipeline-cache")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	return &Store{root: root}, nil
}

func (s *Store) Key(workspace string, cfg pipelineconfig.CacheConfig) (string, error) {
	h := sha256.New()
	_, _ = io.WriteString(h, cfg.Key+"\x00")
	for _, name := range cfg.KeyFiles {
		file, err := safeJoin(workspace, name)
		if err != nil {
			return "", err
		}
		raw, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("cache key file %s: %w", name, err)
		}
		_, _ = io.WriteString(h, name+"\x00")
		_, _ = h.Write(raw)
	}
	return cfg.Key + "-" + hex.EncodeToString(h.Sum(nil))[:16], nil
}

func (s *Store) Restore(project, key, workspace string, cfg pipelineconfig.CacheConfig) (bool, string, error) {
	base := filepath.Join(s.root, project)
	chosen := key
	if _, err := os.Stat(filepath.Join(base, key)); errors.Is(err, os.ErrNotExist) {
		entries, _ := os.ReadDir(base)
		names := []string{}
		for _, entry := range entries {
			for _, prefix := range cfg.RestoreKeys {
				if entry.IsDir() && strings.HasPrefix(entry.Name(), prefix) {
					names = append(names, entry.Name())
				}
			}
		}
		sort.Sort(sort.Reverse(sort.StringSlice(names)))
		if len(names) == 0 {
			return false, key, nil
		}
		chosen = names[0]
	}
	for _, name := range cfg.Paths {
		from, err := safeJoin(filepath.Join(base, chosen), name)
		if err != nil {
			return false, chosen, err
		}
		if _, err = os.Lstat(from); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return false, chosen, err
		}
		to, err := safeJoin(workspace, name)
		if err != nil {
			return false, chosen, err
		}
		if err = os.RemoveAll(to); err != nil {
			return false, chosen, err
		}
		if err = copyTree(from, to); err != nil {
			return false, chosen, err
		}
	}
	return true, chosen, nil
}

func (s *Store) Save(project, key, workspace string, cfg pipelineconfig.CacheConfig) error {
	target := filepath.Join(s.root, project, key)
	_ = os.RemoveAll(target)
	for _, name := range cfg.Paths {
		from, err := safeJoin(workspace, name)
		if err != nil {
			return err
		}
		if _, err = os.Lstat(from); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return err
		}
		to, _ := safeJoin(target, name)
		if err = copyTree(from, to); err != nil {
			return err
		}
	}
	entries, _ := os.ReadDir(filepath.Join(s.root, project))
	if len(entries) > cfg.Retention {
		sort.Slice(entries, func(i, j int) bool {
			a, _ := entries[i].Info()
			b, _ := entries[j].Info()
			return a.ModTime().After(b.ModTime())
		})
		for _, e := range entries[cfg.Retention:] {
			_ = os.RemoveAll(filepath.Join(s.root, project, e.Name()))
		}
	}
	return nil
}

func safeJoin(root, name string) (string, error) {
	root, _ = filepath.Abs(root)
	target, _ := filepath.Abs(filepath.Join(root, filepath.Clean(name)))
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		return "", errors.New("cache path escapes workspace")
	}
	return target, nil
}
func copyTree(from, to string) error {
	info, err := os.Lstat(from)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("symbolic links are not allowed in cache")
	}
	if !info.IsDir() {
		return copyFile(from, to, info.Mode())
	}
	return filepath.WalkDir(from, func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		i, e := d.Info()
		if e != nil {
			return e
		}
		if i.Mode()&os.ModeSymlink != 0 {
			return errors.New("symbolic links are not allowed in cache")
		}
		rel, _ := filepath.Rel(from, p)
		dest := filepath.Join(to, rel)
		if d.IsDir() {
			return os.MkdirAll(dest, i.Mode().Perm())
		}
		return copyFile(p, dest, i.Mode())
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
	_, e := io.Copy(dst, src)
	ce := dst.Close()
	if e != nil {
		return e
	}
	return ce
}
