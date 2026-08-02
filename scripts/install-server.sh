#!/usr/bin/env bash
set -Eeuo pipefail

# Custom line-panel server installer.
# Usage:
#   bash scripts/install-server.sh
#
# Important env vars:
#   PANEL_REPO_URL       Git repository to install from. Default: Relay Panel repository.
#   PANEL_REPO_REF       Required immutable release tag, for example: v2.10.1
#   PANEL_UPGRADE        Set to true to preserve the installed panel settings and data.
#   PANEL_PORT           Web panel port. Default: 2053
#   PANEL_WEB_BASE_PATH  Web base path. Default: random
#   PANEL_USERNAME       Login username. Default: random
#   PANEL_PASSWORD       Login password. Default: random
#   PANEL_INSTALL_NGINX  Install nginx if missing. Default: true
#   PANEL_INSTALL_XRAY   Download Xray-core if missing. Default: true

APP_NAME="${APP_NAME:-line-panel}"
SERVICE_NAME="${SERVICE_NAME:-line-panel}"
INSTALL_ROOT="${INSTALL_ROOT:-/usr/local/line-panel}"
SOURCE_DIR="${SOURCE_DIR:-/opt/line-panel-src}"
DATA_DIR="${DATA_DIR:-/etc/line-panel}"
LOG_DIR="${LOG_DIR:-/var/log/line-panel}"
BIN_DIR="${BIN_DIR:-${INSTALL_ROOT}/bin}"
ENV_FILE="${ENV_FILE:-/etc/default/line-panel}"
RESULT_FILE="${RESULT_FILE:-${DATA_DIR}/install-result.env}"
SERVICE_FILE="${SERVICE_FILE:-/etc/systemd/system/${SERVICE_NAME}.service}"
VERSION_FILE="${VERSION_FILE:-${INSTALL_ROOT}/VERSION}"
COMMAND_PATH="${COMMAND_PATH:-/usr/local/bin/relay-panel}"
PANEL_REPO_URL="${PANEL_REPO_URL:-https://github.com/cchu40558-collab/relay-panel.git}"
PANEL_REPO_REF="${PANEL_REPO_REF:-}"
PANEL_UPGRADE="${PANEL_UPGRADE:-false}"
PANEL_PORT="${PANEL_PORT:-2053}"
PANEL_INSTALL_NGINX="${PANEL_INSTALL_NGINX:-true}"
PANEL_INSTALL_XRAY="${PANEL_INSTALL_XRAY:-true}"
GO_VERSION="${GO_VERSION:-1.26.5}"
BACKUP_ROOT="${BACKUP_ROOT:-/var/backups/${APP_NAME}}"
BACKUP_KEEP="${BACKUP_KEEP:-2}"
UPGRADE_BACKUP_DIR=""
UPGRADE_COMPLETE=false

need_root() {
  if [[ "${EUID}" -ne 0 ]]; then
    echo "Please run as root." >&2
    exit 1
  fi
}

log() {
  printf '\n==> %s\n' "$*"
}

die() {
  echo "ERROR: $*" >&2
  exit 1
}

is_upgrade() {
  [[ "${PANEL_UPGRADE}" == "true" ]]
}

validate_release_ref() {
  [[ "${PANEL_REPO_REF}" =~ ^v[0-9]+\.[0-9]+\.[0-9]+([-.][0-9A-Za-z.-]+)?$ ]] || die "PANEL_REPO_REF must be a release tag such as v2.10.1"
}

rand_text() {
  local n="${1:-16}"
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -base64 48 | tr -dc 'A-Za-z0-9' | head -c "$n"
  else
    tr -dc 'A-Za-z0-9' </dev/urandom | head -c "$n"
  fi
}

detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64) echo "amd64" ;;
    aarch64|arm64) echo "arm64" ;;
    *) die "Unsupported CPU architecture: $(uname -m)" ;;
  esac
}

install_packages() {
  log "Installing base packages"
  local common=(ca-certificates curl wget git tar unzip xz-utils openssl)
  local build=(gcc g++ make)
  local nginx=()
  if [[ "${PANEL_INSTALL_NGINX}" == "true" ]]; then
    nginx=(nginx)
  fi

  if command -v apt-get >/dev/null 2>&1; then
    apt-get update
    DEBIAN_FRONTEND=noninteractive apt-get install -y "${common[@]}" "${build[@]}" "${nginx[@]}"
  elif command -v dnf >/dev/null 2>&1; then
    dnf install -y "${common[@]}" "${build[@]}" "${nginx[@]}"
  elif command -v yum >/dev/null 2>&1; then
    yum install -y "${common[@]}" "${build[@]}" "${nginx[@]}"
  else
    die "Unsupported Linux package manager. Install dependencies manually first."
  fi
}

ensure_go() {
  local arch
  arch="$(detect_arch)"
  if command -v go >/dev/null 2>&1 && go version | grep -q "go${GO_VERSION}"; then
    log "Go ${GO_VERSION} already installed"
    return
  fi

  log "Installing Go ${GO_VERSION}"
  local url="https://go.dev/dl/go${GO_VERSION}.linux-${arch}.tar.gz"
  local tmp="/tmp/go${GO_VERSION}.linux-${arch}.tar.gz"
  curl -fL "$url" -o "$tmp"
  rm -rf /usr/local/go
  tar -C /usr/local -xzf "$tmp"
  ln -sf /usr/local/go/bin/go /usr/local/bin/go
  ln -sf /usr/local/go/bin/gofmt /usr/local/bin/gofmt
}

ensure_node() {
  local major=""
  if command -v node >/dev/null 2>&1; then
    major="$(node -p 'process.versions.node.split(".")[0]' 2>/dev/null || true)"
  fi
  if [[ "${major}" =~ ^[0-9]+$ ]] && (( major >= 24 )) && command -v npm >/dev/null 2>&1; then
    log "Node $(node -v) already installed"
    return
  fi

  log "Installing latest Node 24"
  local arch node_arch base sums file node_version tmp install_dir
  arch="$(detect_arch)"
  case "$arch" in
    amd64) node_arch="x64" ;;
    arm64) node_arch="arm64" ;;
  esac
  base="https://nodejs.org/dist/latest-v24.x"
  sums="$(curl -fsSL "${base}/SHASUMS256.txt")"
  file="$(printf '%s\n' "$sums" | awk -v a="linux-${node_arch}.tar.xz" '$2 ~ a {print $2; exit}')"
  [[ -n "$file" ]] || die "Could not find latest Node 24 tarball for ${node_arch}"
  node_version="${file#node-}"
  node_version="${node_version%-linux-${node_arch}.tar.xz}"
  tmp="/tmp/${file}"
  install_dir="/usr/local/lib/nodejs"
  curl -fL "${base}/${file}" -o "$tmp"
  rm -rf "$install_dir"
  mkdir -p "$install_dir"
  tar -C "$install_dir" --strip-components=1 -xJf "$tmp"
  ln -sf "${install_dir}/bin/node" /usr/local/bin/node
  ln -sf "${install_dir}/bin/npm" /usr/local/bin/npm
  ln -sf "${install_dir}/bin/npx" /usr/local/bin/npx
  log "Installed Node ${node_version}"
}

checkout_source() {
  log "Preparing source"
  if [[ -d "${SOURCE_DIR}/.git" ]]; then
    git -C "$SOURCE_DIR" fetch --all --tags
    if ! git -C "$SOURCE_DIR" checkout "$PANEL_REPO_REF"; then
      log "Cleaning generated source files before retrying checkout"
      git -C "$SOURCE_DIR" restore --source=HEAD -- frontend/public/openapi.json 2>/dev/null || \
        git -C "$SOURCE_DIR" checkout -- frontend/public/openapi.json 2>/dev/null || true
      git -C "$SOURCE_DIR" checkout "$PANEL_REPO_REF"
    fi
    git -C "$SOURCE_DIR" pull --ff-only || true
    return
  fi

  [[ -n "${PANEL_REPO_URL:-}" ]] || die "PANEL_REPO_URL is required on first install"
  rm -rf "$SOURCE_DIR"
  git clone "$PANEL_REPO_URL" "$SOURCE_DIR"
  git -C "$SOURCE_DIR" checkout "$PANEL_REPO_REF"
}

build_panel() {
  log "Building frontend"
  cd "${SOURCE_DIR}/frontend"
  npm ci
  npm run build

  log "Building backend"
  cd "$SOURCE_DIR"
  export CGO_ENABLED=1
  local new_binary="${INSTALL_ROOT}/.${APP_NAME}.new"
  rm -f "$new_binary"
  go build -trimpath -ldflags="-s -w" -o "$new_binary" .
  install -m 0755 "$new_binary" "${INSTALL_ROOT}/${APP_NAME}"
  install -m 0644 "${SOURCE_DIR}/internal/config/version" "$VERSION_FILE"
  rm -f "$new_binary"
  chmod 0755 "${INSTALL_ROOT}/${APP_NAME}"
}

backup_existing_install() {
  is_upgrade || return 0
  [[ -x "${INSTALL_ROOT}/${APP_NAME}" ]] || die "Upgrade requires an existing ${INSTALL_ROOT}/${APP_NAME}"
  [[ -f "$ENV_FILE" ]] || die "Upgrade requires an existing $ENV_FILE"

  local backup_dir
  backup_dir="${BACKUP_ROOT}/$(date +%Y%m%d-%H%M%S)"
  log "Backing up current installation to ${backup_dir}"
  install -d -m 0700 "$backup_dir"
  cp -a "${INSTALL_ROOT}/${APP_NAME}" "$backup_dir/${APP_NAME}"
  cp -a "$ENV_FILE" "$backup_dir/environment"
  [[ -f "$SERVICE_FILE" ]] && cp -a "$SERVICE_FILE" "$backup_dir/${SERVICE_NAME}.service"
  [[ -d "$DATA_DIR" ]] && cp -a "$DATA_DIR" "$backup_dir/data"
  [[ -f "$VERSION_FILE" ]] && cp -a "$VERSION_FILE" "$backup_dir/VERSION"
  UPGRADE_BACKUP_DIR="$backup_dir"
}

restore_failed_upgrade() {
  [[ -n "$UPGRADE_BACKUP_DIR" && -d "$UPGRADE_BACKUP_DIR" ]] || return 0

  log "Upgrade failed; restoring ${UPGRADE_BACKUP_DIR}"
  trap - ERR
  set +e
  systemctl stop "$SERVICE_NAME"
  cp -a "$UPGRADE_BACKUP_DIR/${APP_NAME}" "${INSTALL_ROOT}/${APP_NAME}"
  cp -a "$UPGRADE_BACKUP_DIR/environment" "$ENV_FILE"
  [[ -f "$UPGRADE_BACKUP_DIR/${SERVICE_NAME}.service" ]] && cp -a "$UPGRADE_BACKUP_DIR/${SERVICE_NAME}.service" "$SERVICE_FILE"
  [[ -f "$UPGRADE_BACKUP_DIR/VERSION" ]] && cp -a "$UPGRADE_BACKUP_DIR/VERSION" "$VERSION_FILE"
  if [[ -d "$UPGRADE_BACKUP_DIR/data" ]]; then
    rm -rf "$DATA_DIR"
    cp -a "$UPGRADE_BACKUP_DIR/data" "$DATA_DIR"
  fi
  systemctl daemon-reload
  systemctl start "$SERVICE_NAME"
}

prune_old_backups() {
  is_upgrade || return 0
  [[ "$BACKUP_KEEP" =~ ^[0-9]+$ ]] || die "BACKUP_KEEP must be a non-negative integer"
  [[ -d "$BACKUP_ROOT" ]] || return 0

  local backup_dirs=()
  mapfile -t backup_dirs < <(find "$BACKUP_ROOT" -mindepth 1 -maxdepth 1 -type d -printf '%f\n' | sort -r)
  local index
  for ((index = BACKUP_KEEP; index < ${#backup_dirs[@]}; index++)); do
    rm -rf "${BACKUP_ROOT}/${backup_dirs[$index]}"
  done
}

write_env() {
  if is_upgrade; then
    log "Preserving existing panel environment, account, and web path"
    return
  fi

  log "Writing environment"
  mkdir -p "$INSTALL_ROOT" "$DATA_DIR" "$LOG_DIR" "$BIN_DIR"
  chmod 0755 "$INSTALL_ROOT" "$LOG_DIR" "$BIN_DIR"
  chmod 0700 "$DATA_DIR"

  local web_base username password
  web_base="${PANEL_WEB_BASE_PATH:-/$(rand_text 22)}"
  username="${PANEL_USERNAME:-u$(rand_text 11)}"
  password="${PANEL_PASSWORD:-$(rand_text 18)}"

  cat > "$ENV_FILE" <<EOF
XUI_DB_FOLDER=${DATA_DIR}
XUI_LOG_FOLDER=${LOG_DIR}
XUI_BIN_FOLDER=${BIN_DIR}
XUI_PORT=${PANEL_PORT}
XUI_INIT_WEB_BASE_PATH=${web_base}
XUI_SKIP_HSTS=true
XUI_ENABLE_FAIL2BAN=false
XRAY_VMESS_AEAD_FORCED=false
EOF
  chmod 0600 "$ENV_FILE"

  set -a
  # shellcheck disable=SC1090
  . "$ENV_FILE"
  set +a

  "${INSTALL_ROOT}/${APP_NAME}" setting \
    -username "$username" \
    -password "$password" \
    -port "$PANEL_PORT" \
    -webBasePath "$web_base"

  cat > "$RESULT_FILE" <<EOF
PANEL_URL=http://$(hostname -I | awk '{print $1}'):${PANEL_PORT}${web_base}
PANEL_PORT=${PANEL_PORT}
PANEL_WEB_BASE_PATH=${web_base}
PANEL_USERNAME=${username}
PANEL_PASSWORD=${password}
SERVICE_NAME=${SERVICE_NAME}
INSTALL_ROOT=${INSTALL_ROOT}
DATA_DIR=${DATA_DIR}
LOG_DIR=${LOG_DIR}
BIN_DIR=${BIN_DIR}
EOF
  chmod 0600 "$RESULT_FILE"
}

install_xray() {
  [[ "${PANEL_INSTALL_XRAY}" == "true" ]] || return 0
  if [[ -x "${BIN_DIR}/xray-linux-$(detect_arch)" || -x "${BIN_DIR}/xray" ]]; then
    log "Xray binary already exists"
    return
  fi

  log "Installing Xray-core"
  local arch asset tmp
  arch="$(detect_arch)"
  case "$arch" in
    amd64) asset="Xray-linux-64.zip" ;;
    arm64) asset="Xray-linux-arm64-v8a.zip" ;;
  esac
  tmp="/tmp/${asset}"
  curl -fL "https://github.com/XTLS/Xray-core/releases/latest/download/${asset}" -o "$tmp"
  rm -rf /tmp/xray-core
  mkdir -p /tmp/xray-core "$BIN_DIR"
  unzip -o "$tmp" -d /tmp/xray-core >/dev/null
  install -m 0755 /tmp/xray-core/xray "${BIN_DIR}/xray-linux-${arch}"
  [[ -f /tmp/xray-core/geoip.dat ]] && install -m 0644 /tmp/xray-core/geoip.dat "${BIN_DIR}/geoip.dat"
  [[ -f /tmp/xray-core/geosite.dat ]] && install -m 0644 /tmp/xray-core/geosite.dat "${BIN_DIR}/geosite.dat"
}

write_service() {
  log "Writing systemd service"
  cat > "$SERVICE_FILE" <<EOF
[Unit]
Description=Line Panel Service
After=network.target
Wants=network.target

[Service]
Type=simple
EnvironmentFile=-${ENV_FILE}
WorkingDirectory=${INSTALL_ROOT}
ExecStart=${INSTALL_ROOT}/${APP_NAME} run
Restart=on-failure
RestartSec=5s

[Install]
WantedBy=multi-user.target
EOF
  systemctl daemon-reload
  systemctl enable "$SERVICE_NAME"
  if is_upgrade; then
    systemctl restart "$SERVICE_NAME"
  else
    systemctl start "$SERVICE_NAME"
  fi
  systemctl is-active --quiet "$SERVICE_NAME"
}

write_command_wrapper() {
  log "Installing relay-panel command"
  cat > "$COMMAND_PATH" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail

SERVICE_NAME="line-panel"
INSTALL_ROOT="/usr/local/line-panel"
VERSION_FILE="${INSTALL_ROOT}/VERSION"
DATA_DIR="/etc/line-panel"
ENV_FILE="/etc/default/line-panel"
SERVICE_FILE="/etc/systemd/system/${SERVICE_NAME}.service"
BACKUP_ROOT="/var/backups/line-panel"
APP_NAME="line-panel"
REPO_URL="https://github.com/cchu40558-collab/relay-panel.git"

require_root() {
  if [[ "${EUID}" -ne 0 ]]; then
    echo "Please run this command as root." >&2
    exit 1
  fi
}

print_version() {
  if [[ -r "$VERSION_FILE" ]]; then
    printf 'Relay Panel v%s\n' "$(tr -d '[:space:]' < "$VERSION_FILE")"
  else
    echo "Relay Panel version unknown (missing $VERSION_FILE)"
  fi
}

validate_release_tag() {
  [[ "${1:-}" =~ ^v[0-9]+\.[0-9]+\.[0-9]+([-.][0-9A-Za-z.-]+)?$ ]] || {
    echo "Usage: relay-panel update vX.Y.Z" >&2
    exit 1
  }
}

latest_backup() {
  [[ -d "$BACKUP_ROOT" ]] || return 1
  find "$BACKUP_ROOT" -mindepth 1 -maxdepth 1 -type d -printf '%p\n' | sort -r | head -n 1
}

backup_current_install() {
  local backup_dir="$1"
  install -d -m 0700 "$backup_dir"
  cp -a "${INSTALL_ROOT}/${APP_NAME}" "$backup_dir/${APP_NAME}"
  cp -a "$ENV_FILE" "$backup_dir/environment"
  [[ -f "$SERVICE_FILE" ]] && cp -a "$SERVICE_FILE" "$backup_dir/${SERVICE_NAME}.service"
  [[ -f "$VERSION_FILE" ]] && cp -a "$VERSION_FILE" "$backup_dir/VERSION"
  [[ -d "$DATA_DIR" ]] && cp -a "$DATA_DIR" "$backup_dir/data"
}

restore_backup() {
  local backup_dir="$1"
  [[ -x "$backup_dir/${APP_NAME}" && -f "$backup_dir/environment" ]] || {
    echo "Backup is incomplete: $backup_dir" >&2
    return 1
  }

  systemctl stop "$SERVICE_NAME"
  cp -a "$backup_dir/${APP_NAME}" "${INSTALL_ROOT}/${APP_NAME}"
  cp -a "$backup_dir/environment" "$ENV_FILE"
  [[ -f "$backup_dir/${SERVICE_NAME}.service" ]] && cp -a "$backup_dir/${SERVICE_NAME}.service" "$SERVICE_FILE"
  [[ -f "$backup_dir/VERSION" ]] && cp -a "$backup_dir/VERSION" "$VERSION_FILE"
  if [[ -d "$backup_dir/data" ]]; then
    rm -rf "$DATA_DIR"
    cp -a "$backup_dir/data" "$DATA_DIR"
  fi
  systemctl daemon-reload
  systemctl start "$SERVICE_NAME"
  systemctl is-active --quiet "$SERVICE_NAME"
}

prune_old_backups() {
  local backup_dirs=()
  mapfile -t backup_dirs < <(find "$BACKUP_ROOT" -mindepth 1 -maxdepth 1 -type d -printf '%f\n' | sort -r)
  local index
  for ((index = 2; index < ${#backup_dirs[@]}; index++)); do
    rm -rf "${BACKUP_ROOT}/${backup_dirs[$index]}"
  done
}

case "${1:-help}" in
  version)
    print_version
    ;;
  status)
    print_version
    systemctl status "$SERVICE_NAME" --no-pager
    ;;
  logs)
    journalctl -u "$SERVICE_NAME" -n 100 --no-pager
    ;;
  check)
    print_version
    systemctl is-active "$SERVICE_NAME"
    nginx -t
    ss -lntp | grep -E ':(443|8443|2053|2096) '
    ;;
  restart)
    require_root
    systemctl restart "$SERVICE_NAME"
    systemctl is-active "$SERVICE_NAME"
    ;;
  update)
    require_root
    target="${2:-}"
    if [[ -z "$target" ]]; then
      print_version
      echo "Specify a release version, for example: relay-panel update v2.10.1"
      exit 0
    fi
    validate_release_tag "$target"
    git ls-remote --exit-code --refs "$REPO_URL" "refs/tags/${target}" >/dev/null
    installer_url="https://raw.githubusercontent.com/cchu40558-collab/relay-panel/${target}/scripts/install-server.sh"
    PANEL_UPGRADE=true PANEL_REPO_REF="$target" bash <(curl -fsSL "$installer_url")
    ;;
  rollback)
    require_root
    target="$(latest_backup)" || {
      echo "No backup is available." >&2
      exit 1
    }
    current_backup="${BACKUP_ROOT}/$(date +%Y%m%d-%H%M%S)"
    backup_current_install "$current_backup"
    if ! restore_backup "$target"; then
      restore_backup "$current_backup" || true
      echo "Rollback failed; restored the version that was running before rollback." >&2
      exit 1
    fi
    rm -rf "$target"
    prune_old_backups
    print_version
    ;;
  help|-h|--help)
    cat <<'USAGE'
Usage: relay-panel <command>

Commands:
  version  Show the deployed panel version
  status   Show panel service status
  logs     Show the latest 100 panel log lines
  check    Check panel, Nginx, and listening ports
  restart  Restart the panel service
  update <version>  Upgrade to an explicit release tag, for example v2.10.1
  rollback  Restore the most recent previous-version backup
USAGE
    ;;
  *)
    echo "Unknown command: $1" >&2
    exit 1
    ;;
esac
EOF
  chmod 0755 "$COMMAND_PATH"
}

print_result() {
  log "Install complete"
  cat "$RESULT_FILE"
  echo
  echo "Useful commands:"
  echo "  systemctl status ${SERVICE_NAME} --no-pager"
  echo "  journalctl -u ${SERVICE_NAME} -f"
  echo "  cat ${RESULT_FILE}"
}

main() {
  need_root
  validate_release_ref
  if is_upgrade; then
    trap 'status=$?; if [[ "$UPGRADE_COMPLETE" != true ]]; then restore_failed_upgrade; fi; exit "$status"' ERR
  fi
  install_packages
  ensure_go
  ensure_node
  mkdir -p "$INSTALL_ROOT"
  checkout_source
  backup_existing_install
  build_panel
  write_env
  install_xray
  write_service
  write_command_wrapper
  UPGRADE_COMPLETE=true
  prune_old_backups
  print_result
}

main "$@"
