package deployment

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/charlesfeng/mini-cicd/apps/server/internal/pipelineconfig"
	"github.com/charlesfeng/mini-cicd/apps/server/internal/project"
)

type Commit struct{ SHA, Message, Author string }
type Resolver interface {
	Resolve(context.Context, string, string, string) (Commit, error)
}
type FixedResolver interface {
	ResolveCommit(context.Context, string, string, string, string) (Commit, error)
}
type RepositoryFileReader interface {
	ReadFile(context.Context, string, string, string, string) ([]byte, error)
}
type projectSnapshot struct {
	repo, branch, buildJSON, deployJSON, healthURL, healthExpected string
	healthEnabled                                                  bool
	healthInitial, healthTimeout, healthRetries, healthInterval    int
	stepTimeout, deploymentTimeout                                 int
	configSource, configSnapshot                                   string
	applicationJSON                                                string
	notificationJSON                                               string
	artifactJSON                                                   string
	cacheJSON                                                      string
	environment, environmentJSON, approvalStatus                   string
	commitStatusJSON                                               string
}
type Step struct {
	ID               int64   `json:"id"`
	Sequence         int     `json:"sequence"`
	Phase            string  `json:"phase"`
	Name             string  `json:"name"`
	Command          string  `json:"command"`
	WorkingDirectory string  `json:"workingDirectory"`
	Status           string  `json:"status"`
	ExitCode         *int    `json:"exitCode"`
	StartedAt        *string `json:"startedAt"`
	FinishedAt       *string `json:"finishedAt"`
}
type Service struct {
	db       *sql.DB
	resolver Resolver
}
type Deployment struct {
	ID                         int64   `json:"id"`
	ProjectID                  string  `json:"projectId"`
	Status                     string  `json:"status"`
	TriggerType                string  `json:"triggerType"`
	Branch                     string  `json:"branch"`
	CommitSHA                  string  `json:"commitSha"`
	CommitMessage              string  `json:"commitMessage"`
	CommitAuthor               string  `json:"commitAuthor"`
	ErrorSummary               string  `json:"errorSummary"`
	ConfigSource               string  `json:"configSource"`
	ConfigSnapshot             string  `json:"configSnapshot,omitempty"`
	HasArtifacts               bool    `json:"hasArtifacts"`
	ArtifactSourceDeploymentID *int64  `json:"artifactSourceDeploymentId,omitempty"`
	CacheKey                   string  `json:"cacheKey,omitempty"`
	CacheHit                   *bool   `json:"cacheHit,omitempty"`
	Environment                string  `json:"environment"`
	ApprovalStatus             string  `json:"approvalStatus"`
	ApprovedAt                 *string `json:"approvedAt,omitempty"`
	ApprovedBy                 string  `json:"approvedBy,omitempty"`
	QueuedAt                   *string `json:"queuedAt"`
	StartedAt                  *string `json:"startedAt"`
	FinishedAt                 *string `json:"finishedAt"`
	CreatedAt                  string  `json:"createdAt"`
}
type Claimed struct {
	Deployment
	StepTimeout             time.Duration
	DeploymentTimeout       time.Duration
	BuildSteps, DeploySteps []project.Step
}

func New(db *sql.DB, resolver Resolver) *Service { return &Service{db: db, resolver: resolver} }

func (s *Service) Create(ctx context.Context, projectID, trigger string) (Deployment, error) {
	return s.CreateForEnvironment(ctx, projectID, trigger, "production")
}
func (s *Service) CreateForEnvironment(ctx context.Context, projectID, trigger, environment string) (Deployment, error) {
	if trigger != "manual" && trigger != "webhook" && trigger != "redeploy" {
		return Deployment{}, errors.New("invalid trigger type")
	}
	snapshot, err := s.projectSnapshot(ctx, projectID)
	if err != nil {
		return Deployment{}, err
	}
	commit, err := s.resolver.Resolve(ctx, projectID, snapshot.repo, snapshot.branch)
	if err != nil {
		return Deployment{}, fmt.Errorf("resolve commit: %w", err)
	}
	if len(commit.SHA) != 40 {
		return Deployment{}, errors.New("resolver returned an invalid commit SHA")
	}
	return s.createResolved(ctx, projectID, trigger, environment, snapshot, commit)
}

func (s *Service) CreateAtCommit(ctx context.Context, projectID, trigger, sha string) (Deployment, error) {
	if trigger != "webhook" && trigger != "redeploy" {
		return Deployment{}, errors.New("fixed commit is only valid for webhook or redeploy")
	}
	snapshot, err := s.projectSnapshot(ctx, projectID)
	if err != nil {
		return Deployment{}, err
	}
	var commit Commit
	if fixed, ok := s.resolver.(FixedResolver); ok {
		commit, err = fixed.ResolveCommit(ctx, projectID, snapshot.repo, snapshot.branch, sha)
	} else {
		commit, err = s.resolver.Resolve(ctx, projectID, snapshot.repo, snapshot.branch)
		if err == nil && commit.SHA != sha {
			err = errors.New("requested commit does not match resolved branch")
		}
	}
	if err != nil {
		return Deployment{}, fmt.Errorf("resolve commit: %w", err)
	}
	return s.createResolved(ctx, projectID, trigger, "production", snapshot, commit)
}

// Rollback queues only the deploy phase using a successful deployment's
// immutable artifact and configuration snapshots. It deliberately skips Git
// resolution and build steps.
func (s *Service) Rollback(ctx context.Context, sourceID int64) (Deployment, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Deployment{}, err
	}
	defer tx.Rollback()
	var projectID, status, artifactPath string
	if err = tx.QueryRowContext(ctx, `SELECT project_id,status,COALESCE(artifact_path,'') FROM deployments WHERE id=?`, sourceID).Scan(&projectID, &status, &artifactPath); err != nil {
		return Deployment{}, err
	}
	if status != "succeeded" || artifactPath == "" {
		return Deployment{}, errors.New("rollback requires a successful deployment with saved artifacts")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := tx.ExecContext(ctx, `INSERT INTO deployments(project_id,status,trigger_type,branch,commit_sha,commit_message,commit_author,queued_at,created_at,health_enabled,health_url,health_initial_delay_seconds,health_timeout_seconds,health_retries,health_retry_interval_seconds,health_expected_status,step_timeout_seconds,deployment_timeout_seconds,config_source,config_snapshot,application_config_json,notification_config_json,artifact_config_json,artifact_source_deployment_id) SELECT project_id,'queued','redeploy',branch,commit_sha,commit_message,commit_author,?,?,health_enabled,health_url,health_initial_delay_seconds,health_timeout_seconds,health_retries,health_retry_interval_seconds,health_expected_status,step_timeout_seconds,deployment_timeout_seconds,config_source,config_snapshot,application_config_json,notification_config_json,artifact_config_json,? FROM deployments WHERE id=?`, now, now, sourceID, sourceID)
	if err != nil {
		return Deployment{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Deployment{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE deployments SET environment=(SELECT environment FROM deployments WHERE id=?),environment_config_json=(SELECT environment_config_json FROM deployments WHERE id=?),commit_status_config_json=(SELECT commit_status_config_json FROM deployments WHERE id=?),approval_status='approved' WHERE id=?`, sourceID, sourceID, sourceID, id); err != nil {
		return Deployment{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO commit_status_deliveries(deployment_id,deployment_status,next_attempt_at,created_at) SELECT id,'queued',?,? FROM deployments WHERE id=? AND commit_status_config_json<>'{}'`, now, now, id); err != nil {
		return Deployment{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO deployment_variables(deployment_id,name,is_secret,plain_value,cipher_value,source_version) SELECT ?,name,is_secret,plain_value,cipher_value,source_version FROM deployment_variables WHERE deployment_id=?`, id, sourceID); err != nil {
		return Deployment{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO deployment_steps(deployment_id,sequence,phase,name,command_text,working_directory,status) SELECT ?,ROW_NUMBER() OVER(ORDER BY sequence),'deploy',name,command_text,working_directory,'pending' FROM deployment_steps WHERE deployment_id=? AND phase='deploy' ORDER BY sequence`, id, sourceID); err != nil {
		return Deployment{}, err
	}
	if err = tx.Commit(); err != nil {
		return Deployment{}, err
	}
	return s.Get(ctx, id)
}

func (s *Service) projectSnapshot(ctx context.Context, projectID string) (projectSnapshot, error) {
	var p projectSnapshot
	err := s.db.QueryRowContext(ctx, `SELECT repository_url,branch,build_steps_json,deploy_steps_json,health_enabled,health_url,health_initial_delay_seconds,health_timeout_seconds,health_retries,health_retry_interval_seconds,health_expected_status,step_timeout_seconds,deployment_timeout_seconds FROM projects WHERE id=? AND archived_at IS NULL`, projectID).Scan(&p.repo, &p.branch, &p.buildJSON, &p.deployJSON, &p.healthEnabled, &p.healthURL, &p.healthInitial, &p.healthTimeout, &p.healthRetries, &p.healthInterval, &p.healthExpected, &p.stepTimeout, &p.deploymentTimeout)
	return p, err
}

func (s *Service) createResolved(ctx context.Context, projectID, trigger, environment string, snapshot projectSnapshot, commit Commit) (Deployment, error) {
	if len(commit.SHA) != 40 {
		return Deployment{}, errors.New("resolver returned an invalid commit SHA")
	}
	var build, deploy []project.Step
	if json.Unmarshal([]byte(snapshot.buildJSON), &build) != nil || json.Unmarshal([]byte(snapshot.deployJSON), &deploy) != nil {
		return Deployment{}, errors.New("invalid stored pipeline")
	}
	snapshot.configSource = "project"
	snapshot.environment, snapshot.environmentJSON, snapshot.approvalStatus = environment, "{}", "approved"
	if reader, ok := s.resolver.(RepositoryFileReader); ok {
		raw, readErr := reader.ReadFile(ctx, projectID, snapshot.repo, commit.SHA, pipelineconfig.Filename)
		if readErr == nil {
			resolved, parseErr := pipelineconfig.Parse(raw, pipelineconfig.Resolved{Build: build, Deploy: deploy, StepTimeout: time.Duration(snapshot.stepTimeout) * time.Second, DeploymentTimeout: time.Duration(snapshot.deploymentTimeout) * time.Second})
			if parseErr != nil {
				return Deployment{}, parseErr
			}
			build, deploy = resolved.Build, resolved.Deploy
			applicationRaw, _ := json.Marshal(resolved.Application)
			snapshot.applicationJSON = string(applicationRaw)
			notificationRaw, _ := json.Marshal(resolved.Notifications)
			snapshot.notificationJSON = string(notificationRaw)
			artifactRaw, _ := json.Marshal(resolved.Artifacts)
			snapshot.artifactJSON = string(artifactRaw)
			cacheRaw, _ := json.Marshal(resolved.Cache)
			snapshot.cacheJSON = string(cacheRaw)
			commitStatusRaw, _ := json.Marshal(resolved.CommitStatus)
			snapshot.commitStatusJSON = string(commitStatusRaw)
			env, exists := resolved.Environments[environment]
			if !exists && environment != "production" {
				return Deployment{}, fmt.Errorf("environment %q is not defined", environment)
			}
			if env.Frozen {
				return Deployment{}, fmt.Errorf("environment %q is frozen", environment)
			}
			if len(env.AllowedBranches) > 0 && !stringIn(env.AllowedBranches, snapshot.branch) {
				return Deployment{}, fmt.Errorf("branch %q is not allowed for environment %q", snapshot.branch, environment)
			}
			if env.Window != nil && !insideWindow(time.Now().UTC(), *env.Window) {
				return Deployment{}, fmt.Errorf("environment %q is outside its deployment window", environment)
			}
			envRaw, _ := json.Marshal(env)
			snapshot.environmentJSON = string(envRaw)
			if env.ApprovalRequired {
				snapshot.approvalStatus = "pending"
			}
			snapshot.stepTimeout, snapshot.deploymentTimeout = int(resolved.StepTimeout/time.Second), int(resolved.DeploymentTimeout/time.Second)
			snapshot.configSource, snapshot.configSnapshot = "repository", string(raw)
		} else if !errors.Is(readErr, os.ErrNotExist) {
			return Deployment{}, fmt.Errorf("read %s: %w", pipelineconfig.Filename, readErr)
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Deployment{}, err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if snapshot.applicationJSON == "" {
		snapshot.applicationJSON = "{}"
	}
	if snapshot.notificationJSON == "" {
		snapshot.notificationJSON = "[]"
	}
	if snapshot.artifactJSON == "" {
		snapshot.artifactJSON = "{}"
	}
	if snapshot.cacheJSON == "" {
		snapshot.cacheJSON = "{}"
	}
	if snapshot.commitStatusJSON == "" {
		snapshot.commitStatusJSON = "{}"
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO deployments(project_id,status,trigger_type,branch,commit_sha,commit_message,commit_author,queued_at,created_at,health_enabled,health_url,health_initial_delay_seconds,health_timeout_seconds,health_retries,health_retry_interval_seconds,health_expected_status,step_timeout_seconds,deployment_timeout_seconds,config_source,config_snapshot,application_config_json,notification_config_json,artifact_config_json,cache_config_json) VALUES(?,'queued',?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, projectID, trigger, snapshot.branch, commit.SHA, commit.Message, commit.Author, now, now, snapshot.healthEnabled, snapshot.healthURL, snapshot.healthInitial, snapshot.healthTimeout, snapshot.healthRetries, snapshot.healthInterval, snapshot.healthExpected, snapshot.stepTimeout, snapshot.deploymentTimeout, snapshot.configSource, snapshot.configSnapshot, snapshot.applicationJSON, snapshot.notificationJSON, snapshot.artifactJSON, snapshot.cacheJSON)
	if err != nil {
		return Deployment{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Deployment{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE deployments SET environment=?,environment_config_json=?,approval_status=? WHERE id=?`, snapshot.environment, snapshot.environmentJSON, snapshot.approvalStatus, id); err != nil {
		return Deployment{}, err
	}
	if snapshot.commitStatusJSON != "{}" {
		if _, err = tx.ExecContext(ctx, `UPDATE deployments SET commit_status_config_json=? WHERE id=?`, snapshot.commitStatusJSON, id); err != nil {
			return Deployment{}, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO commit_status_deliveries(deployment_id,deployment_status,next_attempt_at,created_at) VALUES(?,?,?,?)`, id, "queued", now, now); err != nil {
			return Deployment{}, err
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO deployment_variables(deployment_id,name,is_secret,plain_value,cipher_value,source_version) SELECT ?,name,is_secret,plain_value,cipher_value,version FROM project_variables WHERE project_id=? AND environment=? AND replaced_at IS NULL`, id, projectID, snapshot.environment); err != nil {
		return Deployment{}, err
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

func (s *Service) Steps(ctx context.Context, id int64) ([]Step, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,sequence,phase,name,command_text,working_directory,status,exit_code,started_at,finished_at FROM deployment_steps WHERE deployment_id=? ORDER BY sequence`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Step{}
	for rows.Next() {
		var x Step
		if err = rows.Scan(&x.ID, &x.Sequence, &x.Phase, &x.Name, &x.Command, &x.WorkingDirectory, &x.Status, &x.ExitCode, &x.StartedAt, &x.FinishedAt); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func stringIn(items []string, value string) bool {
	for _, item := range items {
		if item == value {
			return true
		}
	}
	return false
}
func insideWindow(now time.Time, w pipelineconfig.DeploymentWindow) bool {
	parsed, err := time.Parse("-07:00", w.Timezone)
	if err != nil {
		return false
	}
	_, offset := parsed.Zone()
	local := now.In(time.FixedZone("deployment", offset))
	days := []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}
	if !stringIn(w.Days, days[int(local.Weekday())]) {
		return false
	}
	clock := local.Format("15:04")
	if w.Start < w.End {
		return clock >= w.Start && clock < w.End
	}
	return clock >= w.Start || clock < w.End
}

func (s *Service) Get(ctx context.Context, id int64) (Deployment, error) {
	var d Deployment
	err := s.db.QueryRowContext(ctx, selectDeployment+` WHERE id=?`, id).Scan(&d.ID, &d.ProjectID, &d.Status, &d.TriggerType, &d.Branch, &d.CommitSHA, &d.CommitMessage, &d.CommitAuthor, &d.ErrorSummary, &d.ConfigSource, &d.ConfigSnapshot, &d.HasArtifacts, &d.ArtifactSourceDeploymentID, &d.Environment, &d.ApprovalStatus, &d.ApprovedAt, &d.ApprovedBy, &d.CacheKey, &d.CacheHit, &d.QueuedAt, &d.StartedAt, &d.FinishedAt, &d.CreatedAt)
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
		if err = rows.Scan(&d.ID, &d.ProjectID, &d.Status, &d.TriggerType, &d.Branch, &d.CommitSHA, &d.CommitMessage, &d.CommitAuthor, &d.ErrorSummary, &d.ConfigSource, &d.ConfigSnapshot, &d.HasArtifacts, &d.ArtifactSourceDeploymentID, &d.Environment, &d.ApprovalStatus, &d.ApprovedAt, &d.ApprovedBy, &d.CacheKey, &d.CacheHit, &d.QueuedAt, &d.StartedAt, &d.FinishedAt, &d.CreatedAt); err != nil {
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
	err = tx.QueryRowContext(ctx, `SELECT d.id,d.project_id FROM deployments d WHERE d.status='queued' AND d.approval_status='approved' AND NOT EXISTS(SELECT 1 FROM project_locks l WHERE l.project_id=d.project_id) ORDER BY d.queued_at,d.id LIMIT 1`).Scan(&id, &projectID)
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

func (s *Service) Approve(ctx context.Context, id int64, approvedBy string) (Deployment, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := s.db.ExecContext(ctx, `UPDATE deployments SET approval_status='approved',approved_at=?,approved_by=? WHERE id=? AND status='queued' AND approval_status='pending'`, now, approvedBy, id)
	if err != nil {
		return Deployment{}, err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return Deployment{}, errors.New("deployment is not awaiting approval")
	}
	return s.Get(ctx, id)
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

const selectDeployment = `SELECT id,project_id,status,trigger_type,branch,COALESCE(commit_sha,''),commit_message,commit_author,error_summary,config_source,config_snapshot,artifact_path IS NOT NULL,artifact_source_deployment_id,environment,approval_status,approved_at,COALESCE(approved_by,''),cache_key,cache_hit,queued_at,started_at,finished_at,created_at FROM deployments`
