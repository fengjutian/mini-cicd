package pipelineconfig

import (
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	raw := []byte("version: 1\npipeline:\n  build:\n    - name: Build\n      command: npm run build\n  deploy:\n    - name: Restart\n      command: systemctl --user restart app\ntimeouts:\n  step: 5m\n  deployment: 20m\n")
	got, err := Parse(raw, Resolved{StepTimeout: time.Minute, DeploymentTimeout: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Build) != 1 || len(got.Deploy) != 1 || got.StepTimeout != 5*time.Minute {
		t.Fatalf("unexpected config: %#v", got)
	}
}

func TestParseRejectsUnknownAndTraversal(t *testing.T) {
	defaults := Resolved{StepTimeout: time.Minute, DeploymentTimeout: time.Hour}
	for _, raw := range []string{
		"version: 1\nunknown: true\npipeline:\n  build: [{name: x, command: y}]\n",
		"version: 1\npipeline:\n  build:\n    - {name: x, command: y, workingDirectory: ../outside}\n",
	} {
		if _, err := Parse([]byte(raw), defaults); err == nil {
			t.Fatalf("expected error for %q", raw)
		}
	}
}

func TestParseDeploymentAdapters(t *testing.T) {
	defaults := Resolved{StepTimeout: time.Minute, DeploymentTimeout: time.Hour}
	tests := []struct{ yaml, want string }{
		{"version: 1\npipeline:\n  build: [{name: x, command: y}]\napplication:\n  adapter: docker-compose\n  composeFile: deploy/compose.yaml\n  build: true\n  services: [web]\n", "docker compose -f 'deploy/compose.yaml' up -d --build --remove-orphans web"},
		{"version: 1\npipeline:\n  build: [{name: x, command: y}]\napplication:\n  adapter: systemd\n  unit: my-app.service\n", "systemctl --user restart my-app.service"},
		{"version: 1\npipeline:\n  build: [{name: x, command: y}]\napplication:\n  adapter: pm2\n  ecosystemFile: deploy/ecosystem.js\n  processName: api\n  environment: production\n", "pm2 startOrReload 'deploy/ecosystem.js' --env production --update-env"},
	}
	for _, tt := range tests {
		got, err := Parse([]byte(tt.yaml), defaults)
		if err != nil {
			t.Fatal(err)
		}
		if got.Deploy[len(got.Deploy)-1].Command != tt.want {
			t.Fatalf("got %q want %q", got.Deploy[len(got.Deploy)-1].Command, tt.want)
		}
	}
}

func TestAdapterCanBeTheOnlyDeployStep(t *testing.T) {
	raw := []byte("version: 1\napplication:\n  adapter: systemd\n  unit: app.service\n")
	got, err := Parse(raw, Resolved{StepTimeout: time.Minute, DeploymentTimeout: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Deploy) != 1 {
		t.Fatalf("expected generated deploy step, got %#v", got.Deploy)
	}
}

func TestNotificationConfig(t *testing.T) {
	raw := []byte("version: 1\npipeline:\n  build: [{name: x, command: y}]\nnotifications:\n  - name: ops\n    type: webhook\n    urlVariable: DEPLOY_WEBHOOK_URL\n    events: [succeeded, failed]\n")
	got, err := Parse(raw, Resolved{StepTimeout: time.Minute, DeploymentTimeout: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Notifications) != 1 || got.Notifications[0].URLVariable != "DEPLOY_WEBHOOK_URL" {
		t.Fatalf("unexpected notifications: %#v", got.Notifications)
	}
}

func TestArtifactConfig(t *testing.T) {
	raw := []byte("version: 1\npipeline:\n  build: [{name: x, command: y}]\nartifacts:\n  paths: [dist, backend/bin]\n")
	got, err := Parse(raw, Resolved{StepTimeout: time.Minute, DeploymentTimeout: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if got.Artifacts.Retention != 5 || len(got.Artifacts.Paths) != 2 {
		t.Fatalf("artifacts: %#v", got.Artifacts)
	}
}

func TestEnvironmentConfig(t *testing.T) {
	raw := []byte("version: 1\npipeline:\n  deploy: [{name: x, command: y}]\nenvironments:\n  production:\n    approvalRequired: true\n    allowedBranches: [main]\n    deploymentWindow:\n      days: [mon, tue, wed, thu, fri]\n      start: 09:00\n      end: 18:00\n      timezone: +08:00\n")
	got, err := Parse(raw, Resolved{StepTimeout: time.Minute, DeploymentTimeout: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Environments["production"].ApprovalRequired {
		t.Fatal("approval not parsed")
	}
}

func TestCommitStatusConfig(t *testing.T) {
	raw := []byte("version: 1\npipeline:\n  deploy: [{name: x, command: y}]\ncommitStatus:\n  enabled: true\n  provider: github\n  repository: org/repo\n  tokenVariable: GITHUB_TOKEN\n")
	got, err := Parse(raw, Resolved{StepTimeout: time.Minute, DeploymentTimeout: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if got.CommitStatus.APIBase != "https://api.github.com" || got.CommitStatus.Context != "mini-ci-cd" {
		t.Fatalf("status: %#v", got.CommitStatus)
	}
}

func TestCacheConfig(t *testing.T) {
	raw := []byte("version: 1\npipeline:\n  build: [{name: build, command: npm_ci}]\ncache:\n  key: node\n  keyFiles: [package-lock.json]\n  paths: [node_modules]\n  restoreKeys: [node-]\n")
	got, err := Parse(raw, Resolved{StepTimeout: time.Minute, DeploymentTimeout: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if got.Cache.Key != "node" || got.Cache.Retention != 10 || len(got.Cache.Paths) != 1 {
		t.Fatalf("cache: %#v", got.Cache)
	}
}
