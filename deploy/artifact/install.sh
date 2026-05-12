#!/usr/bin/env bash
set -euo pipefail

ARTIFACT_DIR=""
APP_USER="ptxt"
APP_DIR="/opt/ptxt-nstr"
DATA_DIR="/var/lib/ptxt-nstr"
DOMAIN_NAME=""
ORIGIN_DOMAIN_NAME=""
CADDY_VERSION="2.9.1"
GOMEMLIMIT_VALUE="1GiB"
PTXT_ADDR_VALUE="127.0.0.1:8080"
PTXT_DB_VALUE="/var/lib/ptxt-nstr/ptxt-nstr.sqlite"
PTXT_DEBUG_VALUE="false"
PTXT_EVENT_RETENTION_VALUE="20000"
PTXT_COMPACT_ON_START_VALUE="false"
DATA_VOLUME_ID=""

usage() {
  cat <<'EOF'
Usage: install.sh --artifact-dir DIR --domain DOMAIN --data-volume-id VOL_ID [options]

Required:
  --artifact-dir DIR
  --domain DOMAIN
  --data-volume-id VOL_ID   EBS volume ID to mount at --data-dir (e.g. vol-0abc...)

Options:
  --origin-domain DOMAIN  (optional; Caddy also serves TLS for this name so a CDN origin can hit it)
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
    --origin-domain)
      ORIGIN_DOMAIN_NAME="$2"
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
    --data-volume-id)
      DATA_VOLUME_ID="$2"
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

if [[ -z "$ARTIFACT_DIR" || -z "$DOMAIN_NAME" || -z "$DATA_VOLUME_ID" ]]; then
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

if [[ -n "$ORIGIN_DOMAIN_NAME" ]]; then
  ORIGIN_DOMAIN_SUFFIX=", $ORIGIN_DOMAIN_NAME"
else
  ORIGIN_DOMAIN_SUFFIX=""
fi

render_template() {
  local src="$1"
  local dst="$2"

  sed \
    -e "s|__APP_USER__|$APP_USER|g" \
    -e "s|__APP_DIR__|$APP_DIR|g" \
    -e "s|__DATA_DIR__|$DATA_DIR|g" \
    -e "s|__DOMAIN_NAME__|$DOMAIN_NAME|g" \
    -e "s|__ORIGIN_DOMAIN_NAME_SUFFIX__|$ORIGIN_DOMAIN_SUFFIX|g" \
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
need_file "$ARTIFACT_DIR/maintenance/502.html"
need_file "$ARTIFACT_DIR/maintenance/503.html"
need_file "$ARTIFACT_DIR/maintenance/ascritch_icon_black.png"
need_file "$ARTIFACT_DIR/maintenance/ascritch_icon_white.png"

dnf install -y tar gzip shadow-utils unzip amazon-cloudwatch-agent xfsprogs util-linux

if ! id -u "$APP_USER" >/dev/null 2>&1; then
  useradd --system --home-dir "$APP_DIR" --shell /sbin/nologin "$APP_USER"
fi

sync_data_fstab() {
  local device="$1"
  local fs_uuid
  fs_uuid="$(blkid -s UUID -o value "$device")"
  if [[ -z "$fs_uuid" ]]; then
    echo "install.sh: could not read UUID from $device" >&2
    exit 1
  fi

  # Rewrite any prior fstab line for $DATA_DIR (e.g. a stale UUID after a
  # mkfs) before inserting the current one. Uses fixed-string field matching
  # so $DATA_DIR is not interpreted as a regex.
  local tmp_fstab
  tmp_fstab="$(mktemp)"
  awk -v mp="$DATA_DIR" '$2 != mp { print }' /etc/fstab >"$tmp_fstab"
  printf 'UUID=%s %s xfs defaults,nofail,x-systemd.device-timeout=30 0 2\n' "$fs_uuid" "$DATA_DIR" >>"$tmp_fstab"
  install -m 0644 -o root -g root "$tmp_fstab" /etc/fstab
  rm -f "$tmp_fstab"
}

mount_data_volume() {
  # Nitro EBS volumes surface the volume ID (dashes stripped) as the NVMe
  # device serial, which is the only reliable handle for picking the right
  # device when there are multiple attached. Everything else here is
  # idempotency for the SSM-reapply case (this script runs on every deploy).
  local vol_id="$1"
  if [[ -z "$vol_id" ]]; then
    echo "install.sh: --data-volume-id is required" >&2
    exit 1
  fi

  local vol_serial
  vol_serial="$(printf '%s' "${vol_id//-/}" | tr '[:upper:]' '[:lower:]')"

  local device=""
  for _ in $(seq 1 60); do
    device="$(lsblk -d -n -o NAME,SERIAL 2>/dev/null | awk -v v="$vol_serial" '$2==v {print "/dev/"$1; exit}')"
    if [[ -n "$device" ]]; then
      break
    fi
    sleep 5
  done

  if [[ -z "$device" ]]; then
    echo "install.sh: could not find NVMe device for volume $vol_id within 5m" >&2
    lsblk -d -o NAME,SERIAL,SIZE >&2 || true
    exit 1
  fi

  echo "install.sh: data volume $vol_id => $device"

  # Canonicalize for comparison since findmnt may report a /dev/disk/by-* or
  # symlinked path even though we identified the device via /dev/nvmeXn1.
  local device_canon
  device_canon="$(readlink -f "$device")"

  if mountpoint -q "$DATA_DIR"; then
    local current_source current_source_canon
    current_source="$(findmnt -no SOURCE "$DATA_DIR")"
    current_source_canon="$(readlink -f "$current_source")"
    if [[ "$current_source_canon" != "$device_canon" ]]; then
      echo "install.sh: $DATA_DIR is mounted from $current_source, expected $device" >&2
      exit 1
    fi
    local fs_type
    fs_type="$(blkid -s TYPE -o value "$device" 2>/dev/null || true)"
    if [[ "$fs_type" != "xfs" ]]; then
      echo "install.sh: $device must be xfs for $DATA_DIR (found '${fs_type:-empty}')" >&2
      exit 1
    fi
    sync_data_fstab "$device"
    echo "install.sh: $DATA_DIR already mounted from $device; fstab refreshed"
    return 0
  fi

  if systemctl is-active --quiet ptxt-nstr.service; then
    systemctl stop ptxt-nstr.service
  fi

  local fs_type
  fs_type="$(blkid -s TYPE -o value "$device" 2>/dev/null || true)"
  if [[ -z "$fs_type" ]]; then
    echo "install.sh: formatting $device as xfs"
    mkfs.xfs -L ptxt-data "$device"
  elif [[ "$fs_type" != "xfs" ]]; then
    echo "install.sh: $device already has filesystem '$fs_type', refusing to overwrite" >&2
    exit 1
  fi

  sync_data_fstab "$device"

  mkdir -p "$DATA_DIR"
  mount "$DATA_DIR"
}

mount_data_volume "$DATA_VOLUME_ID"

install -d -m 0755 "$APP_DIR/bin" "$DATA_DIR" /etc/ptxt-nstr /etc/caddy /etc/caddy/maintenance /var/log/ptxt-nstr /var/log/caddy
install -m 0644 "$ARTIFACT_DIR/maintenance/502.html" /etc/caddy/maintenance/502.html
install -m 0644 "$ARTIFACT_DIR/maintenance/503.html" /etc/caddy/maintenance/503.html
install -m 0644 "$ARTIFACT_DIR/maintenance/ascritch_icon_black.png" /etc/caddy/maintenance/ascritch_icon_black.png
install -m 0644 "$ARTIFACT_DIR/maintenance/ascritch_icon_white.png" /etc/caddy/maintenance/ascritch_icon_white.png
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
