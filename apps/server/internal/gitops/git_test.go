package gitops

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charlesfeng/mini-cicd/apps/server/internal/database"
	"github.com/charlesfeng/mini-cicd/apps/server/internal/secret"
)

func gitTest(t *testing.T, auth string, password, key []byte, hosts string) (*Git, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := database.Open(filepath.Join(dir, "x.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err = database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	box, _ := secret.New(bytes.Repeat([]byte{3}, 32))
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = db.Exec(`INSERT INTO projects(id,name,slug,repository_url,branch,auth_type,git_username,git_secret_cipher,ssh_private_key_cipher,ssh_known_hosts,created_at,updated_at) VALUES('p','p','p','https://example/repo','main',?,'user',?,?,?, ?,?)`, auth, password, key, hosts, now, now)
	if err != nil {
		t.Fatal(err)
	}
	return New(dir, db, box), dir
}
func TestHTTPSEnvironmentUsesAskPass(t *testing.T) {
	box, _ := secret.New(bytes.Repeat([]byte{3}, 32))
	cipher, _ := box.Encrypt([]byte("token&danger"), "project:p:git-secret")
	g, _ := gitTest(t, "https", cipher, nil, "")
	env, cleanup, err := g.gitEnvironment("p")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, "GIT_ASKPASS=") || !strings.Contains(joined, "MINICICD_GIT_PASSWORD=token&danger") {
		t.Fatal("askpass environment missing")
	}
	for _, v := range env {
		if strings.HasPrefix(v, "GIT_ASKPASS=") {
			raw, err := os.ReadFile(strings.TrimPrefix(v, "GIT_ASKPASS="))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), "token&danger") {
				t.Fatal("token embedded in script")
			}
		}
	}
}
func TestSSHEnvironmentEnforcesKnownHosts(t *testing.T) {
	box, _ := secret.New(bytes.Repeat([]byte{3}, 32))
	key, _ := box.Encrypt([]byte("PRIVATE"), "project:p:ssh-key")
	g, _ := gitTest(t, "ssh", nil, key, "example ssh-ed25519 AAAA")
	env, cleanup, err := g.gitEnvironment("p")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, "StrictHostKeyChecking=yes") || !strings.Contains(joined, "UserKnownHostsFile=") {
		t.Fatalf("unsafe SSH environment: %s", joined)
	}
	if strings.Contains(joined, "PRIVATE") {
		t.Fatal("private key leaked into environment")
	}
}
func TestSSHRequiresKnownHosts(t *testing.T) {
	box, _ := secret.New(bytes.Repeat([]byte{3}, 32))
	key, _ := box.Encrypt([]byte("PRIVATE"), "project:p:ssh-key")
	g, _ := gitTest(t, "ssh", nil, key, "")
	if _, _, err := g.gitEnvironment("p"); err == nil {
		t.Fatal("SSH without known_hosts was accepted")
	}
}
