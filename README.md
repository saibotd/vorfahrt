# Vorfahrt

Vorfahrt is a simple, queueing SMTP server written in Go. It is designed to run in a Docker environment, particularly alongside Traefik. It provides a minimal Web UI for managing sender accounts and inspecting the outbound queue.

## Features

- **SMTP Server**: Accepts incoming emails and stores them in a local SQLite database.
- **Queueing & Delivery**: A background worker automatically attempts to deliver queued emails to their destination MX servers (with up to 5 retries).
- **Traefik ACME Integration**: Automatically reads and watches Traefik's `acme.json` to secure the SMTP connection (STARTTLS) and validate allowed `MAIL FROM` sender domains.
- **DKIM Signing**: Automatically generates a DKIM key and signs all outgoing messages to improve deliverability.
- **Web UI**: A simple interface to add/delete SMTP sender credentials, view the email queue, and check DNS configuration status (SPF, DKIM, DMARC) for your domains.

## Quick Start (Docker Compose)

Vorfahrt is best run via Docker Compose alongside Traefik. A sample `docker-compose.yml` is provided.

1. Ensure your Traefik instance is saving its certificates to a file named `acme.json`.
2. Configure the `docker-compose.yml` to mount the `acme.json` file into Vorfahrt:
   ```yaml
   volumes:
     - "/path/to/your/traefik/acme.json:/data/acme.json:ro"
     - "./data:/data" # Persist SQLite DB and DKIM key
   ```
3. Start the service:
   ```bash
   docker compose up -d
   ```
4. Access the Web UI at http://localhost:8080 (or your configured Traefik domain).

## Environment Variables

| Variable | Default | Description |
|---|---|---|
| `ACME_FILE` | | Path to Traefik's `acme.json`. Used for TLS certs and sender domain validation. |
| `DATA_DIR` | `data` | Directory where the SQLite database and DKIM key are stored. |
| `DKIM_KEY_FILE` | `data/dkim.key` | Path to the RSA private key used for DKIM signing. Auto-generated if missing. |
| `DKIM_SELECTOR` | `mail` | The DKIM selector used in DNS records. |
| `WEB_ADDR` | `:8080` | Port for the Web UI and API. |
| `WEB_USERNAME` | `admin` | Username for basic auth on the Web UI. |
| `WEB_PASSWORD` | | Password for basic auth on the Web UI. If empty, auth is disabled. |
| `SMTP_ADDR` | `:25` | Port for the SMTP server. |
| `SMTP_DOMAIN` | `localhost` | The EHLO domain announced by the server. |
| `SMTP_IMPLICIT_TLS`| `false` | If `true`, requires TLS from the first byte (usually for port 465). If `false`, uses STARTTLS. |

## DNS Configuration

To ensure your emails do not end up in spam, you need to configure SPF, DKIM, and DMARC records for your domains. 

The Vorfahrt Web UI automatically checks these DNS records for all domains found in your `acme.json` and provides the exact `TXT` records you need to copy/paste into your DNS provider's dashboard.

## Local Development

To run the server locally for testing (without root privileges):

```bash
# Create a dummy acme.json or use a test file
SMTP_ADDR=":2525" WEB_ADDR=":8080" ACME_FILE="_testdata/acme.json" go run .
```

You can test sending an email through it using `swaks`:
```bash
swaks --to recipient@example.com --from your-domain@example.com --server localhost --port 2525 --auth LOGIN --auth-user sender --auth-password secret
```
*(Ensure the sender account is created in the UI first!)*
