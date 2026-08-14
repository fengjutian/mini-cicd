package runneripc

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestResolveRejectsWorkspaceEscape(t *testing.T) {
	root, err := os.MkdirTemp(".", ".runner-ipc-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	root, err = filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(root, "project", "1", "source")
	if err := os.MkdirAll(inside, 0o700); err != nil {
		t.Fatal(err)
	}
	s := &Server{root: root}
	if got, err := s.resolve(inside); err != nil || got == "" {
		t.Fatalf("inside path rejected: %q %v", got, err)
	}
	if _, err := s.resolve(filepath.Dir(root)); err == nil {
		t.Fatal("parent path accepted")
	}
	if runtime.GOOS != "windows" {
		link := filepath.Join(root, "escape")
		if err := os.Symlink(filepath.Dir(root), link); err == nil {
			if _, err = s.resolve(link); err == nil {
				t.Fatal("escaping symlink accepted")
			}
		}
	}
}

func TestNewServerRejectsRootJob(t *testing.T) {
	if _, err := NewServer(filepath.Join(os.TempDir(), "runner.sock"), os.TempDir(), "/bin/bash", 1, 0, 1, nil); err == nil {
		t.Fatal("root job UID accepted")
	}
}
