# Pemmece

Pemmece is a small self-hosted customer support ticketing system for small
teams. It is one Go binary with SQLite storage, embedded web assets, registered
customers, staff tools, no-reply email notifications, webhooks, and an audit log.

The project is intentionally simple: no external database, no separate frontend
build, and no inbound email processing.

## Project Status

Current target: `v0.1.0-alpha` (see [VERSION](./VERSION)).

Pemmece is intended for small-team self-hosting and public audit. It is not yet
externally security audited, and the API/schema should be considered unstable
until a non-alpha release.

## Features

- Products group tickets by service, customer, or team.
- Customers and staff use the same UI with role-based actions.
- Ticket workflow: New, Assigned, Resolved, Rejected.
- Public replies, internal notes, assignees, priorities, filtering, and sorting.
- Admin-created accounts with one-time setup/reset links.
- SMTP-backed no-reply notifications with a durable SQLite outbox.
- API tokens, webhooks, and admin audit events.

## Requirements

- Go 1.26+
- SQLite is embedded through the Go driver; no database server is required.
- Optional for E2E tests: Node, OpenSSL, and Chromium.

## Run Locally

Browser sessions require HTTPS because cookies are marked `Secure`.

```sh
openssl req -x509 -newkey rsa:2048 -nodes \
  -keyout localhost-key.pem \
  -out localhost.pem \
  -days 365 \
  -subj /CN=127.0.0.1 \
  -addext subjectAltName=IP:127.0.0.1,DNS:localhost

go run ./cmd/pemmece \
  -tls-cert ./localhost.pem \
  -tls-key ./localhost-key.pem
```

Open `https://127.0.0.1:8388` and create the first admin account.

For persistent local configuration:

```sh
cp .env.example .env
go run ./cmd/pemmece
```

## Configuration

Every runtime option is available as a flag and as an environment variable.
Pemmece loads a repo-local `.env` file when present; existing process
environment variables take precedence.

Important values:

- `PEMMECE_ADDR`: listen address, default `127.0.0.1:8388`
- `PEMMECE_DB`: SQLite database path, default `./pemmece.db`
- `PEMMECE_TLS_CERT` / `PEMMECE_TLS_KEY`: HTTPS certificate and key
- `PEMMECE_PUBLIC_URL`: public HTTPS URL used in emails
- `PEMMECE_SESSION_TTL`: browser session lifetime, default `336h`
- `PEMMECE_BRAND_NAME`: display name for the deployed instance
- `PEMMECE_UPLOAD_DIR`: directory for ticket attachments
- `PEMMECE_BACKUP_DIR`: directory where backup snapshots are stored

Use [.env.example](./.env.example) as the complete reference.

## Branding

Set `PEMMECE_BRAND_NAME`, `PEMMECE_BRAND_SUBTITLE`, `PEMMECE_BRAND_MARK`, and
`PEMMECE_BRAND_COLOR` to brand a deployment, for example `lallero.dev`.
Branding changes the visible instance identity without changing the software
name or requiring a custom build.

## Attachments

Ticket descriptions and replies can include files. Files are stored on disk in
`PEMMECE_UPLOAD_DIR`; SQLite stores only metadata and access rules. Back up the
database and upload directory together.

## Backup And Restore

Backups are local snapshots of the SQLite database plus the upload directory.
The backup script uses SQLite's online backup command, so it can run while
Pemmece is running.

```sh
scripts/backup.sh
```

This creates `PEMMECE_BACKUP_DIR/<timestamp>/` with `pemmece.db`,
`uploads.tar`, and a small manifest. The admin Maintenance page shows the backup
directory and latest detected backup.

Stop Pemmece before restoring:

```sh
scripts/restore.sh pemmece-backups/20260101T120000Z
```

Use `scripts/restore.sh latest` to restore the newest snapshot. The restore
script moves the current database, WAL/SHM files, and upload directory into a
`restore-pre-<timestamp>` folder before replacing them.

## Email

Pemmece only sends no-reply email. It does not receive or parse replies.

Enable SMTP with:

```env
PEMMECE_EMAIL_NOTIFICATIONS=true
PEMMECE_PUBLIC_URL=https://support.example.com
PEMMECE_SMTP_HOST=smtp.example.com
PEMMECE_SMTP_PORT=587
PEMMECE_SMTP_USER=pemmece
PEMMECE_SMTP_PASSWORD=secret
PEMMECE_SMTP_FROM=no-reply@example.com
PEMMECE_SMTP_TLS_MODE=starttls
```

Ticket notifications are queued in SQLite and coalesced for
`PEMMECE_EMAIL_BATCH_DELAY` before sending. Admins can inspect the outbox, send a
test email, and retry failures from the admin page.

## Webhooks And API

API access uses either the web session cookie or an API token:

```sh
curl -H "Authorization: Bearer pme_..." https://127.0.0.1:8388/api/tickets
```

Cookie-backed mutating requests must include the `X-Pemmece-CSRF` token returned
by `GET /api/session`.

Webhook payloads are signed with `X-Pemmece-Signature`. Supported ticket events:

- `ticket.created`
- `ticket.updated`
- `ticket.commented`
- `ticket.assigned`

Webhook URLs must be HTTPS and public by default. Development-only escape
hatches are available with `PEMMECE_ALLOW_INSECURE_WEBHOOKS` and
`PEMMECE_ALLOW_PRIVATE_WEBHOOKS`.

## Tests

```sh
go test ./...
npm run test:e2e
```

The E2E smoke test starts an isolated HTTPS Pemmece instance with a temporary
SQLite database and fake SMTP server, then drives Chromium through the core
customer/staff ticket flow. Set `PEMMECE_E2E_CHROMIUM=/path/to/chromium` if
Chromium is not at `/usr/bin/chromium`.

## Contributing

Keep changes small and focused. Run the tests above before opening a pull
request. Do not commit `.env`, SQLite databases, certificates, or SMTP secrets.

## License

Pemmece is released under the MIT License. See [LICENSE](./LICENSE).

Copyright 2026 Paolo Marrone, [lallero.dev](https://lallero.dev).
