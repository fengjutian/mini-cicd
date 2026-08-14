#!/usr/bin/env bash
set -euo pipefail
if [[ ${EUID} -ne 0 ]]; then echo "run as root" >&2; exit 1; fi
: "${MINICICD_BINARY:=./minicicd-linux-amd64}"
install -m 0755 "$MINICICD_BINARY" /usr/local/bin/minicicd
getent group minicicd-control >/dev/null || groupadd --system minicicd-control
getent group minicicd-workspace >/dev/null || groupadd --system minicicd-workspace
id minicicd >/dev/null 2>&1 || useradd --system --home /var/lib/minicicd --shell /usr/sbin/nologin minicicd
id minicicd-job >/dev/null 2>&1 || useradd --system --home /var/lib/minicicd-workspaces --shell /usr/sbin/nologin minicicd-job
usermod -a -G minicicd-control,minicicd-workspace minicicd
usermod -a -G minicicd-workspace minicicd-job
install -d -m 0700 -o minicicd -g minicicd /var/lib/minicicd
install -d -m 2770 -o minicicd-job -g minicicd-workspace /var/lib/minicicd-workspaces
install -d -m 0750 /etc/minicicd
if [[ ! -f /etc/minicicd/minicicd.env ]]; then
  key="$(head -c 32 /dev/urandom | base64 | tr -d '=\n')"
  printf 'MINICICD_LISTEN_ADDR=127.0.0.1:8080\nMINICICD_DATA_DIR=/var/lib/minicicd\nMINICICD_MASTER_KEY=%s\nMINICICD_SECURE_COOKIES=false\nMINICICD_RUNNER_ENDPOINT=/run/minicicd/runner.sock\nMINICICD_RUNNER_WORKSPACE_DIR=/var/lib/minicicd-workspaces\n' "$key" >/etc/minicicd/minicicd.env
  chmod 0600 /etc/minicicd/minicicd.env
fi
grep -q '^MINICICD_RUNNER_ENDPOINT=' /etc/minicicd/minicicd.env || printf 'MINICICD_RUNNER_ENDPOINT=/run/minicicd/runner.sock\nMINICICD_RUNNER_WORKSPACE_DIR=/var/lib/minicicd-workspaces\n' >>/etc/minicicd/minicicd.env
printf 'MINICICD_RUNNER_SOCKET=/run/minicicd/runner.sock\nMINICICD_RUNNER_WORKSPACE_DIR=/var/lib/minicicd-workspaces\nMINICICD_RUNNER_SOCKET_GID=%s\nMINICICD_RUNNER_JOB_UID=%s\nMINICICD_RUNNER_JOB_GID=%s\nMINICICD_SHELL=/bin/bash\n' "$(getent group minicicd-control | cut -d: -f3)" "$(id -u minicicd-job)" "$(getent group minicicd-workspace | cut -d: -f3)" >/etc/minicicd/runner.env
chmod 0600 /etc/minicicd/runner.env
install -m 0644 "$(dirname "$0")/minicicd.service" /etc/systemd/system/minicicd.service
install -m 0644 "$(dirname "$0")/minicicd-runner.service" /etc/systemd/system/minicicd-runner.service
systemctl daemon-reload
systemctl enable --now minicicd-runner minicicd
echo "mini-ci-cd installed; open http://127.0.0.1:8080"
