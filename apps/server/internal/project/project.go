package project

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/charlesfeng/mini-cicd/apps/server/internal/auth"
	"github.com/charlesfeng/mini-cicd/apps/server/internal/secret"
)

var (
	slugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	namePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

type Step struct {
	Name             string `json:"name"`
	Command          string `json:"command"`
	WorkingDirectory string `json:"workingDirectory,omitempty"`
}

type Project struct {
	ID                       string `json:"id"`
	Name                     string `json:"name"`
	Slug                     string `json:"slug"`
	Description              string `json:"description"`
	RepositoryURL            string `json:"repositoryUrl"`
	Branch                   string `json:"branch"`
	AuthType                 string `json:"authType"`
	GitUsername              string `json:"gitUsername,omitempty"`
	HasGitSecret             bool   `json:"hasGitSecret"`
	HasSSHPrivateKey         bool   `json:"hasSshPrivateKey"`
	SSHKnownHosts            string `json:"sshKnownHosts,omitempty"`
	BuildSteps               []Step `json:"buildSteps"`
	DeploySteps              []Step `json:"deploySteps"`
	StepTimeoutSeconds       int    `json:"stepTimeoutSeconds"`
	DeploymentTimeoutSeconds int    `json:"deploymentTimeoutSeconds"`
	CreatedAt                string `json:"createdAt"`
	UpdatedAt                string `json:"updatedAt"`
}

type Input struct {
	Name                     string `json:"name"`
	Slug                     string `json:"slug"`
	Description              string `json:"description"`
	RepositoryURL            string `json:"repositoryUrl"`
	Branch                   string `json:"branch"`
	AuthType                 string `json:"authType"`
	GitUsername              string `json:"gitUsername"`
	GitSecret                string `json:"gitSecret"`
	SSHPrivateKey            string `json:"sshPrivateKey"`
	SSHKnownHosts            string `json:"sshKnownHosts"`
	BuildSteps               []Step `json:"buildSteps"`
	DeploySteps              []Step `json:"deploySteps"`
	StepTimeoutSeconds       int    `json:"stepTimeoutSeconds"`
	DeploymentTimeoutSeconds int    `json:"deploymentTimeoutSeconds"`
}

type VariableInput struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	IsSecret bool   `json:"isSecret"`
}

type Variable struct {
	Name      string `json:"name"`
	Value     string `json:"value,omitempty"`
	IsSecret  bool   `json:"isSecret"`
	Version   int    `json:"version"`
	UpdatedAt string `json:"updatedAt"`
}

type Service struct {
	db  *sql.DB
	box *secret.Box
}

func New(db *sql.DB, box *secret.Box) *Service { return &Service{db: db, box: box} }

func (s *Service) Create(ctx context.Context, in Input) (Project, error) {
	if err := validate(&in); err != nil {
		return Project{}, err
	}
	id, _, err := auth.NewSessionToken()
	if err != nil {
		return Project{}, err
	}
	id = id[:22]
	gitCipher, keyCipher, err := s.encryptCredentials(id, in)
	if err != nil {
		return Project{}, err
	}
	build, _ := json.Marshal(in.BuildSteps)
	deploy, _ := json.Marshal(in.DeploySteps)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = s.db.ExecContext(ctx, `INSERT INTO projects(id,name,slug,description,repository_url,branch,auth_type,git_username,git_secret_cipher,ssh_private_key_cipher,ssh_known_hosts,build_steps_json,deploy_steps_json,step_timeout_seconds,deployment_timeout_seconds,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, in.Name, in.Slug, in.Description, in.RepositoryURL, in.Branch, in.AuthType, in.GitUsername, gitCipher, keyCipher, in.SSHKnownHosts, string(build), string(deploy), in.StepTimeoutSeconds, in.DeploymentTimeoutSeconds, now, now)
	if err != nil {
		return Project{}, fmt.Errorf("create project: %w", err)
	}
	return s.Get(ctx, id)
}

func (s *Service) List(ctx context.Context) ([]Project, error) {
	rows, err := s.db.QueryContext(ctx, selectProject+` WHERE archived_at IS NULL ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Project{}
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, p)
	}
	return result, rows.Err()
}

func (s *Service) Get(ctx context.Context, id string) (Project, error) {
	return scanProject(s.db.QueryRowContext(ctx, selectProject+` WHERE id=? AND archived_at IS NULL`, id))
}

func (s *Service) Update(ctx context.Context, id string, in Input) (Project, error) {
	if err := validate(&in); err != nil {
		return Project{}, err
	}
	existing, err := s.Get(ctx, id)
	if err != nil {
		return Project{}, err
	}
	var gitCipher, keyCipher any
	if in.GitSecret != "" || in.SSHPrivateKey != "" {
		gitCipher, keyCipher, err = s.encryptCredentials(id, in)
		if err != nil {
			return Project{}, err
		}
	}
	build, _ := json.Marshal(in.BuildSteps)
	deploy, _ := json.Marshal(in.DeploySteps)
	query := `UPDATE projects SET name=?,slug=?,description=?,repository_url=?,branch=?,auth_type=?,git_username=?,ssh_known_hosts=?,build_steps_json=?,deploy_steps_json=?,step_timeout_seconds=?,deployment_timeout_seconds=?,updated_at=?`
	args := []any{in.Name, in.Slug, in.Description, in.RepositoryURL, in.Branch, in.AuthType, in.GitUsername, in.SSHKnownHosts, string(build), string(deploy), in.StepTimeoutSeconds, in.DeploymentTimeoutSeconds, time.Now().UTC().Format(time.RFC3339Nano)}
	if in.GitSecret != "" {
		query += `,git_secret_cipher=?`
		args = append(args, gitCipher)
	} else if in.AuthType != "https" && existing.HasGitSecret {
		query += `,git_secret_cipher=NULL`
	}
	if in.SSHPrivateKey != "" {
		query += `,ssh_private_key_cipher=?`
		args = append(args, keyCipher)
	} else if in.AuthType != "ssh" && existing.HasSSHPrivateKey {
		query += `,ssh_private_key_cipher=NULL`
	}
	query += ` WHERE id=? AND archived_at IS NULL`
	args = append(args, id)
	res, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return Project{}, fmt.Errorf("update project: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return Project{}, sql.ErrNoRows
	}
	return s.Get(ctx, id)
}

func (s *Service) Archive(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE projects SET archived_at=?,updated_at=? WHERE id=? AND archived_at IS NULL`, time.Now().UTC().Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Service) PutVariable(ctx context.Context, projectID string, in VariableInput) (Variable, error) {
	if !namePattern.MatchString(in.Name) || len(in.Name) > 128 {
		return Variable{}, errors.New("invalid environment variable name")
	}
	var cipherValue []byte
	plain := any(in.Value)
	if in.IsSecret {
		var err error
		cipherValue, err = s.box.Encrypt([]byte(in.Value), "project:"+projectID+":variable:"+in.Name)
		if err != nil {
			return Variable{}, err
		}
		plain = nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Variable{}, err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var version int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0)+1 FROM project_variables WHERE project_id=? AND name=?`, projectID, in.Name).Scan(&version); err != nil {
		return Variable{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE project_variables SET replaced_at=? WHERE project_id=? AND name=? AND replaced_at IS NULL`, now, projectID, in.Name); err != nil {
		return Variable{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO project_variables(project_id,name,version,is_secret,plain_value,cipher_value,created_at) VALUES(?,?,?,?,?,?,?)`, projectID, in.Name, version, in.IsSecret, plain, cipherValue, now); err != nil {
		return Variable{}, err
	}
	if err = tx.Commit(); err != nil {
		return Variable{}, err
	}
	return Variable{Name: in.Name, Value: map[bool]string{false: in.Value}[in.IsSecret], IsSecret: in.IsSecret, Version: version, UpdatedAt: now}, nil
}

func (s *Service) ListVariables(ctx context.Context, projectID string) ([]Variable, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name,is_secret,COALESCE(plain_value,''),version,created_at FROM project_variables WHERE project_id=? AND replaced_at IS NULL ORDER BY name`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Variable{}
	for rows.Next() {
		var v Variable
		if err := rows.Scan(&v.Name, &v.IsSecret, &v.Value, &v.Version, &v.UpdatedAt); err != nil {
			return nil, err
		}
		if v.IsSecret {
			v.Value = ""
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Service) DeleteVariable(ctx context.Context, projectID, name string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE project_variables SET replaced_at=? WHERE project_id=? AND name=? AND replaced_at IS NULL`, time.Now().UTC().Format(time.RFC3339Nano), projectID, name)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Service) encryptCredentials(id string, in Input) ([]byte, []byte, error) {
	var a, b []byte
	var err error
	if in.GitSecret != "" {
		a, err = s.box.Encrypt([]byte(in.GitSecret), "project:"+id+":git-secret")
		if err != nil {
			return nil, nil, err
		}
	}
	if in.SSHPrivateKey != "" {
		b, err = s.box.Encrypt([]byte(in.SSHPrivateKey), "project:"+id+":ssh-key")
		if err != nil {
			return nil, nil, err
		}
	}
	return a, b, nil
}

func validate(in *Input) error {
	in.Name = strings.TrimSpace(in.Name)
	in.Slug = strings.ToLower(strings.TrimSpace(in.Slug))
	in.RepositoryURL = strings.TrimSpace(in.RepositoryURL)
	in.Branch = strings.TrimSpace(in.Branch)
	if in.AuthType == "" {
		in.AuthType = "none"
	}
	if in.StepTimeoutSeconds == 0 {
		in.StepTimeoutSeconds = 900
	}
	if in.DeploymentTimeoutSeconds == 0 {
		in.DeploymentTimeoutSeconds = 3600
	}
	if in.Name == "" || len(in.Name) > 128 {
		return errors.New("project name is required and must not exceed 128 characters")
	}
	if !slugPattern.MatchString(in.Slug) {
		return errors.New("slug must contain lowercase letters, numbers, and single hyphens")
	}
	if in.Branch == "" || strings.HasPrefix(in.Branch, "-") {
		return errors.New("invalid branch")
	}
	if !validRepository(in.RepositoryURL) {
		return errors.New("repository URL must use HTTPS or SSH")
	}
	if in.AuthType != "none" && in.AuthType != "https" && in.AuthType != "ssh" {
		return errors.New("invalid auth type")
	}
	if in.StepTimeoutSeconds < 1 || in.DeploymentTimeoutSeconds < 1 || in.StepTimeoutSeconds > in.DeploymentTimeoutSeconds {
		return errors.New("invalid timeout configuration")
	}
	for _, steps := range [][]Step{in.BuildSteps, in.DeploySteps} {
		for _, step := range steps {
			if strings.TrimSpace(step.Name) == "" || strings.TrimSpace(step.Command) == "" {
				return errors.New("every step requires a name and command")
			}
		}
	}
	return nil
}

func validRepository(value string) bool {
	if strings.HasPrefix(value, "git@") {
		return strings.Contains(value, ":")
	}
	u, err := url.Parse(value)
	return err == nil && (u.Scheme == "https" || u.Scheme == "ssh") && u.Host != ""
}

const selectProject = `SELECT id,name,slug,description,repository_url,branch,auth_type,git_username,git_secret_cipher IS NOT NULL,ssh_private_key_cipher IS NOT NULL,ssh_known_hosts,build_steps_json,deploy_steps_json,step_timeout_seconds,deployment_timeout_seconds,created_at,updated_at FROM projects`

type scanner interface{ Scan(...any) error }

func scanProject(row scanner) (Project, error) {
	var p Project
	var build, deploy string
	err := row.Scan(&p.ID, &p.Name, &p.Slug, &p.Description, &p.RepositoryURL, &p.Branch, &p.AuthType, &p.GitUsername, &p.HasGitSecret, &p.HasSSHPrivateKey, &p.SSHKnownHosts, &build, &deploy, &p.StepTimeoutSeconds, &p.DeploymentTimeoutSeconds, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return Project{}, err
	}
	if err = json.Unmarshal([]byte(build), &p.BuildSteps); err != nil {
		return Project{}, err
	}
	if err = json.Unmarshal([]byte(deploy), &p.DeploySteps); err != nil {
		return Project{}, err
	}
	return p, nil
}
