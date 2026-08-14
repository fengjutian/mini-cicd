package project

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/charlesfeng/mini-cicd/apps/server/internal/database"
	"github.com/charlesfeng/mini-cicd/apps/server/internal/secret"
)

func TestProjectCRUDAndEncryptedVersions(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	box, _ := secret.New(bytes.Repeat([]byte{5}, 32))
	s := New(db, box)
	ctx := context.Background()
	p, err := s.Create(ctx, Input{Name: "App", Slug: "app", RepositoryURL: "https://example.com/app.git", Branch: "main", AuthType: "https", GitUsername: "deploy", GitSecret: "git-token-value", BuildSteps: []Step{{Name: "Build", Command: "go build ./..."}}})
	if err != nil {
		t.Fatal(err)
	}
	if !p.HasGitSecret {
		t.Fatal("credential flag is incorrect")
	}
	raw, _ := json.Marshal(p)
	if bytes.Contains(raw, []byte("git-token-value")) {
		t.Fatal("credential leaked through project JSON")
	}
	var cipher []byte
	if err = db.QueryRow(`SELECT git_secret_cipher FROM projects WHERE id=?`, p.ID).Scan(&cipher); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(cipher, []byte("git-token-value")) {
		t.Fatal("credential stored in plaintext")
	}
	p.Name = "Renamed"
	updated, err := s.Update(ctx, p.ID, Input{Name: p.Name, Slug: p.Slug, RepositoryURL: p.RepositoryURL, Branch: p.Branch, AuthType: "https", GitUsername: "deploy", BuildSteps: p.BuildSteps, StepTimeoutSeconds: p.StepTimeoutSeconds, DeploymentTimeoutSeconds: p.DeploymentTimeoutSeconds})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "Renamed" || !updated.HasGitSecret {
		t.Fatalf("bad update: %#v", updated)
	}
	v1, err := s.PutVariable(ctx, p.ID, VariableInput{Name: "API_KEY", Value: "first-secret", IsSecret: true})
	if err != nil {
		t.Fatal(err)
	}
	v2, err := s.PutVariable(ctx, p.ID, VariableInput{Name: "API_KEY", Value: "second-secret", IsSecret: true})
	if err != nil {
		t.Fatal(err)
	}
	if v1.Version != 1 || v2.Version != 2 || v2.Value != "" {
		t.Fatalf("bad versions: %#v %#v", v1, v2)
	}
	vars, err := s.ListVariables(ctx, p.ID)
	if err != nil || len(vars) != 1 || vars[0].Value != "" || !vars[0].IsSecret {
		t.Fatalf("bad variable list: %#v %v", vars, err)
	}
	if err = s.Archive(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Get(ctx, p.ID); err == nil {
		t.Fatal("archived project remained visible")
	}
}

func TestRejectsStepWorkingDirectoryOutsideRepository(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	box, _ := secret.New(bytes.Repeat([]byte{5}, 32))
	s := New(db, box)
	in := Input{Name: "App", Slug: "app", RepositoryURL: "https://example.com/app.git", Branch: "main", AuthType: "none", BuildSteps: []Step{{Name: "Build", Command: "echo ok", WorkingDirectory: "../outside"}}}
	if _, err = s.Create(context.Background(), in); err == nil {
		t.Fatal("expected parent working directory to be rejected")
	}
	in.BuildSteps[0].WorkingDirectory = filepath.Join(string(filepath.Separator), "outside")
	if _, err = s.Create(context.Background(), in); err == nil {
		t.Fatal("expected absolute working directory to be rejected")
	}
}
