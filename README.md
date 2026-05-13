# Pemmece

Pemmece is a small Go issue tracker inspired by MantisBT and the local-first
operational feel of the Syncthing web UI.

It is still a single binary, but now includes the core pieces needed for a
usable self-hosted tracker:

- First-run admin setup, secure login sessions, staff/client roles, project
  roles, and API tokens.
- Mantis-like project issues with statuses, severity, priority, assignees,
  reporters, tags, comments, and filtering.
- SQLite-backed local persistence.
- Embedded web assets served by the Go process.
- Global and project webhooks with `X-Pemmece-Signature`.
- SQLite-backed email notification outbox with SMTP delivery.
- Registered-client support portal for no-reply customer tickets.
- Per-project Git repository scanning that links commits mentioning `#123` or
  `{PROJECTKEY}-123` to matching issues.

## Run

Browser sessions require HTTPS because session cookies are always marked
`Secure`. For local development, run with a certificate:

```sh
go run ./cmd/pemmece -tls-cert ./localhost.pem -tls-key ./localhost-key.pem
```

Open https://127.0.0.1:8388 and create the first admin user.
Admins can create client users, add them to projects as reporters, and those
clients can submit and follow support tickets at `/support`.

Useful flags:

```sh
go run ./cmd/pemmece \
  -addr 0.0.0.0:8388 \
  -db ./pemmece.db \
  -tls-cert ./localhost.pem \
  -tls-key ./localhost-key.pem \
  -repo-root /home/me/repos
```

The same values can be supplied with `PEMMECE_ADDR`, `PEMMECE_DB`,
`PEMMECE_TLS_CERT`, `PEMMECE_TLS_KEY`, and `PEMMECE_REPO_ROOTS`.

Email notifications are enabled when SMTP is configured. The app enqueues email
jobs durably in SQLite when issue events happen, then a background worker sends
them with retry/backoff.

```sh
go run ./cmd/pemmece \
  -public-url https://tracker.example.test \
  -smtp-host smtp.example.test \
  -smtp-port 587 \
  -smtp-user pemmece \
  -smtp-password secret \
  -smtp-from tracker@example.test
```

Equivalent environment variables are `PEMMECE_PUBLIC_URL`,
`PEMMECE_EMAIL_NOTIFICATIONS`, `PEMMECE_SMTP_HOST`, `PEMMECE_SMTP_PORT`,
`PEMMECE_SMTP_USER`, `PEMMECE_SMTP_PASSWORD`, `PEMMECE_SMTP_FROM`, and
`PEMMECE_SMTP_TLS_MODE` (`starttls`, `tls`, or `none`).

Webhook delivery defaults are conservative: webhook URLs must be HTTPS and must
not resolve to private, loopback, or link-local addresses. Development-only
escape hatches are available with `-allow-insecure-webhooks` and
`-allow-private-webhooks`.

## API

Authentication works with the web session cookie or an API token. Cookie-backed
mutating requests must include the `X-Pemmece-CSRF` value returned by login or
`GET /api/session`.

```sh
curl -H "Authorization: Bearer pme_..." http://127.0.0.1:8388/api/issues
```

Core endpoints:

- `GET /api/health`
- `GET /api/session`
- `POST /api/setup`
- `POST /api/login`
- `POST /api/logout`
- `GET /api/support/projects`
- `POST /api/support/tickets`
- `GET /api/support/tickets/{token}`
- `POST /api/support/tickets/{token}/comments`
- `GET /api/projects`
- `POST /api/projects`
- `GET /api/projects/{id}`
- `PATCH /api/projects/{id}`
- `DELETE /api/projects/{id}`
- `GET /api/projects/{id}/members`
- `POST /api/projects/{id}/members`
- `DELETE /api/projects/{id}/members/{user_id}`
- `GET /api/projects/{id}/issues`
- `POST /api/projects/{id}/issues`
- `GET /api/issues`
- `POST /api/issues`
- `GET /api/issues/{id}`
- `PATCH /api/issues/{id}`
- `POST /api/issues/{id}/comments`
- `GET /api/issues/{id}/commits`
- `GET /api/users`
- `POST /api/users`
- `PATCH /api/users/{id}`
- `DELETE /api/users/{id}`
- `GET /api/tokens`
- `POST /api/tokens`
- `DELETE /api/tokens/{id}`
- `GET /api/webhooks`
- `POST /api/webhooks`
- `GET /api/projects/{id}/webhooks`
- `POST /api/projects/{id}/webhooks`
- `PATCH /api/webhooks/{id}`
- `DELETE /api/webhooks/{id}`
- `POST /api/webhooks/{id}/test`
- `GET /api/webhook-deliveries`
- `GET /api/email-notifications`
- `GET /api/projects/{id}/webhook-deliveries`
- `GET /api/projects/{id}/repo`
- `PATCH /api/projects/{id}/repo`
- `POST /api/projects/{id}/repo/scan`

Webhook events:

- `issue.created`
- `issue.updated`
- `issue.commented`
- `issue.assigned`
- `repo.scanned`
