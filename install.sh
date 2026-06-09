#!/usr/bin/env bash
# install.sh — Flux.io Linux installer
# Usage: sudo ./install.sh
# Supports: Ubuntu/Debian, RHEL/CentOS/Rocky/AlmaLinux, Fedora, Arch/Manjaro
set -euo pipefail

# ─── Constants ────────────────────────────────────────────────────────────────
GO_VERSION="1.22.4"
NODE_VERSION="20"
REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SYSTEMD_UNIT="/etc/systemd/system/fluxio.service"

# ─── Colours ──────────────────────────────────────────────────────────────────
RED='\033[0;31m'
YELLOW='\033[1;33m'
GREEN='\033[0;32m'
CYAN='\033[0;36m'
BOLD='\033[1m'
RESET='\033[0m'

info()    { echo -e "${CYAN}  →${RESET} $*"; }
success() { echo -e "${GREEN}  ✅${RESET} $*"; }
warn()    { echo -e "${YELLOW}  ⚠️ ${RESET}  $*"; }
error()   { echo -e "${RED}  ✗${RESET} $*" >&2; }
header()  { echo -e "\n${BOLD}${CYAN}=== $* ===${RESET}\n"; }

# ─── Privilege check ──────────────────────────────────────────────────────────
check_root() {
    if [[ $EUID -ne 0 ]]; then
        warn "Not running as root. Re-executing with sudo..."
        exec sudo bash "$0" "$@"
    fi
}

# ─── Distro detection ─────────────────────────────────────────────────────────
PKG_FAMILY=""
PKG_MGR=""

detect_distro() {
    if [[ ! -f /etc/os-release ]]; then
        error "Cannot detect Linux distribution: /etc/os-release not found."
        exit 1
    fi
    # shellcheck source=/dev/null
    source /etc/os-release
    local id="${ID:-}"
    local id_like="${ID_LIKE:-}"
    local combined="${id} ${id_like}"

    case "$combined" in
        *ubuntu*|*debian*|*mint*)
            PKG_FAMILY="debian"
            PKG_MGR="apt-get"
            ;;
        *rhel*|*centos*|*rocky*|*alma*)
            PKG_FAMILY="rhel"
            PKG_MGR="$(command -v dnf 2>/dev/null || echo yum)"
            ;;
        *fedora*)
            PKG_FAMILY="fedora"
            PKG_MGR="dnf"
            ;;
        *arch*|*manjaro*)
            PKG_FAMILY="arch"
            PKG_MGR="pacman"
            ;;
        *)
            error "Unsupported distribution: '${id}'. Supported: Ubuntu/Debian, RHEL/CentOS/Rocky/AlmaLinux, Fedora, Arch/Manjaro."
            exit 1
            ;;
    esac
    success "Detected distro: ${id} (family: ${PKG_FAMILY}, package manager: ${PKG_MGR})"
}

# ─── Docker installation ──────────────────────────────────────────────────────
install_docker() {
    if command -v docker &>/dev/null; then
        success "Docker already installed: $(docker --version)"
        return
    fi
    info "Installing Docker via official script (get.docker.com)..."
    curl -fsSL https://get.docker.com | sh
    if [[ -n "${SUDO_USER:-}" ]]; then
        usermod -aG docker "$SUDO_USER" || true
        info "Added ${SUDO_USER} to the 'docker' group. Log out and back in to use Docker without sudo."
    fi
    success "Docker installed: $(docker --version)"
}

ensure_docker_running() {
    if ! systemctl is-active --quiet docker 2>/dev/null; then
        info "Starting Docker daemon..."
        systemctl start docker || { error "Failed to start Docker daemon. Check: systemctl status docker"; exit 1; }
    fi

    if docker compose version &>/dev/null 2>&1; then
        success "Docker Compose v2 available: $(docker compose version --short 2>/dev/null || docker compose version)"
    elif command -v docker-compose &>/dev/null; then
        warn "Using docker-compose v1 (standalone). docker compose v2 plugin is preferred."
    else
        info "Installing Docker Compose plugin..."
        case "$PKG_FAMILY" in
            debian)   apt-get install -y docker-compose-plugin ;;
            rhel|fedora) "$PKG_MGR" install -y docker-compose-plugin ;;
            arch)     pacman -S --noconfirm docker-compose ;;
        esac
        success "Docker Compose plugin installed."
    fi
}

# ─── Mode selection ───────────────────────────────────────────────────────────
INSTALL_MODE=""

select_mode() {
    header "Installation Mode"
    echo "  [1] Production  — runs in background, auto-starts on boot, optional GeoIP"
    echo "  [2] Development — installs Go + Node on host, runs containers in foreground"
    echo
    read -rp "Choice [1]: " choice
    choice="${choice:-1}"
    case "$choice" in
        1) INSTALL_MODE="production"  ;;
        2) INSTALL_MODE="development" ;;
        *) warn "Invalid choice '${choice}'. Defaulting to production."; INSTALL_MODE="production" ;;
    esac
    success "Mode selected: ${INSTALL_MODE}"
}

# ─── .env helpers ─────────────────────────────────────────────────────────────
_env_val() {
    grep -E "^$1=" "${REPO_DIR}/.env" 2>/dev/null | cut -d= -f2- | tr -d '"' || true
}

_set_env() {
    local key="$1" val="$2"
    sed -i "s|^${key}=.*|${key}=${val}|" "${REPO_DIR}/.env"
}

# ─── Configuration wizard ─────────────────────────────────────────────────────
run_wizard() {
    header "Flux.io Configuration"

    if [[ -f "${REPO_DIR}/.env" ]]; then
        echo ".env already exists."
        read -rp "  [K]eep existing / [O]verwrite / [E]dit [K]: " env_choice
        env_choice="${env_choice:-K}"
        case "${env_choice^^}" in
            K) info "Keeping existing .env."; return ;;
            O) info "Overwriting .env." ;;
            E) "${EDITOR:-nano}" "${REPO_DIR}/.env"; return ;;
            *) info "Keeping existing .env."; return ;;
        esac
    fi

    local wazuh_ip wazuh_port pg_pass netflow_port tzsp_port http_port

    read -rp "Wazuh Manager IP (blank to disable) []: " wazuh_ip
    wazuh_ip="${wazuh_ip:-}"

    read -rp "Wazuh Manager Port [1514]: " wazuh_port
    wazuh_port="${wazuh_port:-1514}"

    read -rp "Postgres password [fluxio_password]: " pg_pass
    pg_pass="${pg_pass:-fluxio_password}"

    read -rp "NetFlow UDP port [2055]: " netflow_port
    netflow_port="${netflow_port:-2055}"

    read -rp "TZSP UDP port [37008]: " tzsp_port
    tzsp_port="${tzsp_port:-37008}"

    read -rp "Backend HTTP port [80]: " http_port
    http_port="${http_port:-80}"

    cat > "${REPO_DIR}/.env" <<EOF
WAZUH_MANAGER_IP=${wazuh_ip}
WAZUH_MANAGER_PORT=${wazuh_port}
POSTGRES_PASSWORD=${pg_pass}
POSTGRES_PORT=5432
POSTGRES_DSN=postgres://fluxio:${pg_pass}@postgres:5432/fluxioclient?sslmode=disable
CLICKHOUSE_DSN=clickhouse://default:@clickhouse:9000/fluxio
NETFLOW_PORT=${netflow_port}
TZSP_PORT=${tzsp_port}
PORT=${http_port}
SURICATA_EVE_LOG_PATH=/var/log/suricata/eve.json
GEOIP_CITY_DB=/root/geoip/GeoLite2-City.mmdb
GEOIP_ASN_DB=/root/geoip/GeoLite2-ASN.mmdb
EOF
    success ".env written to ${REPO_DIR}/.env"
}

# ─── Port conflict checker ────────────────────────────────────────────────────
_port_in_use_tcp() {
    local port="$1"
    # Anchor on whitespace or EOL so :80 doesn't false-match :8080
    if command -v ss &>/dev/null; then
        ss -H -tlnp 2>/dev/null | grep -qE "[[:space:]]:${port}([[:space:]]|$)" && return 0
    fi
    if command -v netstat &>/dev/null; then
        netstat -tlnp 2>/dev/null | grep -qE "[[:space:]]:${port}([[:space:]]|$)" && return 0
    fi
    return 1
}

_port_in_use_udp() {
    local port="$1"
    if command -v ss &>/dev/null; then
        ss -H -ulnp 2>/dev/null | grep -qE "[[:space:]]:${port}([[:space:]]|$)" && return 0
    fi
    if command -v netstat &>/dev/null; then
        netstat -ulnp 2>/dev/null | grep -qE "[[:space:]]:${port}([[:space:]]|$)" && return 0
    fi
    return 1
}

_check_tcp() {
    local port="$1" label="$2" env_key="$3"
    if _port_in_use_tcp "$port"; then
        warn "Port ${port}/tcp (${label}) is IN USE."
        read -rp "     Alternative port (Enter to keep ${port} anyway): " alt
        if [[ -n "$alt" ]]; then
            _set_env "$env_key" "$alt"
            echo -e "  ${GREEN}✅${RESET}  ${alt}/tcp  (${label}) — free  [changed from ${port}]"
        else
            warn "Keeping ${port}/tcp — this may cause startup failures."
        fi
    else
        echo -e "  ${GREEN}✅${RESET}  ${port}/tcp  (${label}) — free"
    fi
}

_check_udp() {
    local port="$1" label="$2" env_key="$3"
    if _port_in_use_udp "$port"; then
        warn "Port ${port}/udp (${label}) is IN USE."
        read -rp "     Alternative port (Enter to keep ${port} anyway): " alt
        if [[ -n "$alt" ]]; then
            _set_env "$env_key" "$alt"
            echo -e "  ${GREEN}✅${RESET}  ${alt}/udp  (${label}) — free  [changed from ${port}]"
        else
            warn "Keeping ${port}/udp — this may cause startup failures."
        fi
    else
        echo -e "  ${GREEN}✅${RESET}  ${port}/udp  (${label}) — free"
    fi
}

check_all_ports() {
    header "Port Conflict Check"

    local http_port pg_port nf_port tzsp_port
    http_port="$(_env_val PORT)"
    pg_port="$(_env_val POSTGRES_PORT)"
    nf_port="$(_env_val NETFLOW_PORT)"
    tzsp_port="$(_env_val TZSP_PORT)"

    # ClickHouse ports are fixed — warn only, not user-configurable
    for fixed_port in 8123 9000; do
        if _port_in_use_tcp "$fixed_port"; then
            warn "Port ${fixed_port}/tcp (ClickHouse) is IN USE. Stop the conflicting service before starting Flux.io."
        else
            echo -e "  ${GREEN}✅${RESET}  ${fixed_port}/tcp  (ClickHouse) — free"
        fi
    done

    _check_tcp "$http_port"  "HTTP backend"  "PORT"
    _check_tcp "$pg_port"    "Postgres"      "POSTGRES_PORT"
    _check_udp "$nf_port"    "NetFlow"       "NETFLOW_PORT"
    _check_udp "$tzsp_port"  "TZSP"          "TZSP_PORT"
}

# ─── Dev toolchain ────────────────────────────────────────────────────────────
install_go() {
    if command -v go &>/dev/null && go version 2>/dev/null | grep -qE "go1\.22\.[0-9]+"; then
        success "Go 1.22 already installed: $(go version)"
        return
    fi

    info "Installing Go ${GO_VERSION}..."
    local arch
    arch="$(uname -m)"
    case "$arch" in
        x86_64)  arch="amd64" ;;
        aarch64) arch="arm64" ;;
        *)
            error "Unsupported CPU architecture for Go install: ${arch}"
            exit 1
            ;;
    esac

    local tarball="go${GO_VERSION}.linux-${arch}.tar.gz"
    info "Downloading https://go.dev/dl/${tarball} ..."
    curl -fsSL "https://go.dev/dl/${tarball}" -o "/tmp/${tarball}"

    info "Extracting to /usr/local/go ..."
    rm -rf /usr/local/go
    tar -C /usr/local -xzf "/tmp/${tarball}"
    rm "/tmp/${tarball}"

    export PATH="/usr/local/go/bin:$PATH"
    echo 'export PATH="/usr/local/go/bin:$PATH"' > /etc/profile.d/go.sh
    chmod +x /etc/profile.d/go.sh

    success "Go installed: $(go version)"
}

install_node() {
    if command -v node &>/dev/null && node --version 2>/dev/null | grep -qE "^v${NODE_VERSION}\."; then
        success "Node ${NODE_VERSION} already installed: $(node --version)"
        return
    fi

    info "Installing Node.js ${NODE_VERSION}..."
    case "$PKG_FAMILY" in
        arch)
            pacman -S --noconfirm nodejs npm
            ;;
        debian)
            curl -fsSL "https://deb.nodesource.com/setup_${NODE_VERSION}.x" | bash -
            apt-get install -y nodejs
            ;;
        rhel|fedora)
            curl -fsSL "https://rpm.nodesource.com/setup_${NODE_VERSION}.x" | bash -
            "$PKG_MGR" install -y nodejs
            ;;
    esac
    success "Node installed: $(node --version)"
}

# ─── GeoIP download (production, optional) ───────────────────────────────────
download_geoip() {
    header "GeoIP Enrichment (optional)"
    read -rp "Download MaxMind GeoLite2 databases for GeoIP/ASN enrichment? [y/N]: " geoip_choice
    geoip_choice="${geoip_choice:-N}"
    if [[ "${geoip_choice^^}" != "Y" ]]; then
        warn "GeoIP skipped. Add GeoLite2-City.mmdb and GeoLite2-ASN.mmdb to backend/geoip/ to enable enrichment."
        return
    fi

    read -rsp "MaxMind license key: " maxmind_key
    echo
    if [[ -z "$maxmind_key" ]]; then
        warn "No license key provided. Skipping GeoIP download."
        return
    fi

    mkdir -p "${REPO_DIR}/backend/geoip"

    local base_url="https://download.maxmind.com/app/geoip_download"
    for db in GeoLite2-City GeoLite2-ASN; do
        info "Downloading ${db}.mmdb ..."
        local tgz="/tmp/${db}.tar.gz"
        if curl -fsSL \
            "${base_url}?edition_id=${db}&license_key=${maxmind_key}&suffix=tar.gz" \
            -o "$tgz"; then
            local mmdb_path
            mmdb_path="$(tar -tzf "$tgz" | grep '\.mmdb$' | head -1)"
            tar -xzf "$tgz" -C /tmp "${mmdb_path}"
            mv "/tmp/${mmdb_path}" "${REPO_DIR}/backend/geoip/${db}.mmdb"
            rm -rf "$tgz" "/tmp/$(dirname "$mmdb_path")"
            success "${db}.mmdb saved to backend/geoip/"
        else
            warn "Failed to download ${db}.mmdb. GeoIP enrichment will be disabled for this database."
        fi
    done
}

# ─── systemd service (production) ────────────────────────────────────────────
install_systemd_service() {
    local compose_cmd
    if docker compose version &>/dev/null 2>&1; then
        compose_cmd="/usr/bin/docker compose"
    else
        compose_cmd="/usr/local/bin/docker-compose"
    fi

    info "Writing ${SYSTEMD_UNIT} ..."
    cat > "${SYSTEMD_UNIT}" <<EOF
[Unit]
Description=Flux.io Network Monitoring Platform
After=docker.service
Requires=docker.service

[Service]
Type=simple
WorkingDirectory=${REPO_DIR}
ExecStart=${compose_cmd} up
ExecStop=${compose_cmd} down
Restart=on-failure
RestartSec=10

[Install]
WantedBy=multi-user.target
EOF

    systemctl daemon-reload
    systemctl enable --now fluxio
    success "fluxio.service installed and enabled. Check status: systemctl status fluxio"
}

# ─── Health check ────────────────────────────────────────────────────────────
wait_for_healthy() {
    local http_port
    http_port="$(_env_val PORT)"
    local url="http://127.0.0.1:${http_port}/api/health"
    info "Waiting for Flux.io to be ready at ${url} ..."
    local attempts=0
    while (( attempts < 30 )); do
        if curl -fsS "$url" &>/dev/null; then
            success "Flux.io is healthy."
            return
        fi
        (( attempts++ ))
        sleep 2
    done
    warn "Flux.io did not respond at ${url} within 60 seconds."
    warn "Check container logs: docker compose logs --tail=30"
}

# ─── Summary screen ───────────────────────────────────────────────────────────
print_summary() {
    local host_ip
    host_ip="$(hostname -I 2>/dev/null | awk '{print $1}')"
    host_ip="${host_ip:-localhost}"
    local http_port nf_port tzsp_port wazuh_ip
    http_port="$(_env_val PORT)"
    nf_port="$(_env_val NETFLOW_PORT)"
    tzsp_port="$(_env_val TZSP_PORT)"
    wazuh_ip="$(_env_val WAZUH_MANAGER_IP)"

    echo
    echo -e "${GREEN}${BOLD}╔══════════════════════════════════════╗${RESET}"
    echo -e "${GREEN}${BOLD}║   ✅  Flux.io is running!            ║${RESET}"
    echo -e "${GREEN}${BOLD}╚══════════════════════════════════════╝${RESET}"
    echo
    echo -e "  Dashboard : ${CYAN}http://${host_ip}:${http_port}${RESET}"
    echo -e "  API health: ${CYAN}http://${host_ip}:${http_port}/api/health${RESET}"
    echo -e "  NetFlow   : UDP ${host_ip}:${nf_port}"
    echo -e "  TZSP      : UDP ${host_ip}:${tzsp_port}"
    echo
    echo "Next steps:"
    echo "  - Point your NetFlow exporter to ${host_ip}:${nf_port}"
    echo "  - Open the dashboard and configure DPI mode under Settings"
    if [[ -z "$wazuh_ip" ]]; then
        echo "  - Wazuh forwarding: set WAZUH_MANAGER_IP in .env and run 'docker compose restart backend'"
    fi
    if [[ "$INSTALL_MODE" == "development" ]]; then
        echo
        echo "Dev environment ready:"
        echo "  Backend tests : cd backend && go test ./... -short"
        echo "  Frontend dev  : cd frontend && npm install && npm run dev"
        echo "  Containers    : docker compose ps"
    fi
    echo
}

# ─── Main ─────────────────────────────────────────────────────────────────────
main() {
    header "Flux.io Installer"
    check_root "$@"

    detect_distro
    install_docker
    ensure_docker_running

    select_mode
    run_wizard
    check_all_ports

    if [[ "$INSTALL_MODE" == "development" ]]; then
        header "Dev Toolchain"
        install_go
        install_node
    fi

    header "Starting Flux.io"
    cd "${REPO_DIR}"

    if [[ "$INSTALL_MODE" == "production" ]]; then
        # Download GeoIP BEFORE containers start so the backend finds the
        # .mmdb files on its very first boot (the volume mount is read at
        # container-create time, but the backend reads the files at runtime).
        download_geoip
        # Let systemd own the container lifecycle — it calls `docker compose up`
        # on start/restart. We do NOT also run `docker compose up -d` here to
        # avoid two processes managing the same compose project simultaneously.
        install_systemd_service
    else
        # Development mode: start containers directly; systemd not involved.
        if ! docker compose up -d; then
            error "docker compose up failed. Showing last 20 log lines:"
            docker compose logs --tail=20 || true
            error "Run 'docker compose logs' for full details."
            exit 1
        fi
        success "Containers started."
    fi

    wait_for_healthy
    print_summary
}

main "$@"
