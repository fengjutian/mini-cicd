package runner

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"time"

	"github.com/charlesfeng/mini-cicd/apps/server/internal/deployment"
	"github.com/charlesfeng/mini-cicd/apps/server/internal/gitops"
	"github.com/charlesfeng/mini-cicd/apps/server/internal/logstore"
	"github.com/charlesfeng/mini-cicd/apps/server/internal/secret"
	"github.com/charlesfeng/mini-cicd/apps/server/internal/workspace"
)

type Manager struct {
	db       *sql.DB
	deps     *deployment.Service
	git      *gitops.Git
	spaces   *workspace.Manager
	logs     *logstore.Store
	box      *secret.Box
	shell    string
	parallel int
	grace    time.Duration
	logger   *slog.Logger
	wake     chan struct{}
	wg       sync.WaitGroup
	ctx      context.Context
	cancel   context.CancelFunc
}
type step struct {
	id           int64
	command, dir string
	timeout      time.Duration
}

func New(db *sql.DB, deps *deployment.Service, git *gitops.Git, spaces *workspace.Manager, logs *logstore.Store, box *secret.Box, shell string, parallel int, grace time.Duration, logger *slog.Logger) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{db: db, deps: deps, git: git, spaces: spaces, logs: logs, box: box, shell: shell, parallel: parallel, grace: grace, logger: logger, wake: make(chan struct{}, 1), ctx: ctx, cancel: cancel}
}
func (m *Manager) Start() {
	_ = m.recover()
	for i := 0; i < m.parallel; i++ {
		m.wg.Add(1)
		go m.worker(fmt.Sprintf("local-%d", i+1))
	}
}
func (m *Manager) Stop() { m.cancel(); m.signal(); m.wg.Wait() }
func (m *Manager) Wake() { m.signal() }
func (m *Manager) signal() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}
func (m *Manager) recover() error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := m.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE deployments SET status='failed',error_summary='service restarted during deployment',finished_at=? WHERE status IN ('preparing','running','cancelling')`, now); err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM project_locks WHERE deployment_id IN(SELECT id FROM deployments WHERE status IN ('succeeded','failed','cancelled','timed_out'))`); err != nil {
		return err
	}
	return tx.Commit()
}
func (m *Manager) worker(id string) {
	defer m.wg.Done()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-m.wake:
		case <-ticker.C:
		}
		for {
			d, ok, err := m.deps.Claim(m.ctx, id)
			if err != nil {
				m.logger.Error("claim deployment", "error", err)
				break
			}
			if !ok {
				break
			}
			m.execute(d)
		}
	}
}
func (m *Manager) execute(d deployment.Deployment) {
	status, message := "succeeded", ""
	if err := m.run(d); err != nil {
		status = "failed"
		message = err.Error()
		if errors.Is(err, context.Canceled) {
			status = "cancelled"
		}
		if errors.Is(err, context.DeadlineExceeded) {
			status = "timed_out"
		}
	}
	if err := m.deps.Finish(context.Background(), d.ID, status, message); err != nil {
		m.logger.Error("finish deployment", "id", d.ID, "error", err)
	}
	m.signal()
}
func (m *Manager) run(d deployment.Deployment) error {
	var repo string
	var stepSeconds, deploySeconds int
	if err := m.db.QueryRow(`SELECT repository_url,step_timeout_seconds,deployment_timeout_seconds FROM projects WHERE id=?`, d.ProjectID).Scan(&repo, &stepSeconds, &deploySeconds); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(m.ctx, time.Duration(deploySeconds)*time.Second)
	defer cancel()
	watchDone := make(chan struct{})
	defer close(watchDone)
	go func() {
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-watchDone:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				if yes, _ := m.cancelled(d.ID); yes {
					cancel()
					return
				}
			}
		}
	}()
	space, err := m.spaces.Create(d.ProjectID, d.ID)
	if err != nil {
		return err
	}
	secrets, env, err := m.environment(d)
	if err != nil {
		return err
	}
	env = append(env, "MINICICD_WORKSPACE="+space)
	writer, err := m.logs.Open(d.ProjectID, d.ID, secrets)
	if err != nil {
		return err
	}
	defer writer.Close()
	logPath := m.logs.Path(d.ProjectID, d.ID)
	if _, err = m.db.Exec(`UPDATE deployments SET workspace_path=?,log_path=? WHERE id=?`, space, logPath, d.ID); err != nil {
		return err
	}
	if err = writer.WriteStep(0, "system", "checking out commit "+d.CommitSHA); err != nil {
		return err
	}
	if err = m.git.Checkout(ctx, d.ProjectID, repo, d.CommitSHA, space); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	res, err := m.db.Exec(`UPDATE deployments SET status='running' WHERE id=? AND status='preparing'`, d.ID)
	if err != nil {
		return err
	}
	if changed, _ := res.RowsAffected(); changed != 1 {
		return context.Canceled
	}
	rows, err := m.db.Query(`SELECT id,command_text,working_directory FROM deployment_steps WHERE deployment_id=? ORDER BY sequence`, d.ID)
	if err != nil {
		return err
	}
	var steps []step
	for rows.Next() {
		var x step
		if err = rows.Scan(&x.id, &x.command, &x.dir); err != nil {
			rows.Close()
			return err
		}
		x.timeout = time.Duration(stepSeconds) * time.Second
		steps = append(steps, x)
	}
	rows.Close()
	for _, x := range steps {
		if cancelled, _ := m.cancelled(d.ID); cancelled {
			return context.Canceled
		}
		dir, err := workspace.Resolve(space, x.dir)
		if err != nil {
			return err
		}
		if err = m.runStep(ctx, d.ID, x, dir, env, writer); err != nil {
			return err
		}
	}
	return nil
}
func (m *Manager) environment(d deployment.Deployment) ([]string, []string, error) {
	rows, err := m.db.Query(`SELECT name,is_secret,COALESCE(plain_value,''),cipher_value FROM deployment_variables WHERE deployment_id=?`, d.ID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	env := []string{"MINICICD_PROJECT_ID=" + d.ProjectID, "MINICICD_DEPLOYMENT_ID=" + fmt.Sprint(d.ID), "MINICICD_COMMIT_SHA=" + d.CommitSHA, "MINICICD_BRANCH=" + d.Branch}
	values := []string{}
	for rows.Next() {
		var name, plain string
		var secretFlag bool
		var cipher []byte
		if err = rows.Scan(&name, &secretFlag, &plain, &cipher); err != nil {
			return nil, nil, err
		}
		value := plain
		if secretFlag {
			decoded, e := m.box.Decrypt(cipher, "project:"+d.ProjectID+":variable:"+name)
			if e != nil {
				return nil, nil, e
			}
			value = string(decoded)
			values = append(values, value)
		}
		env = append(env, name+"="+value)
	}
	return values, append(os.Environ(), env...), rows.Err()
}
func (m *Manager) runStep(parent context.Context, deploymentID int64, s step, dir string, env []string, w *logstore.Writer) error {
	if err := parent.Err(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(parent, s.timeout)
	defer cancel()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, _ = m.db.Exec(`UPDATE deployment_steps SET status='running',started_at=? WHERE id=?`, now, s.id)
	if err := w.WriteStep(s.id, "system", "$ "+s.command); err != nil {
		return err
	}
	args := []string{"-lc", s.command}
	if runtime.GOOS == "windows" {
		args = []string{"-NoProfile", "-NonInteractive", "-Command", s.command}
	}
	cmd := exec.Command(m.shell, args...)
	cmd.Dir, cmd.Env = dir, env
	configureProcess(cmd)
	pipeR, pipeW := io.Pipe()
	cmd.Stdout, cmd.Stderr = pipeW, pipeW
	copyDone := make(chan error, 1)
	go func() { copyDone <- scanCopy(w, s.id, pipeR) }()
	if err := cmd.Start(); err != nil {
		pipeW.Close()
		<-copyDone
		return err
	}
	wait := make(chan error, 1)
	go func() { wait <- cmd.Wait(); pipeW.Close() }()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	var err error
	copyPending := true
	for {
		select {
		case err = <-wait:
			goto done
		case logErr := <-copyDone:
			copyPending = false
			if logErr != nil {
				if stopErr := m.stopAndWait(cmd, wait); stopErr != nil {
					err = stopErr
				} else {
					err = fmt.Errorf("persist deployment log: %w", logErr)
				}
				goto done
			}
			err = <-wait
			goto done
		case <-ctx.Done():
			stopErr := m.stopAndWait(cmd, wait)
			if stopErr != nil {
				_ = pipeR.CloseWithError(stopErr)
				err = stopErr
				goto done
			}
			if cancelled, _ := m.cancelled(deploymentID); cancelled {
				err = context.Canceled
			} else {
				err = ctx.Err()
			}
			goto done
		case <-ticker.C:
			if yes, _ := m.cancelled(deploymentID); yes {
				if stopErr := m.stopAndWait(cmd, wait); stopErr != nil {
					_ = pipeR.CloseWithError(stopErr)
					err = stopErr
				} else {
					err = context.Canceled
				}
				goto done
			}
		}
	}
done:
	if copyPending {
		<-copyDone
	}
	finished := time.Now().UTC().Format(time.RFC3339Nano)
	status := "succeeded"
	var exit any = 0
	if err != nil {
		status = "failed"
		exit = nil
		if errors.Is(err, context.Canceled) {
			status = "cancelled"
		}
		if errors.Is(err, context.DeadlineExceeded) {
			status = "timed_out"
		}
		if e, ok := err.(*exec.ExitError); ok {
			exit = e.ExitCode()
		}
	}
	_, _ = m.db.Exec(`UPDATE deployment_steps SET status=?,exit_code=?,finished_at=? WHERE id=?`, status, exit, finished, s.id)
	return err
}
func (m *Manager) stopAndWait(cmd *exec.Cmd, wait <-chan error) error {
	_ = gentleStop(cmd.Process)
	select {
	case <-wait:
		return nil
	case <-time.After(m.grace):
	}
	_ = forceStop(cmd.Process)
	select {
	case <-wait:
		return nil
	case <-time.After(m.grace):
		return errors.New("process tree did not exit after forced termination")
	}
}
func (m *Manager) cancelled(id int64) (bool, error) {
	var status string
	err := m.db.QueryRow(`SELECT status FROM deployments WHERE id=?`, id).Scan(&status)
	return status == "cancelling" || status == "cancelled", err
}
func scanCopy(w *logstore.Writer, stepID int64, r io.Reader) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		if err := w.WriteStep(stepID, "output", scanner.Text()); err != nil {
			return err
		}
	}
	return scanner.Err()
}
