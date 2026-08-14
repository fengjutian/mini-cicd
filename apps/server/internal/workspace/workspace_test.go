package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolve(t *testing.T) {
	root, err := os.MkdirTemp(".", ".workspace-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	if err := os.Mkdir(filepath.Join(root, "sub"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(root, "sub"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"../outside", filepath.Join(root, "sub")} {
		if _, err := Resolve(root, bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}
func TestCreateRefusesDirty(t *testing.T) {
	root, err := os.MkdirTemp(".", ".workspace-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	m, _ := New(root)
	path, err := m.Create("abc", 1)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(path, "x"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Create("abc", 1); err == nil {
		t.Fatal("accepted dirty workspace")
	}
}

func TestMakeSharedDoesNotFollowSymlink(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skip("symlinks unavailable")
	}
	before, _ := os.Stat(outside)
	if err := MakeShared(root); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(outside)
	if before.Mode().Perm() != after.Mode().Perm() {
		t.Fatal("followed workspace symlink")
	}
	inside, _ := os.Stat(file)
	if inside.Mode().Perm()&0o060 != 0o060 {
		t.Fatalf("group access not granted: %o", inside.Mode().Perm())
	}
}
