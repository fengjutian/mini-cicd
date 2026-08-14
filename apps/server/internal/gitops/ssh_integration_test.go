//go:build integration

package gitops_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/charlesfeng/mini-cicd/apps/server/internal/database"
	"github.com/charlesfeng/mini-cicd/apps/server/internal/gitops"
	"github.com/charlesfeng/mini-cicd/apps/server/internal/project"
	"github.com/charlesfeng/mini-cicd/apps/server/internal/secret"
)

func TestRealSSHPrivateRepository(t *testing.T) {
	repo, key, knownHosts := os.Getenv("MINICICD_TEST_SSH_REPOSITORY"), os.Getenv("MINICICD_TEST_SSH_PRIVATE_KEY"), os.Getenv("MINICICD_TEST_SSH_KNOWN_HOSTS")
	if repo == "" || key == "" || knownHosts == "" {
		t.Skip("real SSH repository credentials are not configured")
	}
	data := t.TempDir()
	db, err := database.Open(filepath.Join(data, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	box, err := secret.New([]byte("01234567890123456789012345678901"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := project.New(db, box).Create(context.Background(), project.Input{Name: "SSH integration", Slug: "ssh-integration", RepositoryURL: repo, Branch: "main", AuthType: "ssh", SSHPrivateKey: key, SSHKnownHosts: knownHosts, StepTimeoutSeconds: 60, DeploymentTimeoutSeconds: 120})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	commit, err := gitops.New(data, db, box).Resolve(ctx, p.ID, repo, "main")
	if err != nil {
		t.Fatal(err)
	}
	if len(commit.SHA) != 40 {
		t.Fatalf("expected immutable SHA, got %q", commit.SHA)
	}
}
