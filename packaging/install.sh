#!/usr/bin/env bash
set -euo pipefail
if [[ ${EUID} -ne 0 ]]; then echo "run as root" >&2; exit 1; fi
: "${MINICICD_BINARY:=./minicicd-linux-amd64}"
install -m 0755 "$MINICICD_BINARY" /usr/local/bin/minicicd
id minicicd >/dev/null 2>&1 || useradd --system --home /var/lib/minicicd --shell /usr/sbin/nologin minicicd
install -d -m 0700 -o minicicd -g minicicd /var/lib/minicicd
install -d -m 0750 /etc/minicicd
if [[ ! -f /etc/minicicd/minicicd.env ]]; then
  key="$(head -c 32 /dev/urandom | base64 | tr -d '=\n')"
  printf 'MINICICD_LISTEN_ADDR=127.0.0.1:8080\nMINICICD_DATA_DIR=/var/lib/minicicd\nMINICICD_MASTER_KEY=%s\nMINICICD_SECURE_COOKIES=false\n' "$key" >/etc/minicicd/minicicd.env
  chmod 0600 /etc/minicicd/minicicd.env
fi
install -m 0644 "$(dirname "$0")/minicicd.service" /etc/systemd/system/minicicd.service
systemctl daemon-reload
systemctl enable --now minicicd
echo "mini-ci-cd installed; open http://127.0.0.1:8080"
