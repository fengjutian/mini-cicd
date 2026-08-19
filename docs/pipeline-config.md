# Repository pipeline configuration

Place `mini-ci-cd.yml` at the repository root to version the pipeline together
with the application. When a deployment is created, mini-ci-cd reads the file
from the resolved commit and stores the exact content and effective timeouts on
the deployment. Redeploying an older commit therefore uses that commit's file.

If the file is absent, the project pipeline configured in the UI/API remains the
fallback. If the file exists but is invalid, the deployment is rejected instead
of silently falling back.

```yaml
version: 1

pipeline:
  build:
    - name: Install dependencies
      command: npm ci
    - name: Build
      command: npm run build
  deploy:
    - name: Publish
      command: ./scripts/deploy.sh
      workingDirectory: .

timeouts:
  step: 15m
  deployment: 1h
```

The parser rejects unknown fields, unsupported versions, empty pipelines,
invalid durations, and working directories outside the checkout. Commands run
with the same fixed shell and environment-variable rules as UI-defined steps.

## Deployment adapters

An application adapter adds a final deploy step. Adapter settings are validated
before the deployment is queued and stored with the deployment snapshot.

Docker Compose:

```yaml
application:
  adapter: docker-compose
  composeFile: deploy/compose.yaml
  projectName: my-app
  services: [web, worker]
  build: true
```

User-level systemd unit:

```yaml
application:
  adapter: systemd
  unit: my-app.service
```

The generated command uses `systemctl --user`; system-level root service
management is intentionally not enabled.

PM2:

```yaml
application:
  adapter: pm2
  ecosystemFile: deploy/ecosystem.config.js
  processName: api
  environment: production
```

The respective Docker, systemd, or PM2 executable must be available to the
runner service account. Adapter paths must remain inside the checkout.

After a successful adapter deployment, the project page can query application
status and the latest bounded application logs. Docker Compose uses `ps` and
`logs`, systemd uses `systemctl --user show` and `journalctl --user-unit`, and
PM2 uses `describe` and non-streaming `logs`. Commands time out after 15 seconds,
return at most 1 MiB, and log requests are limited to 2,000 lines.

## Deployment notifications

Webhook endpoints must be stored as project Secrets; URLs are never committed to
the repository configuration. For example, create a Secret named
`DEPLOY_WEBHOOK_URL`, then configure:

```yaml
notifications:
  - name: operations
    type: webhook
    urlVariable: DEPLOY_WEBHOOK_URL
    events: [succeeded, failed, cancelled, timed_out]
```

The JSON payload contains `event`, `notification`, `deploymentId`, `projectId`,
`projectName`, `status`, `commitSha`, and `errorSummary`. Notification jobs are
created transactionally when a deployment becomes terminal. Failed requests are
retried with exponential backoff up to five attempts and survive service
restarts. Consumers should use `deploymentId` and `notification` as an
idempotency key because a timed-out response can be retried.

## Versioned artifacts and fast rollback

Declare repository-relative build outputs that must be preserved after a
successful build:

```yaml
artifacts:
  paths:
    - dist
    - backend/bin
  retention: 5
```

Paths are copied without following symbolic links. A deployment with saved
artifacts exposes a fast rollback action. Rollback checks out the immutable
source commit, restores the saved paths, skips every Build Step, then runs the
snapshotted Deploy Steps and health check. Variables, notifications, adapter
settings, and timeouts also come from the source deployment snapshot.

Retention is between 1 and 50 versions per project. Old versions are removed
after a new snapshot is saved; an artifact currently referenced by an active
rollback is not removed.

## Environments and production protection

Environment policy is versioned with the repository configuration:

```yaml
environments:
  staging:
    allowedBranches: [main, develop]
  production:
    approvalRequired: true
    allowedBranches: [main]
    frozen: false
    deploymentWindow:
      days: [mon, tue, wed, thu, fri]
      start: "09:00"
      end: "18:00"
      timezone: "+08:00"
```

Create a manual deployment with `?environment=staging` or `production`.
Protected deployments remain queued but cannot be claimed by a runner until an
Owner approves them. Frozen environments and deployments outside their window
are rejected before queueing. Environment variables and Secrets use the
environment-specific API and are snapshotted independently.
