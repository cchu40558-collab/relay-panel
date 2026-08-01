#!/usr/bin/env bash
set -Eeuo pipefail

# Custom line-panel server installer.
# Usage:
#   bash scripts/install-server.sh
#
# Important env vars:
#   PANEL_REPO_URL       Git repository to install from. Default: Relay Panel repository.
#   PANEL_REPO_REF       Branch, tag, or commit. Default: main
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
PANEL_REPO_URL="${PANEL_REPO_URL:-https://github.com/cchu40558-collab/relay-panel.git}"
PANEL_REPO_REF="${PANEL_REPO_REF:-main}"
PANEL_UPGRADE="${PANEL_UPGRADE:-false}"
PANEL_PORT="${PANEL_PORT:-2053}"
PANEL_INSTALL_NGINX="${PANEL_INSTALL_NGINX:-true}"
PANEL_INSTALL_XRAY="${PANEL_INSTALL_XRAY:-true}"
GO_VERSION="${GO_VERSION:-1.26.5}"

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
    git -C "$SOURCE_DIR" checkout "$PANEL_REPO_REF"
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
  rm -f "$new_binary"
  chmod 0755 "${INSTALL_ROOT}/${APP_NAME}"
}

backup_existing_install() {
  is_upgrade || return
  [[ -x "${INSTALL_ROOT}/${APP_NAME}" ]] || die "Upgrade requires an existing ${INSTALL_ROOT}/${APP_NAME}"
  [[ -f "$ENV_FILE" ]] || die "Upgrade requires an existing $ENV_FILE"

  local backup_dir
  backup_dir="/var/backups/${APP_NAME}/$(date +%Y%m%d-%H%M%S)"
  log "Backing up current installation to ${backup_dir}"
  install -d -m 0700 "$backup_dir"
  cp -a "${INSTALL_ROOT}/${APP_NAME}" "$backup_dir/${APP_NAME}"
  cp -a "$ENV_FILE" "$backup_dir/environment"
  [[ -f "$SERVICE_FILE" ]] && cp -a "$SERVICE_FILE" "$backup_dir/${SERVICE_NAME}.service"
  [[ -d "$DATA_DIR" ]] && cp -a "$DATA_DIR" "$backup_dir/data"
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
  [[ "${PANEL_INSTALL_XRAY}" == "true" ]] || return
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
  print_result
}

main "$@"
