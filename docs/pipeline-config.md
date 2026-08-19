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
  environment: production
```

The respective Docker, systemd, or PM2 executable must be available to the
runner service account. Adapter paths must remain inside the checkout.
