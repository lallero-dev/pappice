# Pemmece

Pemmece is a small self-hosted customer support ticketing system. It is a
single Go binary with SQLite persistence, embedded web assets, staff tools, and
a registered-client support portal.

Current focus:

- First-run admin setup, secure login sessions, staff/client roles, product
  roles, and API tokens.
- Customer tickets with statuses, priorities, assignees, requesters, tags,
  public replies, internal notes, and filtering.
- Registered-client support portal at `/support`.
- SQLite-backed local persistence for multiuser deployments.
- Global and product webhooks with `X-Pemmece-Signature`.
- SQLite-backed no-reply email notification outbox with SMTP delivery.

## Run

Browser sessions require HTTPS because session cookies are always marked
`Secure`. For local development, run with a certificate:

```sh
go run ./cmd/pemmece -tls-cert ./localhost.pem -tls-key ./localhost-key.pem
```

Open `https://127.0.0.1:8388` and create the first admin user. Admins can create
client users, add them to products as customers, and those clients can submit and
follow support tickets at `/support`.

Useful flags:

```sh
go run ./cmd/pemmece \
  -addr 0.0.0.0:8388 \
  -db ./pemmece.db \
  -tls-cert ./localhost.pem \
  -tls-key ./localhost-key.pem
```

The same values can be supplied with `PEMMECE_ADDR`, `PEMMECE_DB`,
`PEMMECE_TLS_CERT`, and `PEMMECE_TLS_KEY`.

Email notifications are enabled when SMTP is configured. The app enqueues email
jobs durably in SQLite when ticket events happen, then a background worker sends
them with retry/backoff.

```sh
go run ./cmd/pemmece \
  -public-url https://support.example.test \
  -smtp-host smtp.example.test \
  -smtp-port 587 \
  -smtp-user pemmece \
  -smtp-password secret \
  -smtp-from noreply@example.test
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
curl -H "Authorization: Bearer pme_..." https://127.0.0.1:8388/api/tickets
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
- `GET /api/projects/{id}/tickets`
- `POST /api/projects/{id}/tickets`
- `GET /api/tickets`
- `POST /api/tickets`
- `GET /api/tickets/{id}`
- `PATCH /api/tickets/{id}`
- `POST /api/tickets/{id}/comments`
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
- `GET /api/projects/{id}/webhook-deliveries`
- `GET /api/email-notifications`

Webhook events:

- `ticket.created`
- `ticket.updated`
- `ticket.commented`
- `ticket.assigned`
