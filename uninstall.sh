#!/usr/bin/env bash
# uninstall.sh — Flux.io uninstaller
# Usage: sudo ./uninstall.sh [--yes]
#
# Removes Flux.io services, containers, and optionally all data.
# Every destructive step asks for confirmation unless --yes is passed.
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LOG_DIR="/var/log/fluxio"
SYSTEMD_UNIT="/etc/systemd/system/fluxio.service"
GEOIP_UPDATE_BIN="/usr/local/bin/fluxio-geoip-update"
GEOIP_UPDATE_SVC="/etc/systemd/system/fluxio-geoip-update.service"
GEOIP_UPDATE_TMR="/etc/systemd/system/fluxio-geoip-update.timer"

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
sep()     { echo -e "  ${CYAN}────────────────────────────────────────────${RESET}"; }

# ─── Flags ────────────────────────────────────────────────────────────────────
YES_ALL=false
for arg in "$@"; do
    [[ "$arg" == "--yes" ]] && YES_ALL=true
done

# ─── Privilege check ──────────────────────────────────────────────────────────
if [[ $EUID -ne 0 ]]; then
    warn "Not running as root. Re-executing with sudo..."
    exec sudo bash "$0" "$@"
fi

# ─── Confirmation helper ──────────────────────────────────────────────────────
# _confirm "Question?" → returns 0 (yes) or 1 (no)
_confirm() {
    if $YES_ALL; then return 0; fi
    local prompt="$1"
    read -rp "  ${prompt} [y/N]: " ans
    [[ "${ans^^}" == "Y" ]]
}

# ─── Status snapshot ──────────────────────────────────────────────────────────
_show_status() {
    header "Current Flux.io Status"

    # systemd services
    for unit in fluxio fluxio-geoip-update; do
        local state
        state="$(systemctl is-active "${unit}" 2>/dev/null || echo 'not-found')"
        case "$state" in
            active)    echo -e "  ${GREEN}●${RESET} ${unit}.service/timer — active" ;;
            inactive)  echo -e "  ${YELLOW}○${RESET} ${unit}.service/timer — installed but inactive" ;;
            not-found) echo -e "  ${CYAN}○${RESET} ${unit}.service/timer — not installed" ;;
            *)         echo -e "  ${RED}●${RESET} ${unit}.service/timer — ${state}" ;;
        esac
    done

    echo

    # containers
    if docker compose -f "${REPO_DIR}/docker-compose.yml" ps --all 2>/dev/null | tail -n +2 | grep -v '^$'; then
        : # output already printed
    else
        info "No Flux.io containers found."
    fi

    echo

    # volumes
    local vols
    vols="$(docker volume ls --filter "name=fluxio" --format "{{.Name}}" 2>/dev/null || true)"
    if [[ -n "$vols" ]]; then
        info "Docker volumes: $(echo "$vols" | tr '\n' ' ')"
    else
        info "No Flux.io Docker volumes found."
    fi
}

# ─── Steps ────────────────────────────────────────────────────────────────────

_stop_services() {
    header "Step 1 — Stop & Disable systemd Services"

    # GeoIP timer
    if systemctl list-unit-files fluxio-geoip-update.timer &>/dev/null 2>&1 | grep -q fluxio; then
        info "Stopping fluxio-geoip-update.timer ..."
        systemctl stop    fluxio-geoip-update.timer  2>/dev/null || true
        systemctl disable fluxio-geoip-update.timer  2>/dev/null || true
        systemctl stop    fluxio-geoip-update.service 2>/dev/null || true
        success "GeoIP update timer stopped and disabled."
    else
        info "fluxio-geoip-update.timer not installed — skipping."
    fi

    # Main service
    if systemctl list-unit-files fluxio.service &>/dev/null 2>&1 | grep -q fluxio; then
        info "Stopping fluxio.service ..."
        systemctl stop    fluxio 2>/dev/null || true
        systemctl disable fluxio 2>/dev/null || true
        success "fluxio.service stopped and disabled."
    else
        # If no systemd unit, try to stop compose directly
        if docker compose -f "${REPO_DIR}/docker-compose.yml" ps -q 2>/dev/null | grep -q .; then
            info "No systemd unit found; stopping containers via docker compose ..."
            docker compose -f "${REPO_DIR}/docker-compose.yml" down 2>/dev/null || true
            success "Containers stopped."
        else
            info "No running containers found."
        fi
    fi
}

_remove_unit_files() {
    header "Step 2 — Remove systemd Unit Files"

    local removed=0
    for f in "${SYSTEMD_UNIT}" "${GEOIP_UPDATE_SVC}" "${GEOIP_UPDATE_TMR}"; do
        if [[ -f "$f" ]]; then
            rm -f "$f"
            info "Removed ${f}"
            (( removed++ )) || true
        fi
    done

    if [[ -f "${GEOIP_UPDATE_BIN}" ]]; then
        rm -f "${GEOIP_UPDATE_BIN}"
        info "Removed ${GEOIP_UPDATE_BIN}"
        (( removed++ )) || true
    fi

    if (( removed > 0 )); then
        systemctl daemon-reload
        success "Unit files removed and daemon reloaded."
    else
        info "No unit files found — nothing to remove."
    fi
}

_remove_containers() {
    header "Step 3 — Remove Docker Containers & Images"

    # Bring down containers (already stopped, but this also removes networks)
    if docker compose -f "${REPO_DIR}/docker-compose.yml" ps --all -q 2>/dev/null | grep -q .; then
        info "Removing containers and networks ..."
        docker compose -f "${REPO_DIR}/docker-compose.yml" down --remove-orphans 2>/dev/null || true
        success "Containers and networks removed."
    else
        info "No containers to remove."
    fi

    # Optionally remove built images
    local img
    img="$(docker images --filter "reference=flux*" --format "{{.Repository}}:{{.Tag}}" 2>/dev/null || true)"
    if [[ -n "$img" ]]; then
        echo
        warn "The following Flux.io images will be deleted (frees disk space):"
        echo "$img" | sed 's/^/    /'
        echo
        if _confirm "Remove Flux.io Docker images?"; then
            echo "$img" | xargs -r docker rmi -f 2>/dev/null || true
            success "Images removed."
        else
            info "Images kept."
        fi
    fi
}

_remove_volumes() {
    header "Step 4 — Remove Docker Volumes (DATA LOSS WARNING)"

    local vols
    vols="$(docker volume ls --filter "name=fluxio" --format "{{.Name}}" 2>/dev/null || true)"

    if [[ -z "$vols" ]]; then
        info "No Flux.io volumes found — skipping."
        return
    fi

    echo -e "  ${RED}${BOLD}⚠️  The following volumes contain ALL Flux.io data:${RESET}"
    echo "$vols" | while read -r v; do
        local size
        size="$(docker run --rm -v "${v}:/data:ro" alpine du -sh /data 2>/dev/null | cut -f1 || echo '?')"
        echo -e "    ${RED}•${RESET} ${v}  (${size})"
    done
    echo
    warn "This will permanently delete all ClickHouse flows, Suricata alerts, and Postgres settings."
    echo

    if _confirm "Delete ALL Flux.io Docker volumes? THIS CANNOT BE UNDONE"; then
        echo "$vols" | xargs -r docker volume rm
        success "Volumes removed."
    else
        info "Volumes kept. To remove later: docker volume rm $(echo "$vols" | tr '\n' ' ')"
    fi
}

_remove_logs() {
    header "Step 5 — Remove Log Files"

    if [[ -d "${LOG_DIR}" ]]; then
        local size
        size="$(du -sh "${LOG_DIR}" 2>/dev/null | cut -f1 || echo '?')"
        warn "Log directory: ${LOG_DIR}  (${size})"
        if _confirm "Remove all Flux.io logs in ${LOG_DIR}?"; then
            rm -rf "${LOG_DIR}"
            success "Logs removed."
        else
            info "Logs kept at ${LOG_DIR}."
        fi
    else
        info "Log directory ${LOG_DIR} not found — skipping."
    fi
}

_remove_geoip() {
    header "Step 6 — Remove GeoIP Databases"

    local geoip_dir="${REPO_DIR}/backend/geoip"
    if [[ -d "$geoip_dir" ]] && ls "${geoip_dir}"/*.mmdb &>/dev/null 2>&1; then
        local size
        size="$(du -sh "${geoip_dir}" 2>/dev/null | cut -f1 || echo '?')"
        info "GeoIP databases found in ${geoip_dir}  (${size})"
        if _confirm "Remove GeoIP databases? (re-downloadable for free)"; then
            rm -f "${geoip_dir}"/*.mmdb
            success "GeoIP databases removed."
        else
            info "GeoIP databases kept."
        fi
    else
        info "No GeoIP databases found — skipping."
    fi
}

_remove_env() {
    header "Step 7 — Remove Configuration"

    local env_file="${REPO_DIR}/.env"
    if [[ -f "$env_file" ]]; then
        warn ".env contains passwords and custom port configuration."
        if _confirm "Remove ${env_file}?"; then
            # Keep a timestamped backup just in case
            local bak="${env_file}.bak.$(date +%Y%m%d%H%M%S)"
            cp "$env_file" "$bak"
            rm -f "$env_file"
            success ".env removed. Backup saved to ${bak}"
        else
            info ".env kept."
        fi
    else
        info ".env not found — skipping."
    fi
}

_remove_firewall_rules() {
    header "Step 8 — Remove Firewall Rules"

    # Detect firewall (same logic as installer)
    local fw_type="none"
    if systemctl is-active --quiet firewalld 2>/dev/null; then
        fw_type="firewalld"
    elif command -v ufw &>/dev/null && ufw status 2>/dev/null | grep -q "Status: active"; then
        fw_type="ufw"
    elif command -v iptables &>/dev/null; then
        fw_type="iptables"
    fi

    if [[ "$fw_type" == "none" ]]; then
        info "No active firewall detected — skipping."
        return
    fi

    # Read ports from .env if it still exists, otherwise use defaults
    local http_port nf_port tzsp_port
    http_port="${PORT:-80}"
    nf_port="${NETFLOW_PORT:-2055}"
    tzsp_port="${TZSP_PORT:-37008}"
    if [[ -f "${REPO_DIR}/.env" ]]; then
        http_port="$(grep -E '^PORT=' "${REPO_DIR}/.env" 2>/dev/null | cut -d= -f2 || echo 80)"
        nf_port="$(  grep -E '^NETFLOW_PORT=' "${REPO_DIR}/.env" 2>/dev/null | cut -d= -f2 || echo 2055)"
        tzsp_port="$(grep -E '^TZSP_PORT='    "${REPO_DIR}/.env" 2>/dev/null | cut -d= -f2 || echo 37008)"
    fi

    info "Active firewall: ${fw_type}"
    info "Rules to remove: tcp/${http_port}, udp/${nf_port}, udp/${tzsp_port}"

    if ! _confirm "Remove Flux.io firewall rules from ${fw_type}?"; then
        info "Firewall rules kept."
        return
    fi

    case "$fw_type" in
        firewalld)
            firewall-cmd --remove-port="${http_port}/tcp"  --zone=public --permanent 2>/dev/null || true
            firewall-cmd --remove-port="${nf_port}/udp"    --zone=public --permanent 2>/dev/null || true
            firewall-cmd --remove-port="${tzsp_port}/udp"  --zone=public --permanent 2>/dev/null || true
            firewall-cmd --reload 2>/dev/null || true
            success "firewalld rules removed."
            ;;
        ufw)
            ufw delete allow "${http_port}/tcp"  2>/dev/null || true
            ufw delete allow "${nf_port}/udp"    2>/dev/null || true
            ufw delete allow "${tzsp_port}/udp"  2>/dev/null || true
            success "ufw rules removed."
            ;;
        iptables)
            iptables -D INPUT -p tcp --dport "$http_port"  -j ACCEPT 2>/dev/null || true
            iptables -D INPUT -p udp --dport "$nf_port"    -j ACCEPT 2>/dev/null || true
            iptables -D INPUT -p udp --dport "$tzsp_port"  -j ACCEPT 2>/dev/null || true
            if [[ -d /etc/iptables ]]; then
                iptables-save > /etc/iptables/rules.v4 2>/dev/null || true
            elif command -v service &>/dev/null; then
                service iptables save 2>/dev/null || true
            fi
            success "iptables rules removed."
            ;;
    esac
}

# ─── Mode selection ───────────────────────────────────────────────────────────
_select_uninstall_mode() {
    header "Uninstall Mode"
    echo "  [1] Services only  — stop & remove systemd units, keep containers/data/logs"
    echo "  [2] Full uninstall — remove services + containers + volumes + logs + GeoIP"
    echo "  [3] Custom         — choose what to remove step by step"
    echo
    read -rp "Choice [2]: " choice
    choice="${choice:-2}"
    echo
    case "$choice" in
        1) echo "services" ;;
        2) echo "full"     ;;
        3) echo "custom"   ;;
        *) warn "Invalid choice — defaulting to full."; echo "full" ;;
    esac
}

# ─── Main ─────────────────────────────────────────────────────────────────────
main() {
    echo
    echo -e "${RED}${BOLD}╔══════════════════════════════════════════╗${RESET}"
    echo -e "${RED}${BOLD}║   🗑️   Flux.io Uninstaller               ║${RESET}"
    echo -e "${RED}${BOLD}╚══════════════════════════════════════════╝${RESET}"
    echo

    _show_status

    sep
    echo
    warn "This will remove Flux.io components from this system."
    warn "Press ${BOLD}Ctrl-C${RESET}${YELLOW} at any time to abort safely."
    echo
    if ! $YES_ALL; then
        read -rp "  Continue with uninstall? [y/N]: " cont
        [[ "${cont^^}" == "Y" ]] || { info "Aborted."; exit 0; }
    fi

    local mode
    mode="$(_select_uninstall_mode)"

    case "$mode" in
        services)
            _stop_services
            _remove_unit_files
            ;;
        full)
            _stop_services
            _remove_unit_files
            _remove_containers
            _remove_volumes
            _remove_logs
            _remove_geoip
            _remove_env
            _remove_firewall_rules
            ;;
        custom)
            _stop_services
            _remove_unit_files

            if _confirm "Remove Docker containers and networks?"; then
                _remove_containers
            fi
            if _confirm "Remove Docker volumes (ALL data)?"; then
                _remove_volumes
            fi
            if _confirm "Remove log files (/var/log/fluxio)?"; then
                _remove_logs
            fi
            if _confirm "Remove GeoIP databases?"; then
                _remove_geoip
            fi
            if _confirm "Remove .env configuration file?"; then
                _remove_env
            fi
            if _confirm "Remove firewall rules?"; then
                _remove_firewall_rules
            fi
            ;;
    esac

    echo
    sep
    echo
    success "${BOLD}Uninstall complete.${RESET}"
    echo
    echo "  Remaining artefacts (if any):"
    echo "  • Repository files : ${REPO_DIR}"
    echo "    Remove with       : rm -rf ${REPO_DIR}"
    echo
    echo "  • Docker images     : docker images | grep flux"
    echo "    Remove with       : docker rmi \$(docker images -q --filter reference='flux*')"
    echo
    if [[ "$mode" != "full" ]]; then
        echo "  • Volumes (if kept): docker volume ls --filter name=fluxio"
        echo "  • Logs   (if kept): ls ${LOG_DIR}"
        echo
    fi
}

main "$@"
