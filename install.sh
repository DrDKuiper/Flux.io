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
GEOIP_UPDATE_BIN="/usr/local/bin/fluxio-geoip-update"
GEOIP_UPDATE_SVC="/etc/systemd/system/fluxio-geoip-update.service"
GEOIP_UPDATE_TMR="/etc/systemd/system/fluxio-geoip-update.timer"
LOG_DIR="/var/log/fluxio"
INSTALL_LOG="${LOG_DIR}/install.log"

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

# ─── Logging to /var/log/fluxio/install.log ───────────────────────────────────
setup_logging() {
    mkdir -p "${LOG_DIR}"
    chmod 750 "${LOG_DIR}"
    # Tee everything (stdout + stderr) into the install log.
    # The file descriptor dance keeps colours on the terminal while the log
    # file stores plain text (strip ANSI via sed).
    exec > >(tee >(sed 's/\x1B\[[0-9;]*[mK]//g' >> "${INSTALL_LOG}")) 2>&1
    info "Install log: ${INSTALL_LOG}"
}

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
            debian)      apt-get install -y docker-compose-plugin ;;
            rhel|fedora) "$PKG_MGR" install -y docker-compose-plugin ;;
            arch)        pacman -S --noconfirm docker-compose ;;
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

# ─── SELinux check & remediation ─────────────────────────────────────────────
# Detects SELinux state and, if Enforcing, applies container file contexts to
# every directory that will be bind-mounted into a Docker container so that
# the daemon can read/write them without AVC denials.
# ──────────────────────────────────────────────────────────────────────────────
check_selinux() {
    header "SELinux Check"

    if ! command -v getenforce &>/dev/null; then
        info "SELinux not present on this system — skipping."
        return
    fi

    local se_status
    se_status="$(getenforce 2>/dev/null || echo 'Unknown')"

    case "$se_status" in
        Enforcing)
            warn "SELinux is in ${BOLD}Enforcing${RESET}${YELLOW} mode."
            info "Applying container file contexts to Flux.io bind-mount directories..."

            # Prefer the modern type; fall back to the legacy one (RHEL 7)
            local se_type="container_file_t"
            if command -v seinfo &>/dev/null && ! seinfo -t container_file_t &>/dev/null 2>&1; then
                se_type="svirt_sandbox_file_t"
            fi

            local -a bind_dirs=(
                "${REPO_DIR}/backend/geoip"
                "${REPO_DIR}/suricata/logs"
                "${REPO_DIR}/db/clickhouse"
                "${REPO_DIR}/db/postgres"
            )
            local ctx_ok=0 ctx_fail=0
            for dir in "${bind_dirs[@]}"; do
                mkdir -p "$dir"
                if chcon -Rt "${se_type}" "$dir" 2>/dev/null; then
                    info "  ✓ chcon -Rt ${se_type} ${dir}"
                    (( ctx_ok++ )) || true
                else
                    warn "  ✗ Could not relabel ${dir} — volume mount may fail."
                    (( ctx_fail++ )) || true
                fi
            done

            # Enable booleans required for Docker networking and cgroup access
            local -a se_bools=(container_manage_cgroup container_use_devices)
            for bool in "${se_bools[@]}"; do
                setsebool -P "$bool" 1 2>/dev/null && \
                    info "  ✓ setsebool -P ${bool} 1" || \
                    warn "  ✗ Could not set ${bool} (may not exist on this policy version)"
            done

            if (( ctx_fail == 0 )); then
                success "SELinux contexts applied (${ctx_ok} directories relabelled, type: ${se_type})."
            else
                warn "${ctx_fail} directories could not be relabelled. If containers log 'Permission denied', run:"
                warn "  chcon -Rt container_file_t ${REPO_DIR}"
            fi
            ;;
        Permissive)
            warn "SELinux is Permissive — not blocking, but writing denials to audit.log."
            info "Check for issues post-install: ausearch -m avc -ts recent | audit2why"
            ;;
        Disabled)
            success "SELinux is disabled — no action needed."
            ;;
        *)
            warn "Unexpected getenforce output: '${se_status}'. Continuing."
            ;;
    esac
}

# ─── Firewall check & auto-configuration ──────────────────────────────────────
# Detects the active firewall manager (firewalld, ufw, nftables, iptables) and
# checks whether the external-facing Flux.io ports are allowed through.
#
# Ports that need to be reachable from outside the host:
#   TCP  PORT         Dashboard + REST API
#   UDP  NETFLOW_PORT NetFlow v9 / IPFIX from routers / switches
#   UDP  TZSP_PORT    TZSP-mirrored packet traffic from switches
#
# Docker-internal ports (ClickHouse 8123/9000, Postgres 5432) are NOT opened —
# they are only reachable inside the Docker bridge network.
# ──────────────────────────────────────────────────────────────────────────────
_fw_type=""

_detect_firewall() {
    if systemctl is-active --quiet firewalld 2>/dev/null; then
        _fw_type="firewalld"
    elif command -v ufw &>/dev/null && ufw status 2>/dev/null | grep -q "Status: active"; then
        _fw_type="ufw"
    elif systemctl is-active --quiet nftables 2>/dev/null && command -v nft &>/dev/null; then
        _fw_type="nftables"
    elif command -v iptables &>/dev/null && \
         iptables -L INPUT -n 2>/dev/null | grep -qv "^Chain\|^target\|^$"; then
        _fw_type="iptables"
    else
        _fw_type="none"
    fi
}

_fw_port_open() {
    local proto="$1" port="$2"
    case "$_fw_type" in
        firewalld) firewall-cmd --query-port="${port}/${proto}" --zone=public &>/dev/null ;;
        ufw)       ufw status 2>/dev/null | grep -qE "^${port}/(${proto}|any)[[:space:]]+ALLOW" ;;
        nftables)  nft list ruleset 2>/dev/null | grep -qE "${proto}[[:space:]]+dport[[:space:]]+${port}[[:space:]]+accept" ;;
        iptables)  iptables -C INPUT -p "$proto" --dport "$port" -j ACCEPT &>/dev/null ;;
        *)         return 0 ;;  # no firewall — assume open
    esac
}

_fw_open_port() {
    local proto="$1" port="$2"
    case "$_fw_type" in
        firewalld)
            firewall-cmd --add-port="${port}/${proto}" --zone=public --permanent
            ;;
        ufw)
            ufw allow "${port}/${proto}"
            ;;
        nftables)
            # nft rule is version-dependent; emit manual command and return
            warn "nftables: add manually → nft add rule inet filter input ${proto} dport ${port} accept"
            return 1
            ;;
        iptables)
            iptables -I INPUT -p "$proto" --dport "$port" -j ACCEPT
            # Persist depending on what's available
            if [[ -d /etc/iptables ]]; then
                iptables-save > /etc/iptables/rules.v4 2>/dev/null || true
            elif command -v service &>/dev/null; then
                service iptables save 2>/dev/null || true
            fi
            ;;
    esac
}

_fw_reload() {
    case "$_fw_type" in
        firewalld) firewall-cmd --reload ;;
        ufw)       ufw reload ;;
        *)         ;; # iptables / nftables — rules take effect immediately
    esac
}

_fw_print_manual() {
    local http_port="$1" nf_port="$2" tzsp_port="$3"
    case "$_fw_type" in
        firewalld)
            echo "  firewall-cmd --add-port=${http_port}/tcp --permanent && \\"
            echo "  firewall-cmd --add-port=${nf_port}/udp  --permanent && \\"
            echo "  firewall-cmd --add-port=${tzsp_port}/udp --permanent && \\"
            echo "  firewall-cmd --reload"
            ;;
        ufw)
            echo "  ufw allow ${http_port}/tcp"
            echo "  ufw allow ${nf_port}/udp"
            echo "  ufw allow ${tzsp_port}/udp"
            ;;
        nftables)
            echo "  nft add rule inet filter input tcp dport ${http_port} accept"
            echo "  nft add rule inet filter input udp dport ${nf_port}  accept"
            echo "  nft add rule inet filter input udp dport ${tzsp_port} accept"
            ;;
        iptables)
            echo "  iptables -I INPUT -p tcp --dport ${http_port} -j ACCEPT"
            echo "  iptables -I INPUT -p udp --dport ${nf_port}  -j ACCEPT"
            echo "  iptables -I INPUT -p udp --dport ${tzsp_port} -j ACCEPT"
            ;;
    esac
}

check_firewall() {
    header "Firewall Check"
    _detect_firewall

    if [[ "$_fw_type" == "none" ]]; then
        info "No active firewall detected — skipping."
        return
    fi

    success "Active firewall: ${BOLD}${_fw_type}${RESET}"
    echo

    local http_port nf_port tzsp_port
    http_port="$(_env_val PORT)"
    nf_port="$(_env_val NETFLOW_PORT)"
    tzsp_port="$(_env_val TZSP_PORT)"

    # Print status table
    printf "  %-6s %-8s %-26s %s\n" "Proto" "Port" "Purpose" "Status"
    printf "  %-6s %-8s %-26s %s\n" "─────" "──────" "────────────────────────" "──────"

    local needs_action=0
    local -a checks=(
        "tcp:${http_port}:Dashboard / REST API"
        "udp:${nf_port}:NetFlow v9 / IPFIX"
        "udp:${tzsp_port}:TZSP mirror traffic"
    )
    for spec in "${checks[@]}"; do
        IFS=: read -r proto port label <<< "$spec"
        if _fw_port_open "$proto" "$port"; then
            printf "  ${GREEN}✅${RESET}  %-6s %-8s %-26s ${GREEN}open${RESET}\n" \
                "$proto" "$port" "$label"
        else
            printf "  ${YELLOW}⚠️ ${RESET}  %-6s %-8s %-26s ${YELLOW}BLOCKED${RESET}\n" \
                "$proto" "$port" "$label"
            needs_action=1
        fi
    done
    echo

    if (( needs_action == 0 )); then
        success "All required ports are open."
        return
    fi

    read -rp "Open blocked ports automatically via ${_fw_type}? [Y/n]: " fw_choice
    fw_choice="${fw_choice:-Y}"
    if [[ "${fw_choice^^}" != "Y" ]]; then
        warn "Skipping. Open ports manually:"
        _fw_print_manual "$http_port" "$nf_port" "$tzsp_port"
        return
    fi

    info "Configuring ${_fw_type} rules..."
    local fw_ok=1
    _fw_open_port "tcp" "$http_port"  && success "Opened TCP ${http_port}  (Dashboard / API)"  || { warn "Failed TCP ${http_port}";  fw_ok=0; }
    _fw_open_port "udp" "$nf_port"   && success "Opened UDP ${nf_port}   (NetFlow)"            || { warn "Failed UDP ${nf_port}";   fw_ok=0; }
    _fw_open_port "udp" "$tzsp_port" && success "Opened UDP ${tzsp_port} (TZSP)"               || { warn "Failed UDP ${tzsp_port}"; fw_ok=0; }

    if (( fw_ok == 1 )); then
        _fw_reload && success "${_fw_type} reloaded — rules are active." || \
            warn "Reload returned non-zero; rules may need a manual reload."
    else
        warn "Some ports could not be opened. See warnings above."
        warn "Manual commands:"
        _fw_print_manual "$http_port" "$nf_port" "$tzsp_port"
    fi
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

# ─── GeoIP download ───────────────────────────────────────────────────────────
# Downloads GeoLite2 databases from a trusted public GitHub mirror
# (P3TERX/GeoLite.mmdb — updated every week directly from MaxMind).
# No API key or MaxMind account required.
#
# Mirror: https://github.com/P3TERX/GeoLite.mmdb
# Fallback: https://github.com/aleskxyz/maxmind-geolite2-mirror
# ──────────────────────────────────────────────────────────────────────────────
GEOIP_MIRRORS=(
    "https://github.com/P3TERX/GeoLite.mmdb/releases/latest/download"
    "https://github.com/aleskxyz/maxmind-geolite2-mirror/releases/latest/download"
)

_download_mmdb() {
    local db="$1"          # e.g. GeoLite2-City
    local dest="$2"        # destination path
    local filename="${db}.mmdb"

    for mirror in "${GEOIP_MIRRORS[@]}"; do
        info "Trying mirror: ${mirror}/${filename} ..."
        if curl -fsSL --retry 3 --retry-delay 2 \
               "${mirror}/${filename}" -o "${dest}"; then
            # Validate it's actually an mmdb file (magic bytes: 0xABCDEF)
            if file "${dest}" 2>/dev/null | grep -qi "data\|mmdb\|binary"; then
                success "${filename} downloaded ($(du -h "${dest}" | cut -f1))"
                return 0
            else
                warn "Downloaded file from ${mirror} does not look like a valid .mmdb — trying next mirror."
                rm -f "${dest}"
            fi
        else
            warn "Mirror ${mirror} failed — trying next."
        fi
    done

    warn "All mirrors failed for ${filename}. GeoIP enrichment will be disabled until the file is present."
    return 1
}

download_geoip() {
    header "GeoIP Enrichment"
    echo "  Databases are downloaded from a trusted public mirror (no API key needed)."
    echo "  Source: https://github.com/P3TERX/GeoLite.mmdb (updated weekly from MaxMind)"
    echo
    read -rp "Download GeoLite2 databases now? [Y/n]: " geoip_choice
    geoip_choice="${geoip_choice:-Y}"
    if [[ "${geoip_choice^^}" != "Y" ]]; then
        warn "GeoIP skipped. Add GeoLite2-City.mmdb and GeoLite2-ASN.mmdb to backend/geoip/ to enable enrichment."
        return
    fi

    mkdir -p "${REPO_DIR}/backend/geoip"

    local ok=0
    _download_mmdb "GeoLite2-City" "${REPO_DIR}/backend/geoip/GeoLite2-City.mmdb" && ok=$(( ok + 1 )) || true
    _download_mmdb "GeoLite2-ASN"  "${REPO_DIR}/backend/geoip/GeoLite2-ASN.mmdb"  && ok=$(( ok + 1 )) || true

    if (( ok == 2 )); then
        success "Both GeoIP databases ready in backend/geoip/"
    elif (( ok == 1 )); then
        warn "Only one GeoIP database was downloaded. Enrichment will be partial."
    else
        warn "No GeoIP databases were downloaded. Enrichment will be disabled."
    fi
}

# ─── GeoIP auto-updater (systemd timer) ───────────────────────────────────────
# Creates:
#   /usr/local/bin/fluxio-geoip-update   — standalone update script
#   fluxio-geoip-update.service          — oneshot, runs the script
#   fluxio-geoip-update.timer            — fires every week on Sunday 03:00
# Then enables the timer and runs the first update immediately.
# ──────────────────────────────────────────────────────────────────────────────
install_geoip_updater() {
    info "Installing weekly GeoIP auto-updater ..."

    # ── Update script ──
    cat > "${GEOIP_UPDATE_BIN}" <<SCRIPT
#!/usr/bin/env bash
# Flux.io GeoIP updater — managed by fluxio-geoip-update.timer
# Do not edit directly; re-run install.sh to regenerate.
set -euo pipefail

GEOIP_DIR="${REPO_DIR}/backend/geoip"
LOG_FILE="${LOG_DIR}/geoip-update.log"
MIRRORS=(
    "https://github.com/P3TERX/GeoLite.mmdb/releases/latest/download"
    "https://github.com/aleskxyz/maxmind-geolite2-mirror/releases/latest/download"
)

mkdir -p "\${GEOIP_DIR}" "${LOG_DIR}"
exec >> "\${LOG_FILE}" 2>&1
echo "[\$(date '+%Y-%m-%d %H:%M:%S')] Starting GeoIP update ..."

download_db() {
    local db="\$1"
    local dest="\${GEOIP_DIR}/\${db}.mmdb"
    local tmp="\${dest}.tmp"

    for mirror in "\${MIRRORS[@]}"; do
        if curl -fsSL --retry 3 --retry-delay 5 "\${mirror}/\${db}.mmdb" -o "\${tmp}"; then
            mv "\${tmp}" "\${dest}"
            echo "[\$(date '+%Y-%m-%d %H:%M:%S')] \${db}.mmdb updated (mirror: \${mirror})"
            return 0
        fi
        echo "[\$(date '+%Y-%m-%d %H:%M:%S')] Mirror \${mirror} failed for \${db}.mmdb"
        rm -f "\${tmp}"
    done

    echo "[\$(date '+%Y-%m-%d %H:%M:%S')] WARNING: All mirrors failed for \${db}.mmdb"
    return 1
}

download_db "GeoLite2-City" || true
download_db "GeoLite2-ASN"  || true
echo "[\$(date '+%Y-%m-%d %H:%M:%S')] GeoIP update complete."
SCRIPT
    chmod +x "${GEOIP_UPDATE_BIN}"

    # ── systemd service (oneshot) ──
    cat > "${GEOIP_UPDATE_SVC}" <<EOF
[Unit]
Description=Flux.io GeoIP database update
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
ExecStart=${GEOIP_UPDATE_BIN}
StandardOutput=append:${LOG_DIR}/geoip-update.log
StandardError=append:${LOG_DIR}/geoip-update.log

[Install]
WantedBy=multi-user.target
EOF

    # ── systemd timer (every Sunday at 03:00) ──
    cat > "${GEOIP_UPDATE_TMR}" <<EOF
[Unit]
Description=Weekly GeoIP database update for Flux.io

[Timer]
OnCalendar=Sun *-*-* 03:00:00
Persistent=true
RandomizedDelaySec=900

[Install]
WantedBy=timers.target
EOF

    systemctl daemon-reload
    systemctl enable fluxio-geoip-update.timer
    systemctl start  fluxio-geoip-update.timer

    success "GeoIP auto-updater installed (fires every Sunday 03:00)."
    success "Update logs: ${LOG_DIR}/geoip-update.log"
    success "Manual update: systemctl start fluxio-geoip-update.service"
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
StandardOutput=append:${LOG_DIR}/fluxio.log
StandardError=append:${LOG_DIR}/fluxio.log

[Install]
WantedBy=multi-user.target
EOF

    systemctl daemon-reload
    systemctl enable --now fluxio
    success "fluxio.service installed and enabled. Check status: systemctl status fluxio"
    success "Service logs: ${LOG_DIR}/fluxio.log  (also: journalctl -u fluxio -f)"
}

# ─── Health check — waits until everything is truly running ──────────────────
# Strategy (production):
#   1. Wait for the systemd service to reach 'active' state.
#   2. Wait for all Docker Compose services to report 'running'.
#   3. Poll /api/health until it returns HTTP 200.
#
# Strategy (development):
#   1. Wait for all Docker Compose services to report 'running'.
#   2. Poll /api/health until it returns HTTP 200.
#
# There is no hard exit timeout — the installer only prints the success banner
# once the stack is genuinely healthy. The user can abort with Ctrl-C at any time.
# ──────────────────────────────────────────────────────────────────────────────
wait_for_healthy() {
    local http_port
    http_port="$(_env_val PORT)"
    local health_url="http://127.0.0.1:${http_port}/api/health"

    # ── Step 1 (production only): wait for systemd service to become active ──
    if [[ "${INSTALL_MODE}" == "production" ]]; then
        info "Waiting for fluxio.service to become active ..."
        local svc_attempts=0
        while true; do
            local svc_state
            svc_state="$(systemctl is-active fluxio 2>/dev/null || true)"
            if [[ "$svc_state" == "active" ]]; then
                success "fluxio.service is active."
                break
            elif [[ "$svc_state" == "failed" ]]; then
                error "fluxio.service failed to start."
                echo
                journalctl -u fluxio --no-pager -n 30 || true
                error "Fix the issue above and run: systemctl start fluxio"
                exit 1
            fi
            svc_attempts=$(( svc_attempts + 1 ))
            if (( svc_attempts % 10 == 0 )); then
                info "Still waiting for systemd service... (${svc_attempts}s elapsed)"
                info "Live logs: journalctl -u fluxio -f"
            fi
            printf '.'
            sleep 1
        done
        echo
    fi

    # ── Step 2: wait for all compose services to reach 'running' ─────────────
    # Use `docker ps -a` filtered by container name prefix ("fluxio-") instead of
    # `docker compose ps` — the latter can miss containers started by systemd when
    # the project context differs (e.g. working directory vs -f flag resolution).
    info "Waiting for all containers to reach 'running' state ..."
    info "(Image build may take a few minutes on first run)"
    local container_attempts=0
    while true; do
        local _ps_out total running exited
        _ps_out="$(docker ps -a --filter 'name=fluxio-' \
                      --format '{{.Names}}\t{{.Status}}' 2>/dev/null || true)"

        total="$(  echo "$_ps_out" | grep -c '.'             || true)"; total="${total:-0}"
        running="$(echo "$_ps_out" | grep -cE '\bUp\b'       || true)"; running="${running:-0}"
        exited="$( echo "$_ps_out" | grep -cE 'Exited|Exit ' || true)"; exited="${exited:-0}"

        if [[ "$total" -gt 0 ]] && [[ "$running" -ge "$total" ]]; then
            success "All ${total} containers are running."
            break
        fi

        if [[ "$exited" -gt 0 ]]; then
            echo
            warn "One or more containers exited. Last 30 log lines:"
            docker compose -f "${REPO_DIR}/docker-compose.yml" logs --tail=30 2>/dev/null || \
                journalctl -u fluxio --no-pager -n 30 2>/dev/null || true
            error "Fix the errors above, then run: systemctl start fluxio"
            exit 1
        fi

        container_attempts=$(( container_attempts + 1 ))

        # Every 30 s: show a live snippet so the user can see progress (build output, pulls, etc.)
        if (( container_attempts % 30 == 0 )); then
            echo
            info "Still waiting... ${container_attempts}s elapsed — ${running}/${total} containers up"
            info "Recent service output:"
            journalctl -u fluxio --no-pager -n 6 2>/dev/null || \
                tail -n 6 "${LOG_DIR}/fluxio.log" 2>/dev/null || true
            echo
        fi

        # Hard timeout at 10 minutes — something is clearly stuck
        if (( container_attempts >= 600 )); then
            echo
            error "Containers did not start after 10 minutes. Full service log:"
            journalctl -u fluxio --no-pager -n 60 2>/dev/null || \
                tail -n 60 "${LOG_DIR}/fluxio.log" 2>/dev/null || true
            error "Try manually: cd ${REPO_DIR} && docker compose up"
            exit 1
        fi

        printf '.'
        sleep 1
    done
    echo

    # ── Step 3: poll the health endpoint ─────────────────────────────────────
    info "Waiting for Flux.io API to be ready at ${health_url} ..."
    local api_attempts=0
    while true; do
        local http_status
        http_status="$(curl -o /dev/null -sw '%{http_code}' --max-time 3 \
                        "${health_url}" 2>/dev/null || echo 000)"
        if [[ "$http_status" == "200" ]]; then
            success "Flux.io is healthy (HTTP 200)."
            return
        fi

        api_attempts=$(( api_attempts + 1 ))
        if (( api_attempts % 15 == 0 )); then
            info "API not ready yet (HTTP ${http_status}, ${api_attempts}s elapsed)"
            info "Container logs: docker compose logs --tail=20 backend"
        fi
        printf '.'
        sleep 1
    done
    echo
}

# ─── Post-startup service verification ───────────────────────────────────────
# Runs after wait_for_healthy and performs a final structured audit:
#   1. Container status table (name, state, port mappings)
#   2. GeoIP database presence and size
#   3. HTTP health endpoint
#   4. Listening port verification (TCP confirmed; UDP advisory)
#   5. Docker volume inventory
# Prints a single PASS / WARN banner at the end.
# ──────────────────────────────────────────────────────────────────────────────
verify_services() {
    header "Service Verification"

    local all_ok=1

    # ── 1. Container status table ─────────────────────────────────────────────
    echo -e "  ${BOLD}Containers:${RESET}"
    printf "  %-5s %-28s %-12s %s\n" "" "Name" "State" "Ports"
    printf "  %-5s %-28s %-12s %s\n" "" "────────────────────────────" "──────────" "──────────────────────────"

    local container_ok=0 container_fail=0
    while IFS= read -r line; do
        [[ -z "$line" ]] && continue
        # Compose v2: NAME  IMAGE  COMMAND  SERVICE  CREATED  STATUS  PORTS
        local cname cstatus cports
        cname="$(   echo "$line" | awk '{print $1}')"
        # STATUS column varies: "Up 2 minutes", "running", "Exited (1) 2 minutes ago"
        if echo "$line" | grep -qiE '\bUp\b|\brunning\b'; then
            cstatus="${GREEN}running${RESET}"
            # Extract port mappings (last field cluster) — simplified
            cports="$(echo "$line" | grep -oE '0\.0\.0\.0:[0-9]+->[0-9]+/(tcp|udp)' | head -3 | tr '\n' ' ')"
            [[ -z "$cports" ]] && cports="(internal / host network)"
            printf "  ${GREEN}✅${RESET}  %-28s %-22b %s\n" "$cname" "$cstatus" "$cports"
            (( container_ok++  )) || true
        else
            cstatus="${RED}$(echo "$line" | awk '{print $6,$7,$8}' | sed 's/[[:space:]]*$//')${RESET}"
            printf "  ${RED}✗${RESET}   %-28s %-22b\n" "$cname" "$cstatus"
            (( container_fail++ )) || true
            all_ok=0
        fi
    done < <(docker compose -f "${REPO_DIR}/docker-compose.yml" ps --all 2>/dev/null | tail -n +2)

    if (( container_ok == 0 && container_fail == 0 )); then
        warn "No containers found. Is the Compose project running?"
        all_ok=0
    fi
    echo

    # ── 2. GeoIP databases ────────────────────────────────────────────────────
    echo -e "  ${BOLD}GeoIP databases:${RESET}"
    for db in GeoLite2-City GeoLite2-ASN; do
        local dbpath="${REPO_DIR}/backend/geoip/${db}.mmdb"
        if [[ -f "$dbpath" ]]; then
            printf "  ${GREEN}✅${RESET}  %-24s %s\n" "${db}.mmdb" "($(du -h "$dbpath" | cut -f1))"
        else
            printf "  ${YELLOW}⚠️ ${RESET}  %-24s not found — GeoIP enrichment disabled\n" "${db}.mmdb"
        fi
    done
    echo

    # ── 3. HTTP health endpoint ───────────────────────────────────────────────
    local http_port
    http_port="$(_env_val PORT)"
    echo -e "  ${BOLD}API:${RESET}"
    local hcode
    hcode="$(curl -o /dev/null -sw '%{http_code}' --max-time 5 \
                "http://127.0.0.1:${http_port}/api/health" 2>/dev/null || echo 000)"
    if [[ "$hcode" == "200" ]]; then
        printf "  ${GREEN}✅${RESET}  GET /api/health → HTTP 200\n"
    else
        printf "  ${RED}✗${RESET}   GET /api/health → HTTP %s\n" "$hcode"
        all_ok=0
    fi
    echo

    # ── 4. Port binding verification ──────────────────────────────────────────
    local nf_port tzsp_port
    nf_port="$(_env_val NETFLOW_PORT)"
    tzsp_port="$(_env_val TZSP_PORT)"
    echo -e "  ${BOLD}Port bindings:${RESET}"
    printf "  %-5s %-6s %-8s %s\n" "" "Proto" "Port" "Purpose"
    printf "  %-5s %-6s %-8s %s\n" "" "─────" "──────" "──────────────────────"

    # TCP ports are verifiable via ss/netstat
    if _port_in_use_tcp "$http_port"; then
        printf "  ${GREEN}✅${RESET}  %-6s %-8s Dashboard / API\n" "tcp" "$http_port"
    else
        printf "  ${RED}✗${RESET}   %-6s %-8s ${RED}NOT listening${RESET} — backend may have failed\n" "tcp" "$http_port"
        all_ok=0
    fi

    # UDP sockets only appear in ss when actively bound; advisory only
    for udp_spec in "${nf_port}:NetFlow v9 / IPFIX" "${tzsp_port}:TZSP mirror"; do
        IFS=: read -r uport ulabel <<< "$udp_spec"
        if _port_in_use_udp "$uport"; then
            printf "  ${GREEN}✅${RESET}  %-6s %-8s %s\n" "udp" "$uport" "$ulabel"
        else
            printf "  ${YELLOW}⚠️ ${RESET}  %-6s %-8s %s — socket not yet visible (normal until first packet)\n" \
                "udp" "$uport" "$ulabel"
        fi
    done
    echo

    # ── 5. Docker volumes ─────────────────────────────────────────────────────
    echo -e "  ${BOLD}Docker volumes:${RESET}"
    local vol_count=0
    while IFS= read -r vol; do
        printf "  ${GREEN}✅${RESET}  %s\n" "$vol"
        (( vol_count++ )) || true
    done < <(docker volume ls --filter "name=fluxio" --format "{{.Name}}" 2>/dev/null)
    (( vol_count == 0 )) && warn "No named volumes found — data may not be persisted."
    echo

    # ── Summary banner ────────────────────────────────────────────────────────
    if (( all_ok == 1 )); then
        success "${BOLD}All checks passed — Flux.io is fully operational.${RESET}"
    else
        warn "${BOLD}Some checks failed.${RESET} Review the output above."
        warn "Diagnostics:"
        warn "  docker compose logs --tail=50"
        warn "  journalctl -u fluxio -n 50 --no-pager"
        warn "  cat ${LOG_DIR}/fluxio.log | tail -50"
    fi
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
    echo "Logs:"
    echo "  Install log  : ${INSTALL_LOG}"
    echo "  Service log  : ${LOG_DIR}/fluxio.log"
    echo "  GeoIP update : ${LOG_DIR}/geoip-update.log"
    echo "  Live tail    : journalctl -u fluxio -f"
    echo
    echo "Next steps:"
    echo "  - Point your NetFlow exporter to ${host_ip}:${nf_port}"
    echo "  - Open the dashboard and configure DPI mode under Settings"
    if [[ -z "$wazuh_ip" ]]; then
        echo "  - Wazuh forwarding: set WAZUH_MANAGER_IP in .env and run 'docker compose restart backend'"
    fi
    if [[ "$INSTALL_MODE" == "production" ]]; then
        echo "  - GeoIP updates    : automatic every Sunday 03:00"
        echo "    Manual update    : systemctl start fluxio-geoip-update.service"
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
    check_root "$@"
    setup_logging

    header "Flux.io Installer"

    detect_distro
    install_docker
    ensure_docker_running

    select_mode
    run_wizard
    check_all_ports
    check_selinux
    check_firewall

    if [[ "$INSTALL_MODE" == "development" ]]; then
        header "Dev Toolchain"
        install_go
        install_node
    fi

    header "Starting Flux.io"
    cd "${REPO_DIR}"

    if [[ "$INSTALL_MODE" == "production" ]]; then
        # Download GeoIP BEFORE containers start so the backend finds the
        # .mmdb files on its very first boot.
        download_geoip
        # Install the weekly auto-updater timer.
        install_geoip_updater
        # Let systemd own the container lifecycle.
        install_systemd_service
    else
        # Development: start containers directly.
        if ! docker compose up -d; then
            error "docker compose up failed. Showing last 20 log lines:"
            docker compose logs --tail=20 || true
            exit 1
        fi
        success "Containers started."
    fi

    # Block here until the full stack is confirmed healthy.
    wait_for_healthy
    # Structured post-startup audit.
    verify_services
    print_summary
}

main "$@"
