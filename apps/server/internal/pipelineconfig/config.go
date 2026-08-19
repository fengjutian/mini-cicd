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
	Application   Application    `yaml:"application" json:"application"`
	Notifications []Notification `yaml:"notifications" json:"notifications"`
	Artifacts     ArtifactConfig `yaml:"artifacts" json:"artifacts"`
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
	return out, nil
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
