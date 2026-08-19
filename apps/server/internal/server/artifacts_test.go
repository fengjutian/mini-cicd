package server

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestArtifactManifestAndDownload(t *testing.T) {
	handler := newTestServer(t)
	s := handler.(*Server)
	setup := requestJSON(t, handler, http.MethodPost, "/api/v1/setup", map[string]string{"email": "owner@example.com", "username": "owner", "password": "correct horse battery staple", "confirmPassword": "correct horse battery staple"})
	cookie := setup.Result().Cookies()[0]
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := s.db.Exec(`INSERT INTO projects(id,name,slug,repository_url,branch,created_at,updated_at) VALUES('p','p','p','https://example/repo','main',?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "dist"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "dist", "app.txt"), []byte("artifact"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := s.db.Exec(`INSERT INTO deployments(project_id,status,trigger_type,branch,artifact_path,created_at) VALUES('p','succeeded','manual','main',?,?)`, root, now)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	base := "/api/v1/deployments/" + strconv.FormatInt(id, 10) + "/artifacts"
	manifest := requestJSON(t, handler, http.MethodGet, base, nil, cookie)
	if manifest.Code != http.StatusOK || !bytes.Contains(manifest.Body.Bytes(), []byte(`"path":"dist/app.txt"`)) || !bytes.Contains(manifest.Body.Bytes(), []byte(`c7c5c1d70c5dec4416ab6158afd0b223ef40c29b1dc1f97ed9428b94d4cadb1c`)) {
		t.Fatalf("manifest: %d %s", manifest.Code, manifest.Body.String())
	}
	download := requestJSON(t, handler, http.MethodGet, base+"/dist/app.txt", nil, cookie)
	if download.Code != http.StatusOK || download.Body.String() != "artifact" {
		t.Fatalf("download: %d %q", download.Code, download.Body.String())
	}
}
