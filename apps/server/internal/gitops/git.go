package gitops

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charlesfeng/mini-cicd/apps/server/internal/deployment"
	"github.com/charlesfeng/mini-cicd/apps/server/internal/procenv"
	"github.com/charlesfeng/mini-cicd/apps/server/internal/secret"
)

var shaPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

type Git struct {
	dataDir string
	db      *sql.DB
	box     *secret.Box
	mu      sync.Mutex
	locks   map[string]*sync.Mutex
}

type credentials struct {
	authType, username, password, privateKey, knownHosts string
}

func New(dataDir string, db *sql.DB, box *secret.Box) *Git {
	return &Git{dataDir: dataDir, db: db, box: box, locks: map[string]*sync.Mutex{}}
}

func (g *Git) projectLock(id string) func() {
	g.mu.Lock()
	lock := g.locks[id]
	if lock == nil {
		lock = &sync.Mutex{}
		g.locks[id] = lock
	}
	g.mu.Unlock()
	lock.Lock()
	return lock.Unlock
}

func (g *Git) Resolve(ctx context.Context, projectID, repo, branch string) (deployment.Commit, error) {
	unlock := g.projectLock(projectID)
	defer unlock()
	ref := "refs/heads/" + branch
	env, cleanup, err := g.gitEnvironment(projectID)
	if err != nil {
		return deployment.Commit{}, err
	}
	defer cleanup()
	out, err := run(ctx, "", env, "git", "ls-remote", "--exit-code", repo, ref)
	if err != nil {
		return deployment.Commit{}, err
	}
	fields := strings.Fields(out)
	if len(fields) < 2 || fields[1] != ref || !shaPattern.MatchString(fields[0]) {
		return deployment.Commit{}, errors.New("branch did not resolve to a full commit SHA")
	}
	sha := fields[0]
	cache := g.cachePath(projectID)
	if err := g.updateCache(ctx, cache, repo, env); err != nil {
		return deployment.Commit{}, err
	}
	meta, err := run(ctx, cache, env, "git", "show", "-s", "--format=%s%n%an <%ae>", sha)
	if err != nil {
		return deployment.Commit{}, fmt.Errorf("read commit metadata: %w", err)
	}
	lines := strings.SplitN(strings.TrimSpace(meta), "\n", 2)
	commit := deployment.Commit{SHA: sha, Message: lines[0]}
	if len(lines) == 2 {
		commit.Author = lines[1]
	}
	return commit, nil
}

func (g *Git) ResolveCommit(ctx context.Context, projectID, repo, branch, sha string) (deployment.Commit, error) {
	if !shaPattern.MatchString(sha) {
		return deployment.Commit{}, errors.New("requested commit SHA is invalid")
	}
	unlock := g.projectLock(projectID)
	defer unlock()
	env, cleanup, err := g.gitEnvironment(projectID)
	if err != nil {
		return deployment.Commit{}, err
	}
	defer cleanup()
	cache := g.cachePath(projectID)
	if err = g.updateCache(ctx, cache, repo, env); err != nil {
		return deployment.Commit{}, err
	}
	if _, err = run(ctx, cache, env, "git", "cat-file", "-e", sha+"^{commit}"); err != nil {
		return deployment.Commit{}, errors.New("requested commit is not available")
	}
	if _, err = run(ctx, cache, env, "git", "merge-base", "--is-ancestor", sha, "refs/heads/"+branch); err != nil {
		return deployment.Commit{}, errors.New("requested commit is not reachable from the configured branch")
	}
	meta, err := run(ctx, cache, env, "git", "show", "-s", "--format=%s%n%an <%ae>", sha)
	if err != nil {
		return deployment.Commit{}, err
	}
	lines := strings.SplitN(strings.TrimSpace(meta), "\n", 2)
	result := deployment.Commit{SHA: sha, Message: lines[0]}
	if len(lines) == 2 {
		result.Author = lines[1]
	}
	return result, nil
}

func (g *Git) Checkout(ctx context.Context, projectID, repo, sha, workspace string) error {
	unlock := g.projectLock(projectID)
	defer unlock()
	if !shaPattern.MatchString(sha) {
		return errors.New("invalid commit SHA")
	}
	env, cleanup, err := g.gitEnvironment(projectID)
	if err != nil {
		return err
	}
	defer cleanup()
	cache := g.cachePath(projectID)
	if err := g.updateCache(ctx, cache, repo, env); err != nil {
		return err
	}
	if _, err := run(ctx, cache, env, "git", "cat-file", "-e", sha+"^{commit}"); err != nil {
		return errors.New("resolved commit is not available")
	}
	if _, err := run(ctx, "", env, "git", "--git-dir", cache, "--work-tree", workspace, "checkout", "--force", sha, "--", "."); err != nil {
		return err
	}
	head, err := run(ctx, cache, env, "git", "rev-parse", sha+"^{commit}")
	if err != nil || strings.TrimSpace(head) != sha {
		return errors.New("checkout SHA verification failed")
	}
	return nil
}

func (g *Git) updateCache(ctx context.Context, cache, repo string, env []string) error {
	if err := os.MkdirAll(filepath.Dir(cache), 0o700); err != nil {
		return err
	}
	if _, err := os.Stat(cache); os.IsNotExist(err) {
		_, err = run(ctx, "", env, "git", "clone", "--mirror", repo, cache)
		return err
	} else if err != nil {
		return err
	}
	_, err := run(ctx, cache, env, "git", "fetch", "--prune", "origin")
	return err
}

func (g *Git) cachePath(projectID string) string {
	return filepath.Join(g.dataDir, "repositories", projectID+".git")
}

func (g *Git) loadCredentials(projectID string) (credentials, error) {
	var c credentials
	var passwordCipher, keyCipher []byte
	err := g.db.QueryRow(`SELECT auth_type,git_username,git_secret_cipher,ssh_private_key_cipher,ssh_known_hosts FROM projects WHERE id=? AND archived_at IS NULL`, projectID).
		Scan(&c.authType, &c.username, &passwordCipher, &keyCipher, &c.knownHosts)
	if err != nil {
		return c, err
	}
	if len(passwordCipher) > 0 {
		plain, err := g.box.Decrypt(passwordCipher, "project:"+projectID+":git-secret")
		if err != nil {
			return c, err
		}
		c.password = string(plain)
	}
	if len(keyCipher) > 0 {
		plain, err := g.box.Decrypt(keyCipher, "project:"+projectID+":ssh-key")
		if err != nil {
			return c, err
		}
		c.privateKey = string(plain)
	}
	return c, nil
}

func (g *Git) gitEnvironment(projectID string) ([]string, func(), error) {
	c, err := g.loadCredentials(projectID)
	if err != nil {
		return nil, func() {}, err
	}
	env := append(procenv.Safe(), "GIT_TERMINAL_PROMPT=0")
	if c.authType == "none" {
		return env, func() {}, nil
	}
	tempDir, err := os.MkdirTemp(filepath.Join(g.dataDir), ".git-auth-")
	if err != nil {
		return nil, func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(tempDir) }
	if c.authType == "https" {
		if c.password == "" {
			cleanup()
			return nil, func() {}, errors.New("HTTPS credential is not configured")
		}
		script := filepath.Join(tempDir, "askpass")
		content := "#!/bin/sh\ncase \"$1\" in *sername*) printf '%s\\n' \"$MINICICD_GIT_USERNAME\";; *) printf '%s\\n' \"$MINICICD_GIT_PASSWORD\";; esac\n"
		if runtime.GOOS == "windows" {
			script += ".cmd"
			content = "@echo off\r\necho %~1| findstr /I Username >nul\r\nif errorlevel 1 (\r\n  <nul set /p \"=%MINICICD_GIT_PASSWORD%\"\r\n) else (\r\n  <nul set /p \"=%MINICICD_GIT_USERNAME%\"\r\n)\r\n"
		}
		if err := os.WriteFile(script, []byte(content), 0o700); err != nil {
			cleanup()
			return nil, func() {}, err
		}
		env = append(env, "GIT_ASKPASS="+script, "MINICICD_GIT_USERNAME="+c.username, "MINICICD_GIT_PASSWORD="+c.password)
		return env, cleanup, nil
	}
	if c.authType == "ssh" {
		if c.privateKey == "" || strings.TrimSpace(c.knownHosts) == "" {
			cleanup()
			return nil, func() {}, errors.New("SSH private key and known_hosts are both required")
		}
		keyPath := filepath.Join(tempDir, "id_deploy")
		hostsPath := filepath.Join(tempDir, "known_hosts")
		if err := os.WriteFile(keyPath, []byte(c.privateKey), 0o600); err != nil {
			cleanup()
			return nil, func() {}, err
		}
		if err := os.WriteFile(hostsPath, []byte(c.knownHosts), 0o600); err != nil {
			cleanup()
			return nil, func() {}, err
		}
		globalHosts := "/dev/null"
		if runtime.GOOS == "windows" {
			globalHosts = "NUL"
		}
		sshCommand := "ssh -i " + strconv.Quote(keyPath) + " -o IdentitiesOnly=yes -o StrictHostKeyChecking=yes -o UserKnownHostsFile=" + strconv.Quote(hostsPath) + " -o GlobalKnownHostsFile=" + globalHosts
		env = append(env, "GIT_SSH_COMMAND="+sshCommand)
		return env, cleanup, nil
	}
	cleanup()
	return nil, func() {}, errors.New("unsupported Git authentication type")
}

func run(ctx context.Context, dir string, env []string, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir, cmd.Env = dir, env
	configureGitProcess(cmd)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		return "", err
	}
	wait := make(chan error, 1)
	go func() { wait <- cmd.Wait() }()
	var err error
	select {
	case err = <-wait:
	case <-ctx.Done():
		stopGitProcess(cmd.Process)
		select {
		case <-wait:
		case <-time.After(2 * time.Second):
			killGitProcess(cmd.Process)
			select {
			case <-wait:
			case <-time.After(2 * time.Second):
				return "", errors.New("Git process tree did not exit after forced termination")
			}
		}
		err = ctx.Err()
	}
	if err != nil {
		return "", fmt.Errorf("git command failed: %s: %w", strings.TrimSpace(out.String()), err)
	}
	return out.String(), nil
}
