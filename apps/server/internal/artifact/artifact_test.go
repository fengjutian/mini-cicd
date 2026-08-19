package artifact

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/charlesfeng/mini-cicd/apps/server/internal/pipelineconfig"
)

func TestSaveAndRestore(t *testing.T) {
	data := t.TempDir()
	source := filepath.Join(data, "source")
	if err := os.MkdirAll(filepath.Join(source, "dist"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "dist", "app.js"), []byte("version one"), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := New(data)
	if err != nil {
		t.Fatal(err)
	}
	cfg := pipelineconfig.ArtifactConfig{Paths: []string{"dist"}, Retention: 5}
	saved, err := store.Save("p", 1, source, cfg)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(data, "target")
	if err = os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err = store.Restore(saved, target, cfg); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(target, "dist", "app.js"))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "version one" {
		t.Fatalf("got %q", raw)
	}
}
