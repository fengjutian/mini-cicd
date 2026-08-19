package pipelineconfig

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/charlesfeng/mini-cicd/apps/server/internal/project"
	"gopkg.in/yaml.v3"
)

const Filename = "mini-ci-cd.yml"

type Config struct {
	Version  int `yaml:"version" json:"version"`
	Pipeline struct {
		Build  []Step `yaml:"build" json:"build"`
		Deploy []Step `yaml:"deploy" json:"deploy"`
	} `yaml:"pipeline" json:"pipeline"`
	Timeouts struct {
		Step       string `yaml:"step" json:"step,omitempty"`
		Deployment string `yaml:"deployment" json:"deployment,omitempty"`
	} `yaml:"timeouts" json:"timeouts"`
	Application   Application            `yaml:"application" json:"application"`
	Notifications []Notification         `yaml:"notifications" json:"notifications"`
	Artifacts     ArtifactConfig         `yaml:"artifacts" json:"artifacts"`
	Environments  map[string]Environment `yaml:"environments" json:"environments"`
	CommitStatus  CommitStatus           `yaml:"commitStatus" json:"commitStatus"`
}

type CommitStatus struct {
	Enabled       bool   `yaml:"enabled" json:"enabled"`
	Provider      string `yaml:"provider" json:"provider"`
	Repository    string `yaml:"repository" json:"repository"`
	TokenVariable string `yaml:"tokenVariable" json:"tokenVariable"`
	APIBase       string `yaml:"apiBase" json:"apiBase,omitempty"`
	Context       string `yaml:"context" json:"context"`
}

type Environment struct {
	ApprovalRequired bool              `yaml:"approvalRequired" json:"approvalRequired"`
	AllowedBranches  []string          `yaml:"allowedBranches" json:"allowedBranches"`
	Frozen           bool              `yaml:"frozen" json:"frozen"`
	Window           *DeploymentWindow `yaml:"deploymentWindow" json:"deploymentWindow,omitempty"`
}
type DeploymentWindow struct {
	Days     []string `yaml:"days" json:"days"`
	Start    string   `yaml:"start" json:"start"`
	End      string   `yaml:"end" json:"end"`
	Timezone string   `yaml:"timezone" json:"timezone"`
}

type ArtifactConfig struct {
	Paths     []string `yaml:"paths" json:"paths"`
	Retention int      `yaml:"retention" json:"retention"`
}

type Notification struct {
	Name        string   `yaml:"name" json:"name"`
	Type        string   `yaml:"type" json:"type"`
	URLVariable string   `yaml:"urlVariable" json:"urlVariable"`
	Events      []string `yaml:"events" json:"events"`
}

type Application struct {
	Adapter       string   `yaml:"adapter" json:"adapter,omitempty"`
	ComposeFile   string   `yaml:"composeFile" json:"composeFile,omitempty"`
	ProjectName   string   `yaml:"projectName" json:"projectName,omitempty"`
	Services      []string `yaml:"services" json:"services,omitempty"`
	Build         bool     `yaml:"build" json:"build,omitempty"`
	Unit          string   `yaml:"unit" json:"unit,omitempty"`
	EcosystemFile string   `yaml:"ecosystemFile" json:"ecosystemFile,omitempty"`
	ProcessName   string   `yaml:"processName" json:"processName,omitempty"`
	Environment   string   `yaml:"environment" json:"environment,omitempty"`
}

type Step struct {
	Name             string `yaml:"name" json:"name"`
	Command          string `yaml:"command" json:"command"`
	WorkingDirectory string `yaml:"workingDirectory" json:"workingDirectory,omitempty"`
}

type Resolved struct {
	Build, Deploy                  []project.Step
	StepTimeout, DeploymentTimeout time.Duration
	Application                    Application
	Notifications                  []Notification
	Artifacts                      ArtifactConfig
	Environments                   map[string]Environment
	CommitStatus                   CommitStatus
}

var safeName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.@-]*$`)

func Parse(raw []byte, defaults Resolved) (Resolved, error) {
	var cfg Config
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return Resolved{}, fmt.Errorf("parse %s: %w", Filename, err)
	}
	if cfg.Version != 1 {
		return Resolved{}, errors.New("mini-ci-cd.yml version must be 1")
	}
	out := defaults
	out.Build = convert(cfg.Pipeline.Build)
	out.Deploy = convert(cfg.Pipeline.Deploy)
	out.Application = cfg.Application
	out.Notifications = cfg.Notifications
	out.Artifacts = cfg.Artifacts
	out.Environments = cfg.Environments
	out.CommitStatus = cfg.CommitStatus
	if cfg.Timeouts.Step != "" {
		d, err := time.ParseDuration(cfg.Timeouts.Step)
		if err != nil || d <= 0 {
			return Resolved{}, errors.New("timeouts.step must be a positive duration")
		}
		out.StepTimeout = d
	}
	if cfg.Timeouts.Deployment != "" {
		d, err := time.ParseDuration(cfg.Timeouts.Deployment)
		if err != nil || d <= 0 {
			return Resolved{}, errors.New("timeouts.deployment must be a positive duration")
		}
		out.DeploymentTimeout = d
	}
	if out.StepTimeout <= 0 || out.DeploymentTimeout <= 0 || out.StepTimeout > out.DeploymentTimeout {
		return Resolved{}, errors.New("step timeout must not exceed deployment timeout")
	}
	for _, step := range append(append([]project.Step{}, out.Build...), out.Deploy...) {
		if strings.TrimSpace(step.Name) == "" || strings.TrimSpace(step.Command) == "" {
			return Resolved{}, errors.New("every pipeline step requires name and command")
		}
		dir := path.Clean(strings.ReplaceAll(strings.TrimSpace(step.WorkingDirectory), `\`, "/"))
		if dir == ".." || strings.HasPrefix(dir, "../") || path.IsAbs(dir) {
			return Resolved{}, errors.New("step workingDirectory must stay inside the repository")
		}
	}
	adapterStep, err := applicationStep(&out.Application)
	if err != nil {
		return Resolved{}, err
	}
	if adapterStep != nil {
		out.Deploy = append(out.Deploy, *adapterStep)
	}
	if len(out.Build)+len(out.Deploy) == 0 {
		return Resolved{}, errors.New("pipeline or application adapter must contain at least one step")
	}
	if err := validateNotifications(out.Notifications); err != nil {
		return Resolved{}, err
	}
	if err := validateArtifacts(&out.Artifacts); err != nil {
		return Resolved{}, err
	}
	if err := validateEnvironments(out.Environments); err != nil {
		return Resolved{}, err
	}
	if err := validateCommitStatus(&out.CommitStatus); err != nil {
		return Resolved{}, err
	}
	return out, nil
}

func validateCommitStatus(cfg *CommitStatus) error {
	if !cfg.Enabled {
		return nil
	}
	cfg.Provider = strings.ToLower(strings.TrimSpace(cfg.Provider))
	if cfg.Provider != "github" && cfg.Provider != "gitlab" {
		return errors.New("commitStatus.provider must be github or gitlab")
	}
	if strings.TrimSpace(cfg.Repository) == "" || strings.Contains(cfg.Repository, "..") {
		return errors.New("commitStatus.repository is required")
	}
	if !regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`).MatchString(cfg.TokenVariable) {
		return errors.New("commitStatus.tokenVariable must be an environment variable name")
	}
	if cfg.Context == "" {
		cfg.Context = "mini-ci-cd"
	}
	if cfg.APIBase == "" {
		if cfg.Provider == "github" {
			cfg.APIBase = "https://api.github.com"
		} else {
			cfg.APIBase = "https://gitlab.com/api/v4"
		}
	}
	return nil
}

func validateEnvironments(items map[string]Environment) error {
	envName := regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	days := map[string]bool{"sun": true, "mon": true, "tue": true, "wed": true, "thu": true, "fri": true, "sat": true}
	for name, cfg := range items {
		if !envName.MatchString(name) {
			return errors.New("environment names must use lowercase letters, numbers, and hyphens")
		}
		for _, branch := range cfg.AllowedBranches {
			if strings.TrimSpace(branch) == "" || strings.HasPrefix(branch, "-") {
				return fmt.Errorf("environment %s has an invalid allowed branch", name)
			}
		}
		if cfg.Window != nil {
			w := cfg.Window
			if len(w.Days) == 0 {
				return fmt.Errorf("environment %s deploymentWindow requires days", name)
			}
			seen := map[string]bool{}
			for _, day := range w.Days {
				day = strings.ToLower(day)
				if !days[day] || seen[day] {
					return fmt.Errorf("environment %s deploymentWindow has invalid days", name)
				}
				seen[day] = true
			}
			if _, err := time.Parse("15:04", w.Start); err != nil {
				return fmt.Errorf("environment %s deploymentWindow start must use HH:MM", name)
			}
			if _, err := time.Parse("15:04", w.End); err != nil || w.Start == w.End {
				return fmt.Errorf("environment %s deploymentWindow end must use a different HH:MM", name)
			}
			if _, err := time.Parse("-07:00", w.Timezone); err != nil {
				return fmt.Errorf("environment %s deploymentWindow timezone must be a UTC offset", name)
			}
		}
	}
	return nil
}

func validateArtifacts(cfg *ArtifactConfig) error {
	if len(cfg.Paths) == 0 {
		if cfg.Retention != 0 {
			return errors.New("artifacts.retention requires artifact paths")
		}
		return nil
	}
	if cfg.Retention == 0 {
		cfg.Retention = 5
	}
	if cfg.Retention < 1 || cfg.Retention > 50 {
		return errors.New("artifacts.retention must be between 1 and 50")
	}
	seen := map[string]bool{}
	for i, p := range cfg.Paths {
		p = strings.TrimSpace(p)
		if err := safeRelativePath(p); err != nil {
			return fmt.Errorf("artifacts.paths: %w", err)
		}
		clean := path.Clean(strings.ReplaceAll(p, `\`, "/"))
		if seen[clean] {
			return errors.New("artifacts.paths contains a duplicate")
		}
		seen[clean] = true
		cfg.Paths[i] = clean
	}
	return nil
}

func validateNotifications(items []Notification) error {
	variable := regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	seen := map[string]bool{}
	allowed := map[string]bool{"succeeded": true, "failed": true, "cancelled": true, "timed_out": true}
	for i := range items {
		n := &items[i]
		n.Name, n.Type, n.URLVariable = strings.TrimSpace(n.Name), strings.ToLower(strings.TrimSpace(n.Type)), strings.TrimSpace(n.URLVariable)
		if n.Name == "" || len(n.Name) > 64 || seen[n.Name] {
			return errors.New("notification names must be unique and non-empty")
		}
		seen[n.Name] = true
		if n.Type != "webhook" {
			return errors.New("notification type must be webhook")
		}
		if !variable.MatchString(n.URLVariable) {
			return errors.New("notification urlVariable must be an environment variable name")
		}
		if len(n.Events) == 0 {
			n.Events = []string{"failed", "timed_out"}
		}
		events := map[string]bool{}
		for _, event := range n.Events {
			if !allowed[event] || events[event] {
				return errors.New("notification events contain an invalid or duplicate status")
			}
			events[event] = true
		}
	}
	return nil
}

func applicationStep(app *Application) (*project.Step, error) {
	app.Adapter = strings.ToLower(strings.TrimSpace(app.Adapter))
	if app.Adapter == "" {
		return nil, nil
	}
	switch app.Adapter {
	case "docker-compose":
		if app.Unit != "" || app.EcosystemFile != "" || app.ProcessName != "" || app.Environment != "" {
			return nil, errors.New("docker-compose adapter contains fields for another adapter")
		}
		if app.ComposeFile == "" {
			app.ComposeFile = "compose.yaml"
		}
		if err := safeRelativePath(app.ComposeFile); err != nil {
			return nil, fmt.Errorf("application.composeFile: %w", err)
		}
		args := []string{"docker", "compose", "-f", shellQuote(app.ComposeFile)}
		if app.ProjectName != "" {
			if !safeName.MatchString(app.ProjectName) {
				return nil, errors.New("application.projectName is invalid")
			}
			args = append(args, "-p", app.ProjectName)
		}
		args = append(args, "up", "-d")
		if app.Build {
			args = append(args, "--build")
		}
		args = append(args, "--remove-orphans")
		for _, service := range app.Services {
			if !safeName.MatchString(service) {
				return nil, errors.New("application.services contains an invalid service name")
			}
			args = append(args, service)
		}
		return &project.Step{Name: "Deploy with Docker Compose", Command: strings.Join(args, " ")}, nil
	case "systemd":
		if app.ComposeFile != "" || app.ProjectName != "" || len(app.Services) != 0 || app.Build || app.EcosystemFile != "" || app.ProcessName != "" || app.Environment != "" {
			return nil, errors.New("systemd adapter contains fields for another adapter")
		}
		if !safeName.MatchString(app.Unit) {
			return nil, errors.New("application.unit is required and must be a valid systemd unit")
		}
		return &project.Step{Name: "Restart systemd service", Command: "systemctl --user restart " + app.Unit}, nil
	case "pm2":
		if app.ComposeFile != "" || app.ProjectName != "" || len(app.Services) != 0 || app.Build || app.Unit != "" {
			return nil, errors.New("pm2 adapter contains fields for another adapter")
		}
		if app.EcosystemFile == "" {
			app.EcosystemFile = "ecosystem.config.js"
		}
		if !safeName.MatchString(app.ProcessName) {
			return nil, errors.New("application.processName is required and must be valid for PM2")
		}
		if err := safeRelativePath(app.EcosystemFile); err != nil {
			return nil, fmt.Errorf("application.ecosystemFile: %w", err)
		}
		cmd := "pm2 startOrReload " + shellQuote(app.EcosystemFile)
		if app.Environment != "" {
			if !safeName.MatchString(app.Environment) {
				return nil, errors.New("application.environment is invalid")
			}
			cmd += " --env " + app.Environment
		}
		cmd += " --update-env"
		return &project.Step{Name: "Deploy with PM2", Command: cmd}, nil
	default:
		return nil, errors.New("application.adapter must be docker-compose, systemd, or pm2")
	}
}

func safeRelativePath(value string) error {
	clean := path.Clean(strings.ReplaceAll(strings.TrimSpace(value), `\`, "/"))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || path.IsAbs(clean) {
		return errors.New("must stay inside the repository")
	}
	return nil
}

func shellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }

func convert(in []Step) []project.Step {
	out := make([]project.Step, 0, len(in))
	for _, s := range in {
		out = append(out, project.Step{Name: strings.TrimSpace(s.Name), Command: s.Command, WorkingDirectory: strings.TrimSpace(s.WorkingDirectory)})
	}
	return out
}
