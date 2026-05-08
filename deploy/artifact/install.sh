#!/usr/bin/env bash
set -euo pipefail

ARTIFACT_DIR=""
APP_USER="ptxt"
APP_DIR="/opt/ptxt-nstr"
DATA_DIR="/var/lib/ptxt-nstr"
DOMAIN_NAME=""
CADDY_VERSION="2.9.1"
GOMEMLIMIT_VALUE="1GiB"
PTXT_ADDR_VALUE="127.0.0.1:8080"
PTXT_DB_VALUE="/var/lib/ptxt-nstr/ptxt-nstr.sqlite"
PTXT_DEBUG_VALUE="false"
PTXT_EVENT_RETENTION_VALUE="20000"
PTXT_COMPACT_ON_START_VALUE="false"

usage() {
  cat <<'EOF'
Usage: install.sh --artifact-dir DIR --domain DOMAIN [options]

Options:
  --artifact-dir DIR
  --domain DOMAIN
  --app-user USER
  --app-dir DIR
  --data-dir DIR
  --caddy-version VERSION
  --gomemlimit VALUE
  --ptxt-addr VALUE
  --ptxt-db VALUE
  --ptxt-debug VALUE
  --ptxt-event-retention VALUE
  --ptxt-compact-on-start VALUE
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --artifact-dir)
      ARTIFACT_DIR="$2"
      shift 2
      ;;
    --domain)
      DOMAIN_NAME="$2"
      shift 2
      ;;
    --app-user)
      APP_USER="$2"
      shift 2
      ;;
    --app-dir)
      APP_DIR="$2"
      shift 2
      ;;
    --data-dir)
      DATA_DIR="$2"
      shift 2
      ;;
    --caddy-version)
      CADDY_VERSION="$2"
      shift 2
      ;;
    --gomemlimit)
      GOMEMLIMIT_VALUE="$2"
      shift 2
      ;;
    --ptxt-addr)
      PTXT_ADDR_VALUE="$2"
      shift 2
      ;;
    --ptxt-db)
      PTXT_DB_VALUE="$2"
      shift 2
      ;;
    --ptxt-debug)
      PTXT_DEBUG_VALUE="$2"
      shift 2
      ;;
    --ptxt-event-retention)
      PTXT_EVENT_RETENTION_VALUE="$2"
      shift 2
      ;;
    --ptxt-compact-on-start)
      PTXT_COMPACT_ON_START_VALUE="$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage >&2
      exit 1
      ;;
  esac
done

if [[ -z "$ARTIFACT_DIR" || -z "$DOMAIN_NAME" ]]; then
  usage >&2
  exit 1
fi

if [[ $EUID -ne 0 ]]; then
  echo "install.sh must run as root" >&2
  exit 1
fi

need_file() {
  local path="$1"
  if [[ ! -f "$path" ]]; then
    echo "required artifact file missing: $path" >&2
    exit 1
  fi
}

render_template() {
  local src="$1"
  local dst="$2"

  sed \
    -e "s|__APP_USER__|$APP_USER|g" \
    -e "s|__APP_DIR__|$APP_DIR|g" \
    -e "s|__DATA_DIR__|$DATA_DIR|g" \
    -e "s|__DOMAIN_NAME__|$DOMAIN_NAME|g" \
    -e "s|__GOMEMLIMIT__|$GOMEMLIMIT_VALUE|g" \
    -e "s|__PTXT_ADDR__|$PTXT_ADDR_VALUE|g" \
    -e "s|__PTXT_DB__|$PTXT_DB_VALUE|g" \
    -e "s|__PTXT_DEBUG__|$PTXT_DEBUG_VALUE|g" \
    -e "s|__PTXT_EVENT_RETENTION__|$PTXT_EVENT_RETENTION_VALUE|g" \
    -e "s|__PTXT_COMPACT_ON_START__|$PTXT_COMPACT_ON_START_VALUE|g" \
    "$src" >"$dst"
}

need_file "$ARTIFACT_DIR/ptxt-nstr"
need_file "$ARTIFACT_DIR/ptxt-nstr.env.tmpl"
need_file "$ARTIFACT_DIR/ptxt-nstr.service.tmpl"
need_file "$ARTIFACT_DIR/Caddyfile.tmpl"
need_file "$ARTIFACT_DIR/caddy.service.tmpl"
need_file "$ARTIFACT_DIR/amazon-cloudwatch-agent.json.tmpl"

dnf install -y tar gzip shadow-utils unzip amazon-cloudwatch-agent

if ! id -u "$APP_USER" >/dev/null 2>&1; then
  useradd --system --home-dir "$APP_DIR" --shell /sbin/nologin "$APP_USER"
fi

install -d -m 0755 "$APP_DIR/bin" "$DATA_DIR" /etc/ptxt-nstr /etc/caddy /var/log/ptxt-nstr /var/log/caddy
chown -R "$APP_USER:$APP_USER" "$APP_DIR" "$DATA_DIR" /var/log/ptxt-nstr
chown -R root:root /etc/ptxt-nstr /etc/caddy /var/log/caddy
touch /var/log/ptxt-nstr/app.log /var/log/caddy/access.log
chown "$APP_USER:$APP_USER" /var/log/ptxt-nstr/app.log
chown root:root /var/log/caddy/access.log
chmod 0644 /var/log/ptxt-nstr/app.log /var/log/caddy/access.log

install -m 0755 "$ARTIFACT_DIR/ptxt-nstr" "$APP_DIR/bin/ptxt-nstr"

CADDY_TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$CADDY_TMP_DIR"' EXIT
CADDY_URL="https://github.com/caddyserver/caddy/releases/download/v${CADDY_VERSION}/caddy_${CADDY_VERSION}_linux_arm64.tar.gz"
curl -fsSL "$CADDY_URL" -o "$CADDY_TMP_DIR/caddy.tar.gz"
tar -xzf "$CADDY_TMP_DIR/caddy.tar.gz" -C "$CADDY_TMP_DIR"
install -m 0755 "$CADDY_TMP_DIR/caddy" /usr/local/bin/caddy

render_template "$ARTIFACT_DIR/ptxt-nstr.env.tmpl" /etc/ptxt-nstr/ptxt-nstr.env
render_template "$ARTIFACT_DIR/ptxt-nstr.service.tmpl" /etc/systemd/system/ptxt-nstr.service
render_template "$ARTIFACT_DIR/Caddyfile.tmpl" /etc/caddy/Caddyfile
render_template "$ARTIFACT_DIR/caddy.service.tmpl" /etc/systemd/system/caddy.service
render_template "$ARTIFACT_DIR/amazon-cloudwatch-agent.json.tmpl" /opt/aws/amazon-cloudwatch-agent/etc/amazon-cloudwatch-agent.json

chmod 0644 /etc/ptxt-nstr/ptxt-nstr.env /etc/caddy/Caddyfile /etc/systemd/system/ptxt-nstr.service /etc/systemd/system/caddy.service /opt/aws/amazon-cloudwatch-agent/etc/amazon-cloudwatch-agent.json

systemctl daemon-reload
systemctl enable ptxt-nstr.service caddy.service amazon-cloudwatch-agent.service
systemctl restart ptxt-nstr.service
systemctl restart caddy.service
systemctl restart amazon-cloudwatch-agent.service
