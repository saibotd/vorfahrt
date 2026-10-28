# AGENTS.md

Developer and agent guide for the **Vorfahrt** codebase.

## Overview

Vorfahrt is a lightweight, self-hosted queueing SMTP server written in Go. It is designed to run in Docker alongside Traefik, utilizing Traefik's `acme.json` storage for TLS certificates and sender domain discovery.

It embeds a minimal web interface for managing sender accounts, viewing outbound mail queues, verifying server Forward-Confirmed Reverse DNS (FCrDNS), and inspecting DNS records (SPF, DKIM, DMARC) for each configured domain.

## Tech Stack & Architecture

- **Language**: Go (v1.21+ / v1.26+)
- **Database**: SQLite via `github.com/glebarez/sqlite` (pure Go, CGO-free) and `gorm.io/gorm`.
- **SMTP Protocol**: `github.com/emersion/go-smtp` with SASL authentication via `github.com/emersion/go-sasl`.
- **DKIM Signing**: `github.com/emersion/go-msgauth/dkim`.
- **File Watching**: `github.com/fsnotify/fsnotify`.
- **Web UI**: Embedded single-file HTML/CSS/Vanilla JS via `embed.FS` (`ui/index.html`).

## Directory Layout

```
├── Dockerfile             # Multi-stage CGO-free Alpine build
├── docker-compose.yml     # Sample deployment with Traefik
├── main.go                # Application entrypoint, server initializations, HTTP routing
├── pkg/
│   ├── acme/              # Traefik acme.json parser, TLS cert loader, and file watcher
│   ├── models/            # GORM database models (Account, Message)
│   ├── queue/             # Background queue worker, MX resolution, DKIM generation/signing
│   ├── smtp/              # SMTP backend session, SASL PLAIN/LOGIN auth, sender domain validation
│   └── web/               # Web server routes, accounts, domains, server status, and UI handlers
├── ui/
│   ├── index.html         # Embedded HTML template
│   ├── script.js          # Client-side logic and API calls
│   ├── style.css          # Styling for Web UI
│   └── ui.go              # Embedded filesystem definition (ui.FS)
└── _testdata/
    └── acme.json          # Sample Traefik certificate storage for local testing
```

## Key Components & Rules

### 1. ACME Integration (`pkg/acme`)
- Reads Traefik's `acme.json`, decoding base64-encoded certificates and private keys.
- Populates `tls.Config` with SNI certificate resolution and maintains a list of valid domains.
- **Reloading**: Traefik updates `acme.json` directly. Vorfahrt watches both the file and parent directory with `fsnotify`. Additionally, `acme.json` is automatically reloaded whenever the Web UI index (`/`) or `/api/domains` endpoint is accessed, ensuring new domains appear immediately without server restart.

### 2. Sender Accounts & Domain Tethering (`pkg/models`, `pkg/smtp`, `pkg/web`)
- **Tethering**: Every sender account (`Account`) is strictly tethered to one domain (`acc.Domain`).
- **Sender Validation**: During SMTP `MAIL FROM`, the authenticated account is verified against its tethered domain. Attempting to send from another domain is rejected with `Sender domain not allowed for this account`.
- **Passwords**:
  - Passwords are automatically generated (24-character cryptographic alphanumeric string via `crypto/rand`).
  - Passwords **cannot** be changed or customized. Any password provided during account creation is ignored.
  - The UI provides a one-click copy button to copy the generated credentials.

### 3. Queue & DKIM Signing (`pkg/queue`)
- Outgoing messages submitted via SMTP are stored in the SQLite database with `status = "queued"`.
- The background `Worker` scans for queued messages every 5 seconds.
- Outgoing messages are signed using the system DKIM key and configured selector (`DKIM_SELECTOR`).
- The worker announces `EHLO <SMTP_DOMAIN>`, negotiates `STARTTLS` with remote MX servers if supported, and iterates through discovered MX candidates. Failed attempts increment `attempts` up to 5 retries before marking as `failed`. Once sent, message raw data is purged.
- **Retention Cleanup**: Expired messages older than `QUEUE_RETENTION` (default `24h`) are automatically purged from the SQLite database every minute. Set to `0` to disable.

### 4. Server Identity, Connectivity & FCrDNS Verification (`main.go`, `ui/index.html`)
- Multi-tenant mail servers verify rDNS and network readiness at the server infrastructure level:
  - `SMTP_DOMAIN` defines the canonical hostname (e.g., `mail.saibotd.com`).
  - `/api/server-status` resolves `SMTP_DOMAIN` $\rightarrow$ IP, then queries reverse DNS `IP` $\rightarrow$ PTR, verifying FCrDNS.
  - Tests outbound TCP port 25 connectivity (via dedicated firewall test host `portquiz.net:25` to avoid triggering spam filters on real mail providers, with graceful RFC-compliant fallback and 10-minute caching). Warns when outbound port 25 is blocked by a VPS host.
  - Per-tenant domains only require SPF (`v=spf1 a:<SMTP_DOMAIN> mx ~all`), DKIM, and DMARC.

### 5. Database Migrations
- GORM auto-migrates `Account` and `Message` tables at startup (`main.go`).

## Environment Variables

| Variable | Default | Purpose |
|---|---|---|
| `ACME_FILE` | *(none)* | Path to Traefik's `acme.json`. |
| `DATA_DIR` | `data` | Directory where SQLite (`data.db`) and DKIM key (`dkim.key`) reside. |
| `DKIM_KEY_FILE` | `<DATA_DIR>/dkim.key` | RSA private key file for DKIM. Auto-generated if missing. |
| `DKIM_SELECTOR` | `mail` | DKIM DNS selector (e.g., `mail._domainkey.<domain>`). |
| `QUEUE_RETENTION` | `24h` | Duration after which messages are deleted (e.g. `24h`, `48h`, `7d`). Set to `0` to disable. |
| `WEB_ADDR` | `:8080` | HTTP listen address for Web UI and API. |
| `WEB_USERNAME` | `admin` | HTTP Basic Auth username for Web UI. |
| `WEB_PASSWORD` | *(empty)* | HTTP Basic Auth password. If unset, auth is disabled. |
| `SMTP_ADDR` | `:25` | SMTP listen address. |
| `SMTP_DOMAIN` | `localhost` | HELO/EHLO hostname announced by the SMTP server. |
| `SMTP_IMPLICIT_TLS`| `false` | Set to `true` for implicit TLS (port 465). Defaults to STARTTLS. |

## Development & Testing

### Local Run
```bash
# Create local data dir and run with sample acme file
mkdir -p data
SMTP_ADDR=":2525" WEB_ADDR=":8080" ACME_FILE="_testdata/acme.json" go run .
```

### Testing SMTP with STARTTLS & Authentication
```bash
python3 -c '
import smtplib, ssl
s = smtplib.SMTP("127.0.0.1", 2525)
s.starttls(context=ssl._create_unverified_context())
s.login("username", "generated_password")
s.sendmail("sender@tethered-domain.com", ["recipient@example.com"], "Subject: Test\n\nHello")
s.quit()
'
```

### Build Check
Ensure pure Go compatibility (no CGO required):
```bash
CGO_ENABLED=0 go build -o vorfahrt .
```
