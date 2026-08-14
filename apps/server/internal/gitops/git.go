package gitops

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/charlesfeng/mini-cicd/apps/server/internal/deployment"
)

var shaPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

type Git struct{ dataDir string }

func New(dataDir string) *Git { return &Git{dataDir: dataDir} }
func (g *Git) Resolve(ctx context.Context, _, repo, branch string) (deployment.Commit, error) {
	ref := "refs/heads/" + branch
	out, err := run(ctx, "", "git", "ls-remote", "--exit-code", repo, ref)
	if err != nil {
		return deployment.Commit{}, err
	}
	fields := strings.Fields(out)
	if len(fields) < 2 || fields[1] != ref || !shaPattern.MatchString(fields[0]) {
		return deployment.Commit{}, errors.New("branch did not resolve to a full commit SHA")
	}
	return deployment.Commit{SHA: fields[0]}, nil
}
func (g *Git) Checkout(ctx context.Context, projectID, repo, sha, workspace string) error {
	if !shaPattern.MatchString(sha) {
		return errors.New("invalid commit SHA")
	}
	cache := filepath.Join(g.dataDir, "repositories", projectID+".git")
	if err := os.MkdirAll(filepath.Dir(cache), 0700); err != nil {
		return err
	}
	if _, err := os.Stat(cache); os.IsNotExist(err) {
		if _, err = run(ctx, "", "git", "clone", "--mirror", repo, cache); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if _, err := run(ctx, cache, "git", "fetch", "--prune", "origin"); err != nil {
		return err
	}
	if _, err := run(ctx, cache, "git", "cat-file", "-e", sha+"^{commit}"); err != nil {
		return errors.New("resolved commit is not available")
	}
	if _, err := run(ctx, "", "git", "--git-dir", cache, "--work-tree", workspace, "checkout", "--force", sha, "--", "."); err != nil {
		return err
	}
	head, err := run(ctx, cache, "git", "rev-parse", sha+"^{commit}")
	if err != nil || strings.TrimSpace(head) != sha {
		return errors.New("checkout SHA verification failed")
	}
	return nil
}
func run(ctx context.Context, dir, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s: %w", strings.TrimSpace(out.String()), err)
	}
	return out.String(), nil
}
