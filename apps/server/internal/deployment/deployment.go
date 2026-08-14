package deployment

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/charlesfeng/mini-cicd/apps/server/internal/project"
)

type Commit struct{ SHA, Message, Author string }
type Resolver interface {
	Resolve(context.Context, string, string, string) (Commit, error)
}
type Service struct {
	db       *sql.DB
	resolver Resolver
}
type Deployment struct {
	ID            int64   `json:"id"`
	ProjectID     string  `json:"projectId"`
	Status        string  `json:"status"`
	TriggerType   string  `json:"triggerType"`
	Branch        string  `json:"branch"`
	CommitSHA     string  `json:"commitSha"`
	CommitMessage string  `json:"commitMessage"`
	CommitAuthor  string  `json:"commitAuthor"`
	ErrorSummary  string  `json:"errorSummary"`
	QueuedAt      *string `json:"queuedAt"`
	StartedAt     *string `json:"startedAt"`
	FinishedAt    *string `json:"finishedAt"`
	CreatedAt     string  `json:"createdAt"`
}
type Claimed struct {
	Deployment
	StepTimeout             time.Duration
	DeploymentTimeout       time.Duration
	BuildSteps, DeploySteps []project.Step
}

func New(db *sql.DB, resolver Resolver) *Service { return &Service{db: db, resolver: resolver} }

func (s *Service) Create(ctx context.Context, projectID, trigger string) (Deployment, error) {
	if trigger != "manual" && trigger != "webhook" && trigger != "redeploy" {
		return Deployment{}, errors.New("invalid trigger type")
	}
	var repo, branch string
	if err := s.db.QueryRowContext(ctx, `SELECT repository_url,branch FROM projects WHERE id=? AND archived_at IS NULL`, projectID).Scan(&repo, &branch); err != nil {
		return Deployment{}, err
	}
	commit, err := s.resolver.Resolve(ctx, projectID, repo, branch)
	if err != nil {
		return Deployment{}, fmt.Errorf("resolve commit: %w", err)
	}
	if len(commit.SHA) != 40 {
		return Deployment{}, errors.New("resolver returned an invalid commit SHA")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Deployment{}, err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := tx.ExecContext(ctx, `INSERT INTO deployments(project_id,status,trigger_type,branch,commit_sha,commit_message,commit_author,queued_at,created_at) VALUES(?,'queued',?,?,?,?,?,?,?)`, projectID, trigger, branch, commit.SHA, commit.Message, commit.Author, now, now)
	if err != nil {
		return Deployment{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Deployment{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO deployment_variables(deployment_id,name,is_secret,plain_value,cipher_value,source_version) SELECT ?,name,is_secret,plain_value,cipher_value,version FROM project_variables WHERE project_id=? AND replaced_at IS NULL`, id, projectID); err != nil {
		return Deployment{}, err
	}
	var buildJSON, deployJSON string
	if err = tx.QueryRowContext(ctx, `SELECT build_steps_json,deploy_steps_json FROM projects WHERE id=?`, projectID).Scan(&buildJSON, &deployJSON); err != nil {
		return Deployment{}, err
	}
	var build, deploy []project.Step
	if json.Unmarshal([]byte(buildJSON), &build) != nil || json.Unmarshal([]byte(deployJSON), &deploy) != nil {
		return Deployment{}, errors.New("invalid stored pipeline")
	}
	seq := 0
	for _, phase := range []struct {
		name  string
		steps []project.Step
	}{{"build", build}, {"deploy", deploy}} {
		for _, step := range phase.steps {
			seq++
			if _, err = tx.ExecContext(ctx, `INSERT INTO deployment_steps(deployment_id,sequence,phase,name,command_text,working_directory,status) VALUES(?,?,?,?,?,?,'pending')`, id, seq, phase.name, step.Name, step.Command, step.WorkingDirectory); err != nil {
				return Deployment{}, err
			}
		}
	}
	if err = tx.Commit(); err != nil {
		return Deployment{}, err
	}
	return s.Get(ctx, id)
}

func (s *Service) Get(ctx context.Context, id int64) (Deployment, error) {
	var d Deployment
	err := s.db.QueryRowContext(ctx, selectDeployment+` WHERE id=?`, id).Scan(&d.ID, &d.ProjectID, &d.Status, &d.TriggerType, &d.Branch, &d.CommitSHA, &d.CommitMessage, &d.CommitAuthor, &d.ErrorSummary, &d.QueuedAt, &d.StartedAt, &d.FinishedAt, &d.CreatedAt)
	return d, err
}
func (s *Service) List(ctx context.Context, projectID string) ([]Deployment, error) {
	rows, err := s.db.QueryContext(ctx, selectDeployment+` WHERE project_id=? ORDER BY id DESC`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Deployment{}
	for rows.Next() {
		var d Deployment
		if err = rows.Scan(&d.ID, &d.ProjectID, &d.Status, &d.TriggerType, &d.Branch, &d.CommitSHA, &d.CommitMessage, &d.CommitAuthor, &d.ErrorSummary, &d.QueuedAt, &d.StartedAt, &d.FinishedAt, &d.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Service) Claim(ctx context.Context, runnerID string) (Deployment, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Deployment{}, false, err
	}
	defer tx.Rollback()
	var id int64
	var projectID string
	err = tx.QueryRowContext(ctx, `SELECT d.id,d.project_id FROM deployments d WHERE d.status='queued' AND NOT EXISTS(SELECT 1 FROM project_locks l WHERE l.project_id=d.project_id) ORDER BY d.queued_at,d.id LIMIT 1`).Scan(&id, &projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return Deployment{}, false, nil
	}
	if err != nil {
		return Deployment{}, false, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err = tx.ExecContext(ctx, `INSERT INTO project_locks(project_id,deployment_id,acquired_at) VALUES(?,?,?)`, projectID, id, now); err != nil {
		return Deployment{}, false, nil
	}
	res, err := tx.ExecContext(ctx, `UPDATE deployments SET status='preparing',runner_id=?,started_at=? WHERE id=? AND status='queued'`, runnerID, now, id)
	if err != nil {
		return Deployment{}, false, err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return Deployment{}, false, nil
	}
	if err = tx.Commit(); err != nil {
		return Deployment{}, false, err
	}
	d, err := s.Get(ctx, id)
	return d, true, err
}

func (s *Service) Cancel(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `UPDATE deployments SET status=CASE WHEN status='queued' THEN 'cancelled' ELSE 'cancelling' END,cancel_requested_at=?,finished_at=CASE WHEN status='queued' THEN ? ELSE finished_at END WHERE id=? AND status IN ('queued','preparing','running')`, time.Now().UTC().Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		var status string
		if err = s.db.QueryRowContext(ctx, `SELECT status FROM deployments WHERE id=?`, id).Scan(&status); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) Finish(ctx context.Context, id int64, status, message string) error {
	if status != "succeeded" && status != "failed" && status != "cancelled" && status != "timed_out" {
		return errors.New("invalid terminal status")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var current string
	if err = tx.QueryRowContext(ctx, `SELECT status FROM deployments WHERE id=?`, id).Scan(&current); err != nil {
		return err
	}
	if current == "succeeded" || current == "failed" || current == "cancelled" || current == "timed_out" {
		return nil
	}
	if current == "cancelling" && status == "succeeded" {
		status = "cancelled"
		message = ""
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err = tx.ExecContext(ctx, `UPDATE deployments SET status=?,error_summary=?,finished_at=? WHERE id=? AND status NOT IN ('succeeded','failed','cancelled','timed_out')`, status, message, now, id); err != nil {
		return err
	}
	if status != "succeeded" {
		if _, err = tx.ExecContext(ctx, `UPDATE deployment_steps SET status='skipped',finished_at=? WHERE deployment_id=? AND status='pending'`, now, id); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM project_locks WHERE deployment_id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

const selectDeployment = `SELECT id,project_id,status,trigger_type,branch,COALESCE(commit_sha,''),commit_message,commit_author,error_summary,queued_at,started_at,finished_at,created_at FROM deployments`
