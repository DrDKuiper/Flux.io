<div align="center">

# ⚡ Flux.io

**Open-source network monitoring platform — NetFlow, DPI, GeoIP enrichment, Suricata IDS integration and real-time dashboards, all in one stack.**

[![Go](https://img.shields.io/badge/Go-1.22-00ADD8?style=flat-square&logo=go)](https://go.dev)
[![React](https://img.shields.io/badge/React-18-61DAFB?style=flat-square&logo=react)](https://react.dev)
[![ClickHouse](https://img.shields.io/badge/ClickHouse-24.3-FFCC01?style=flat-square&logo=clickhouse)](https://clickhouse.com)
[![Docker](https://img.shields.io/badge/Docker-Compose-2496ED?style=flat-square&logo=docker)](https://docs.docker.com/compose)
[![License](https://img.shields.io/badge/License-MIT-green?style=flat-square)](LICENSE)

</div>

---

## Overview

Flux.io is a self-hosted network observability platform that collects raw traffic telemetry from your infrastructure and turns it into actionable intelligence — without sending a byte to the cloud.

| What it ingests | What it adds | Where it stores |
|---|---|---|
| NetFlow v9 / IPFIX from routers & switches | GeoIP country + ASN (MaxMind GeoLite2) | ClickHouse (columnar, fast aggregation) |
| TZSP-mirrored packet captures | TLS SNI, DNS queries, HTTP hostnames via DPI | PostgreSQL (settings & state) |
| Suricata `eve.json` alert stream | 5-tuple flow correlation | — |

---

## Architecture

```
Routers / Switches
       │ NetFlow v9 / IPFIX (UDP 2055)
       ▼
┌──────────────────────────────────────────────────────────┐
│                    Go Backend                            │
│                                                          │
│  NetFlow Decoder ──► Enrichment (GeoIP + DPI) ──► Batch │
│                                │                  Writer │
│  ┌────────── DPI Manager ──────┘                    │    │
│  │  ┌─ Suricata Correlator ◄── eve.json tailer      │    │
│  │  └─ TZSP Listener ◄──── mirrored traffic (37008) │    │
│  │         └── SNI / DNS / HTTP metadata             │    │
│  │                                                   ▼    │
│  └──────────────────────────────────────► ClickHouse     │
│                                                          │
│  Settings API (Fiber) ◄──────────────────► PostgreSQL    │
│  WebSocket /ws/alerts ◄── Suricata alerts ──────────     │
│       │                                                  │
│       └──► Wazuh syslog forwarder (RFC 3164, UDP 1514)  │
└──────────────────────────────────────────────────────────┘
       │ HTTP :8080
       ▼
React Dashboard (Vite + Tailwind + Recharts + Leaflet)
```

### Key design decisions

- **Two DPI modes, hot-swappable at runtime** — switch between *Suricata* (metadata from `eve.json` correlation) and *TZSP* (live packet mirroring) via the Settings UI without restarting services.
- **ClickHouse as the flow store** — `MergeTree` partitioned by day, ordered by `(src_ip, dst_ip, application_id, timestamp)` for sub-second aggregation at millions-of-flows scale.
- **`alertWriter` interface** — decouples the Suricata correlator from the storage layer; swappable for test fakes with no DI framework needed.
- **Graceful context propagation** — all goroutines receive a `pipelineCtx`; `SIGTERM` causes ordered shutdown without data loss.

---

## Tech Stack

| Layer | Technology |
|---|---|
| **Backend** | Go 1.22, [Fiber v2](https://github.com/gofiber/fiber), [goflow2](https://github.com/netsampler/goflow2), [gopacket](https://github.com/google/gopacket) |
| **Storage** | [ClickHouse 24.3](https://clickhouse.com) (flows + alerts), [PostgreSQL 16](https://postgresql.org) (settings) |
| **IDS** | [Suricata](https://suricata.io) — `eve.json` correlation + optional Wazuh forwarding |
| **GeoIP** | [MaxMind GeoLite2](https://dev.maxmind.com/geoip/geolite2-free-geolocation-data) City + ASN |
| **Frontend** | React 18, TypeScript, Vite 5, Tailwind CSS 3, Recharts, React-Leaflet |
| **Infrastructure** | Docker Compose, systemd (production), Suricata container |

---

## Quick Start

### One-line installer (Linux)

```bash
git clone https://github.com/DrDKuiper/Flux.io.git
cd Flux.io
chmod +x install.sh
sudo ./install.sh
```

The interactive wizard will:
1. Detect your distro (Debian/Ubuntu, RHEL/CentOS/Rocky/Alma, Fedora, Arch/Manjaro)
2. Install Docker if not present
3. Ask **Production** or **Development** mode
4. Walk you through every config value — press Enter to accept defaults
5. Check for port conflicts and offer alternatives
6. (Production) Download MaxMind GeoLite2 databases
7. (Production) Install and enable a `systemd` service for auto-start on boot
8. Print a summary with your dashboard URL

> **Supported distros:** Ubuntu 20.04+, Debian 11+, RHEL/CentOS 8+, Rocky Linux 8+, AlmaLinux 8+, Fedora 38+, Arch Linux, Manjaro

---

### Manual setup (Docker Compose)

```bash
# 1. Clone and configure
git clone https://github.com/DrDKuiper/Flux.io.git
cd Flux.io
cp .env.example .env
# Edit .env to set your passwords, ports, Wazuh IP, etc.

# 2. Start the stack
docker compose up -d

# 3. Open the dashboard
open http://localhost:80
```

---

## Configuration

All runtime configuration is driven by `.env` in the repo root. Copy `.env.example` to get started:

```bash
cp .env.example .env
```

| Variable | Default | Description |
|---|---|---|
| `PORT` | `80` | Host port for the backend HTTP server / dashboard |
| `POSTGRES_PASSWORD` | `fluxio_password` | PostgreSQL password |
| `POSTGRES_PORT` | `5432` | Host port PostgreSQL binds on |
| `NETFLOW_PORT` | `2055` | UDP port for NetFlow v9 / IPFIX |
| `TZSP_PORT` | `37008` | UDP port for TZSP-mirrored traffic |
| `SURICATA_EVE_LOG_PATH` | `/var/log/suricata/eve.json` | Path to Suricata eve.json |
| `WAZUH_MANAGER_IP` | *(blank)* | Wazuh server IP — leave empty to disable forwarding |
| `WAZUH_MANAGER_PORT` | `1514` | Wazuh syslog UDP port |
| `GEOIP_CITY_DB` | `/root/geoip/GeoLite2-City.mmdb` | Path to MaxMind City database |
| `GEOIP_ASN_DB` | `/root/geoip/GeoLite2-ASN.mmdb` | Path to MaxMind ASN database |
| `CLICKHOUSE_DSN` | `clickhouse://default:@clickhouse:9000/fluxio` | Override for non-default ClickHouse deployments |
| `POSTGRES_DSN` | *(auto)* | Override for host-mode dev; Compose reconstructs this automatically |

---

## Features

### Network Flow Collection
- **NetFlow v9 / IPFIX decoder** — parses and normalises flow records from any standard exporter
- **TZSP capture** — receives mirrored packets over UDP and extracts TLS SNI, DNS query names, and HTTP hostnames in real time via `gopacket`
- **Batch writer** — buffers enriched flows and flushes to ClickHouse in configurable batches for efficient bulk inserts

### DPI Enrichment
- **Hot-swappable DPI modes** — switch between Suricata correlation and live TZSP capture from the Settings UI without any service restart
- **5-tuple correlation cache** — TTL-keyed in-memory cache maps `(src_ip, dst_ip, src_port, dst_port, proto)` tuples to DPI metadata extracted from `eve.json`
- **GeoIP enrichment** — every flow annotated with country code, city, ASN number, and organisation name (requires free MaxMind account)

### Suricata IDS Integration
- **`eve.json` streaming** — tail-follows Suricata's live log with automatic retry on rotation or restart
- **Alert persistence** — Suricata alerts written to a dedicated `suricata_alerts` ClickHouse table with full five-tuple and signature metadata
- **Wazuh forwarding** — optionally forwards every Suricata alert as an RFC 3164 syslog message (PRI `<134>`, facility local0) to a Wazuh SIEM manager via UDP

### Dashboard & API
- **REST API** — Fiber-powered HTTP API (`/api/...`) for settings management and health checks
- **WebSocket alerts feed** — real-time Suricata alert stream at `ws://<host>/ws/alerts`
- **Settings page** — toggle DPI source (Suricata / TZSP) at runtime; persisted in PostgreSQL
- **React dashboard** — interactive charts (Recharts), geographic traffic map (Leaflet), and responsive layout (Tailwind CSS)

---

## Project Structure

```
Flux.io/
├── backend/
│   ├── cmd/server/
│   │   ├── main.go                     # Entry point, wires all goroutines
│   │   ├── settings_routes.go          # GET /api/settings, PUT /api/settings
│   │   └── main_test.go
│   ├── internal/
│   │   ├── collector/
│   │   │   ├── netflow.go              # UDP listener + goflow2 decode dispatch
│   │   │   ├── netflowv9/decoder.go    # NetFlow v9 template + data record parser
│   │   │   ├── filetailer.go           # tail -f semantics with retry on rotation
│   │   │   ├── eve.go                  # Suricata eve.json event parser
│   │   │   ├── suricata_correlator.go  # Feeds eve events into cache + storage
│   │   │   ├── tzsp.go                 # TZSP packet dissection (SNI, DNS, HTTP)
│   │   │   ├── dpi_manager.go          # Hot-swap between Suricata and TZSP modes
│   │   │   └── wazuh_forwarder.go      # RFC 3164 syslog UDP forwarder
│   │   ├── processor/
│   │   │   ├── types.go                # Shared flow / alert / DPI types
│   │   │   ├── enrichment.go           # GeoIP + DPI metadata injection
│   │   │   └── correlation.go          # 5-tuple TTL cache
│   │   ├── settings/
│   │   │   └── repository.go           # Postgres-backed DPI mode store
│   │   └── storage/
│   │       ├── clickhouse.go           # ClickHouse connection + schema helpers
│   │       └── batch_writer.go         # Buffered async write with flush interval
│   ├── Dockerfile
│   └── go.mod
├── frontend/
│   └── src/
│       ├── App.tsx                     # Router + layout
│       └── pages/Settings.tsx          # DPI mode toggle UI
├── db/
│   ├── clickhouse/init-db.sql          # network_flows + suricata_alerts tables
│   └── postgres/init-db.sql            # settings table
├── docker-compose.yml                  # Full stack definition
├── install.sh                          # Interactive Linux installer wizard
└── .env.example                        # Documented env var template
```

---

## Development

### Prerequisites

- Go 1.22+
- Node 20+
- Docker + Docker Compose v2

### Running locally

```bash
# Start infrastructure only (ClickHouse, Postgres, Suricata)
docker compose up -d clickhouse postgres suricata

# Run the backend
cd backend
go run ./cmd/server

# Run the frontend dev server
cd frontend
npm install
npm run dev
```

### Running tests

```bash
cd backend
go test ./... -short -v
```

### Switching DPI mode at runtime

```bash
# Switch to TZSP live capture
curl -X PUT http://localhost:80/api/settings \
  -H 'Content-Type: application/json' \
  -d '{"dpi_mode":"tzsp"}'

# Switch back to Suricata correlation
curl -X PUT http://localhost:80/api/settings \
  -H 'Content-Type: application/json' \
  -d '{"dpi_mode":"suricata"}'
```

No restart required — the `DPIManager` tears down the old listener and starts the new one in place.

---

## Production Deployment

The `./install.sh` wizard handles everything automatically. Below are the manual steps for reference.

### systemd service

```ini
# /etc/systemd/system/fluxio.service
[Unit]
Description=Flux.io Network Monitoring Platform
After=docker.service
Requires=docker.service

[Service]
Type=simple
WorkingDirectory=/opt/Flux.io
ExecStart=/usr/bin/docker compose up
ExecStop=/usr/bin/docker compose down
Restart=on-failure
RestartSec=10

[Install]
WantedBy=multi-user.target
```

```bash
systemctl enable --now fluxio
```

### GeoIP databases

A free [MaxMind account](https://www.maxmind.com/en/geolite2/signup) is required. The installer handles the download in production mode. For manual setup:

```bash
# Place .mmdb files in backend/geoip/ — the directory is bind-mounted into the container
mkdir -p backend/geoip

curl -L "https://download.maxmind.com/app/geoip_download?edition_id=GeoLite2-City&license_key=YOUR_KEY&suffix=tar.gz" \
  | tar -xzO --wildcards '*.mmdb' > backend/geoip/GeoLite2-City.mmdb

curl -L "https://download.maxmind.com/app/geoip_download?edition_id=GeoLite2-ASN&license_key=YOUR_KEY&suffix=tar.gz" \
  | tar -xzO --wildcards '*.mmdb' > backend/geoip/GeoLite2-ASN.mmdb
```

### NetFlow exporter setup

Point your router/switch at the Flux.io server IP on UDP port 2055 (or your configured `NETFLOW_PORT`).

Example — MikroTik RouterOS:

```
/ip traffic-flow set enabled=yes
/ip traffic-flow target add dst-address=<FLUXIO_IP> port=2055 version=9
```

Example — Cisco IOS:

```
ip flow-export destination <FLUXIO_IP> 2055
ip flow-export version 9
interface GigabitEthernet0/0
  ip flow ingress
  ip flow egress
```

### Wazuh SIEM integration

Set `WAZUH_MANAGER_IP` in `.env` to your Wazuh server's address. Flux.io will forward every Suricata alert as an RFC 3164 syslog datagram tagged `fluxio-suricata:` on UDP port 1514.

To create a custom decoder in Wazuh (`/var/ossec/etc/decoders/local_decoder.xml`):

```xml
<decoder name="fluxio-suricata">
  <prematch>fluxio-suricata: </prematch>
  <regex>fluxio-suricata: \.+</regex>
</decoder>
```

---

## API Reference

| Method | Endpoint | Description |
|---|---|---|
| `GET` | `/api/health` | Health check — returns `{"status":"ok"}` |
| `GET` | `/api/settings` | Read current DPI mode |
| `PUT` | `/api/settings` | Update DPI mode (`"suricata"` or `"tzsp"`) |
| `GET` | `/ws/alerts` | WebSocket stream of live Suricata alerts |

---

## Updating

To apply a new version to an existing installation:

```bash
cd /path/to/Flux.io
git pull
sudo ./install.sh
```

The wizard detects your existing `.env` and asks `[K]eep / [O]verwrite / [E]dit` — press **Enter** to keep your current config. All steps are idempotent: Docker, Go, Node, and the systemd service are skipped if already up to date.

---

## Uninstalling

```bash
sudo ./uninstall.sh
```

Three modes:

| Mode | What is removed |
|---|---|
| **Services only** | systemd units (`fluxio.service`, `fluxio-geoip-update.timer`), update script |
| **Full uninstall** | Everything above + containers, Docker volumes (all data), logs, GeoIP files, `.env`, firewall rules |
| **Custom** | Step-by-step confirmation for each component |

Non-interactive (CI / automation):

```bash
# Full uninstall with no prompts
sudo ./uninstall.sh --yes
```

The uninstaller always saves a timestamped `.env.bak.YYYYMMDDHHMMSS` backup before removing the config file.

---

## Roadmap

- [ ] Grafana dashboard integration (pre-built dashboards for ClickHouse)
- [ ] NetFlow v5 support
- [ ] Alert rules engine with custom thresholds
- [ ] Multi-tenant / RBAC settings API
- [ ] HTTPS / TLS termination built into the installer
- [ ] Quiet/non-interactive install mode (`./install.sh --yes`)

---

## Contributing

Pull requests are welcome. For major changes, please open an issue first to discuss what you'd like to change.

1. Fork the repo
2. Create a feature branch: `git checkout -b feature/my-feature`
3. Commit following [Conventional Commits](https://www.conventionalcommits.org): `feat:`, `fix:`, `chore:`, `docs:`
4. Push the branch and open a PR against `main`

---

## License

[MIT](LICENSE) — © 2026 Flux.io contributors
