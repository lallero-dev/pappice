<p align="center">
  <img src="./internal/server/web/static/logo.svg" alt="Pappice logo" width="96">
</p>

# Pappice

Pappice is a small, self-hosted, chat-style support desk. It runs as one Go
binary with SQLite and embedded web assets. Customers open tickets from the
portal; staff reply, assign, filter, and track them.

![Pappice chat-style ticketing demo](./assets/demo.gif)

It includes registered customers, staff tools, attachments, unread state,
no-reply email notifications, webhooks, and audit logging. It does not require
an external database or frontend build step, and it does not process inbound
email.

## Project Status

Alpha. Pappice is not externally security audited. The API and schema may change
before a stable release; existing installations can require `pappice db migrate`
after a backup.

## Features

- Products group tickets by service, customer, or team.
- Customers and staff use the same UI with role-based actions.
- Ticket workflow: New, Assigned, Resolved, Rejected.
- Chat-style conversations with public replies, internal notes, unread state,
  assignees, priorities, filtering, and sorting.
- Drag/drop and pasted attachments, with inline image previews.
- Admin-created accounts with one-time setup/reset links or manual passwords.
- SMTP-backed no-reply notifications with a durable SQLite outbox.
- API tokens, webhooks, admin audit events, and a maintenance overview.

## Requirements

- Go 1.26+
- SQLite is embedded through the Go driver; no database server is required.
- Optional for E2E tests: Node, OpenSSL, and Chromium.
- Optional for regenerating the README demo GIF: `ffmpeg`.

## Run Locally

Browser sessions require HTTPS because cookies are marked `Secure`.

```sh
openssl req -x509 -newkey rsa:2048 -nodes \
  -keyout localhost-key.pem \
  -out localhost.pem \
  -days 365 \
  -subj /CN=127.0.0.1 \
  -addext subjectAltName=IP:127.0.0.1,DNS:localhost

go run ./cmd/pappice serve \
  -tls-cert ./localhost.pem \
  -tls-key ./localhost-key.pem
```

Open `https://127.0.0.1:8388` and create the first admin account.

For persistent local configuration:

```sh
cp .env.example .env
go run ./cmd/pappice serve
```

## Configuration

Every runtime option is available as a flag and an environment variable.
Pappice loads `.env` from the working directory when present; process
environment variables win.

Important values:

- `PAPPICE_ADDR`: listen address, default `127.0.0.1:8388`
- `PAPPICE_DB`: SQLite database path, default `./pappice.db`
- `PAPPICE_TLS_CERT` / `PAPPICE_TLS_KEY`: HTTPS certificate and key
- `PAPPICE_PUBLIC_URL`: public HTTPS URL used in emails
- `PAPPICE_NOTIFICATION_DELAY`: ticket notification delay, default `30s`
- `PAPPICE_DOMAIN_EVENT_RETENTION`: processed event retention, default `720h`;
  `0` disables pruning
- `PAPPICE_SESSION_TTL`: browser session lifetime, default `336h`
- `PAPPICE_BRAND_NAME`: display name for the deployed instance
- `PAPPICE_UPLOAD_DIR`: directory for ticket attachments
- `PAPPICE_BACKUP_DIR`: directory where backup snapshots are stored

Use [.env.example](./.env.example) as the complete reference.

Useful local commands:

```sh
pappice db status
pappice db migrate --dry-run
pappice db migrate
pappice doctor
pappice version
pappice serve -h
```

`pappice serve` initializes a brand-new database, but it does not run migrations
for an existing database. If the schema is behind, run a backup, then
`pappice db migrate --dry-run`, then `pappice db migrate`.

`pappice doctor` validates paths, TLS, public URL, SMTP, upload limits, rate
limits, and development-only webhook settings.

## Branding

Set `PAPPICE_BRAND_NAME`, `PAPPICE_BRAND_SUBTITLE`, `PAPPICE_BRAND_MARK`, and
`PAPPICE_BRAND_COLOR` to brand a deployment without rebuilding the binary.

## Attachments

Ticket descriptions and replies can include files. Files are stored on disk in
`PAPPICE_UPLOAD_DIR`; SQLite stores only metadata and access rules. The UI
supports file picking, drag/drop, and paste. Back up the database and upload
directory together.

## Backup And Restore

Backups are local snapshots of the SQLite database plus the upload directory.
The backup script uses SQLite's online backup command, so it can run while
Pappice is running.

```sh
ops/backup.sh
```

This creates `PAPPICE_BACKUP_DIR/<timestamp>/` with `pappice.db`,
`uploads.tar`, and a manifest. The admin Maintenance page shows the backup
directory and latest detected backup.

Stop Pappice before restoring:

```sh
ops/restore.sh pappice-backups/20260101T120000Z
```

Use `ops/restore.sh latest` to restore the newest snapshot. The restore
script moves the current database, WAL/SHM files, and upload directory into a
`restore-pre-<timestamp>` folder before replacing them.

## Deployment

Production templates for `systemd`, nginx, and `/etc/pappice/pappice.env` live
in [deploy/](./deploy/README.md). The default production shape is public HTTPS
in nginx and local HTTPS from nginx to Pappice on `127.0.0.1:8388`.

## Email

Pappice only sends no-reply email. It does not receive or parse replies.

Enable SMTP with:

```env
PAPPICE_EMAIL_NOTIFICATIONS=true
PAPPICE_PUBLIC_URL=https://support.example.com
PAPPICE_SMTP_HOST=smtp.example.com
PAPPICE_SMTP_PORT=587
PAPPICE_SMTP_USER=pappice
PAPPICE_SMTP_PASSWORD=secret
PAPPICE_SMTP_FROM=no-reply@support.example.com
PAPPICE_SMTP_TLS_MODE=starttls
```

Ticket notifications are queued in SQLite. Email and webhook delivery waits for
`PAPPICE_NOTIFICATION_DELAY`; pending updates for the same ticket are coalesced.
Admins can inspect the email outbox, send a test email, and retry failures from
the admin page.

## Webhooks And API

API access uses either the web session cookie or an API token:

```sh
curl -H "Authorization: Bearer pap_..." https://127.0.0.1:8388/api/tickets
```

Cookie-backed mutating requests must include the `X-Pappice-CSRF` token returned
by `GET /api/session`.

Webhook payloads are signed with `X-Pappice-Signature`. Supported ticket events:

- `ticket.created`
- `ticket.updated`
- `ticket.commented`
- `ticket.assigned`

Webhook secrets are created in `Admin -> Global Webhooks` or
`Products -> Webhooks`. Leave the secret field empty to let Pappice generate
one, then store the one-time value shown after creation or rotation.

Webhook URLs must be HTTPS and public by default. Development-only escape
hatches are available with `PAPPICE_ALLOW_INSECURE_WEBHOOKS` and
`PAPPICE_ALLOW_PRIVATE_WEBHOOKS`.

## Tests

```sh
go test ./...
npm run test:e2e
```

The E2E smoke test starts an isolated HTTPS Pappice instance with a temporary
SQLite database and fake SMTP server, then drives Chromium through the core
customer/staff ticket flow. Set `PAPPICE_E2E_CHROMIUM=/path/to/chromium` if
Chromium is not at `/usr/bin/chromium`.

Regenerate the README demo GIF with:

```sh
npm run demo:gif
```

Run the small memory benchmark with `npm run bench:small`. On this development
machine, the default scenario reported about 22 MiB RSS mean and 23 MiB RSS max
for 2 products, 2 staff sessions, 8 customer sessions, and 24 tickets. Treat
this as an indicative local measurement; compare runs on the same host.

## Contributing

Keep changes small and focused. Run the tests above before opening a pull
request.

## License

Pappice is released under the GNU General Public License v3.0 only
(`GPL-3.0-only`). See [LICENSE](./LICENSE).

Copyright 2026 Paolo Marrone.
