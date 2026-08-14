package database

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func Backup(db *sql.DB, destination string) error {
	abs, err := filepath.Abs(destination)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		return err
	}
	if _, err = os.Stat(abs); err == nil {
		return errors.New("backup destination already exists")
	} else if !os.IsNotExist(err) {
		return err
	}
	quoted := strings.ReplaceAll(filepath.ToSlash(abs), "'", "''")
	if _, err = db.Exec(`VACUUM INTO '` + quoted + `'`); err != nil {
		return fmt.Errorf("create SQLite backup: %w", err)
	}
	return os.Chmod(abs, 0o600)
}

func Restore(source, destination string) (string, error) {
	src, err := filepath.Abs(source)
	if err != nil {
		return "", err
	}
	dst, err := filepath.Abs(destination)
	if err != nil {
		return "", err
	}
	if src == dst {
		return "", errors.New("backup source and database destination must differ")
	}
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()
	header := make([]byte, 16)
	if _, err = io.ReadFull(in, header); err != nil || string(header) != "SQLite format 3\x00" {
		return "", errors.New("source is not a valid SQLite database")
	}
	if _, err = in.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	if err = os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".restore-*.db")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err = io.Copy(tmp, in); err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return "", err
	}
	previous := ""
	if _, statErr := os.Stat(dst); statErr == nil {
		previous = dst + ".before-restore-" + time.Now().UTC().Format("20060102T150405Z")
		if err = os.Rename(dst, previous); err != nil {
			return "", fmt.Errorf("preserve current database: %w", err)
		}
	} else if !os.IsNotExist(statErr) {
		return "", statErr
	}
	if err = os.Rename(tmpName, dst); err != nil {
		if previous != "" {
			_ = os.Rename(previous, dst)
		}
		return "", fmt.Errorf("install restored database: %w", err)
	}
	return previous, nil
}
