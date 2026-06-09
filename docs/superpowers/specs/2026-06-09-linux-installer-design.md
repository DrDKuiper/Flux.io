# Linux Installer Design — Flux.io

**Date:** 2026-06-09
**Status:** Approved

---

## Goal

A single `install.sh` script at the repo root that guides any Linux user through a fully interactive wizard to install and configure Flux.io, then starts all services. Supports two modes — **Production** and **Development** — and runs on all major Linux distributions.

---

## Scope

**In scope:**
- `install.sh` — the wizard script
- `.env.example` — documented template of all environment variables
- `docker-compose.yml` — updated to read variables from `.env` instead of hardcoded values

**Out of scope:**
- Windows or macOS support
- `curl | bash` one-liner / remote hosting
- Kubernetes / Helm deployment
- Automated upgrades / uninstaller

---

## Architecture

`install.sh` executes the following stages in order:

```
1. Privilege check (must run as root or with sudo)
2. Distro detection (/etc/os-release)
3. Dependency installation (Docker, Docker Compose, curl, git)
4. Mode selection: [1] Production  [2] Development
5. Configuration wizard → generates .env
6. Port conflict check → offers alternatives for conflicting ports
7. (Dev mode) Install Go 1.22 + Node 20 on host
8. docker compose up
9. (Prod mode) Download GeoIP files (optional, requires MaxMind key)
10. (Prod mode) Install + enable fluxio.service (systemd)
11. Summary screen
```

All stages are idempotent: re-running the script is safe and will skip already-installed components (e.g., Docker already present → skip install, `.env` already exists → offer to keep or overwrite).

---

## Components

### 1. Distro Detection

Reads `/etc/os-release` to identify the package manager family:

| Family | Distros | Package Manager |
|--------|---------|-----------------|
| Debian | Ubuntu, Debian, Linux Mint | `apt-get` |
| RHEL | RHEL, CentOS, Rocky Linux, AlmaLinux | `dnf` (or `yum` fallback) |
| Fedora | Fedora | `dnf` |
| Arch | Arch Linux, Manjaro | `pacman` |

If the distro is unrecognised, the script prints a clear error and exits with code 1.

### 2. Dependency Installation

Always installed (if not already present):
- `docker` (via official Docker install script or distro package)
- `docker compose` plugin (v2) or `docker-compose` standalone (v1 fallback)
- `curl`, `git`

Dev mode only:
- **Go 1.22** — installed via the official tarball from `https://go.dev/dl/` if the distro package is older than 1.22, extracted to `/usr/local/go`
- **Node 20** — installed via NodeSource repository (all distros) or `pacman` on Arch

### 3. Mode Selection

Interactive prompt shown after dependency install:

```
How do you want to run Flux.io?

  [1] Production  — runs in background, auto-starts on boot, optional GeoIP
  [2] Development — installs Go + Node on host, runs containers in foreground

Choice [1]:
```

### 4. Configuration Wizard

Prompts for each configurable value, showing the default in brackets. Pressing Enter accepts the default.

```
=== Flux.io Configuration ===

Wazuh Manager IP (blank to disable): []
Wazuh Manager Port [1514]:
Postgres password [fluxio_password]:
NetFlow UDP port [2055]:
TZSP UDP port [37008]:
Backend HTTP port [80]:
```

On completion, writes `.env` to the repo root:

```env
WAZUH_MANAGER_IP=
WAZUH_MANAGER_PORT=1514
POSTGRES_PASSWORD=fluxio_password
POSTGRES_DSN=postgres://fluxio:fluxio_password@postgres:5432/fluxioclient?sslmode=disable
CLICKHOUSE_DSN=clickhouse://default:@clickhouse:9000/fluxio
NETFLOW_PORT=2055
TZSP_PORT=37008
PORT=80
SURICATA_EVE_LOG_PATH=/var/log/suricata/eve.json
```

If `.env` already exists, the wizard asks:
```
.env already exists. [K]eep existing / [O]verwrite / [E]dit:
```

### 5. Port Conflict Check

Runs after the wizard, before starting containers. Checks every configured port using `ss -tlnup` (falls back to `netstat -tlnup` if `ss` is unavailable).

Ports checked: `80` (HTTP), `8123` (ClickHouse HTTP), `9000` (ClickHouse native), `5432` (Postgres), `2055/udp` (NetFlow), `37008/udp` (TZSP).

For each port in use:

```
  ⚠️  Port 5432 — IN USE (postgres/1234)
     Enter an alternative port, or press Enter to keep 5432 anyway: [5433]
```

If the user enters an alternative, the `.env` is updated in place. If they press Enter to keep the conflicting port, a warning is logged but the script continues. After all conflicts are resolved, a summary shows all ports:

```
Port check:
  ✅  80    (HTTP)         — free
  ✅  8123  (ClickHouse)   — free
  ✅  9000  (ClickHouse)   — free
  ✅  5433  (Postgres)     — free  [changed from 5432]
  ✅  2055  (NetFlow UDP)  — free
  ✅  37008 (TZSP UDP)     — free
```

### 6. GeoIP Download (Production mode, optional)

```
Download MaxMind GeoLite2 databases for GeoIP/ASN enrichment? [y/N]:
```

If yes:
```
MaxMind license key: ****
```

Downloads `GeoLite2-City.mmdb` and `GeoLite2-ASN.mmdb` from `https://download.maxmind.com/app/geoip_download` into `backend/geoip/`. If the download fails, the script warns but continues — the pipeline degrades gracefully without GeoIP files.

If no: prints a note that GeoIP enrichment will be disabled until files are added manually to `backend/geoip/`.

### 7. systemd Service (Production mode)

Creates `/etc/systemd/system/fluxio.service`:

```ini
[Unit]
Description=Flux.io Network Monitoring Platform
After=docker.service
Requires=docker.service

[Service]
Type=simple
WorkingDirectory=<absolute path of repo>
ExecStart=/usr/bin/docker compose up
ExecStop=/usr/bin/docker compose down
Restart=on-failure
RestartSec=10

[Install]
WantedBy=multi-user.target
```

Runs `systemctl daemon-reload && systemctl enable --now fluxio`.

### 8. Dev Mode — Host Toolchain

Installs Go 1.22 (tarball, `/usr/local/go`) and Node 20 (NodeSource) on the host, then starts containers and prints:

```
Dev environment ready.
  Run backend tests : cd backend && go test ./... -short
  Run frontend dev  : cd frontend && npm run dev
  Containers        : docker compose ps
```

### 9. Summary Screen (all modes)

```
╔══════════════════════════════════════╗
║   ✅  Flux.io is running!            ║
╚══════════════════════════════════════╝

  Dashboard : http://<HOST_IP>:80
  API health: http://<HOST_IP>:80/api/health
  NetFlow   : UDP <HOST_IP>:<NETFLOW_PORT>
  TZSP      : UDP <HOST_IP>:<TZSP_PORT>

Next steps:
  - Point your NetFlow exporter to <HOST_IP>:<NETFLOW_PORT>
  - Open the dashboard and configure DPI mode in Settings
  - Wazuh forwarding: set WAZUH_MANAGER_IP in .env and restart
```

---

## Files Changed

| File | Change |
|------|--------|
| `install.sh` | New — the installer script (~400 lines bash) |
| `.env.example` | New — documented template of all env vars |
| `docker-compose.yml` | Modified — replace hardcoded values with `${VAR}` references |

---

## Error Handling

- Every command that can fail is checked: `command || { echo "ERROR: ..."; exit 1; }` 
- Docker daemon not running after install → script starts it and retries once
- `docker compose up` fails → script prints the last 20 lines of logs and suggests `docker compose logs`
- Non-root execution → script re-executes itself with `sudo` or exits with a clear message

---

## Non-Goals

- No automated uninstaller (manual: `docker compose down && systemctl disable fluxio`)
- No upgrade path (re-run `git pull && ./install.sh` is sufficient)
- No quiet/non-interactive mode (not in scope for this iteration)
