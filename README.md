# Pemmece

Pemmece is a small Go issue tracker inspired by MantisBT and the local-first
operational feel of the Syncthing web UI.

It is still a single binary, but now includes the core pieces needed for a
usable self-hosted tracker:

- First-run admin setup, login sessions, roles, and API tokens.
- Mantis-like issues with statuses, severity, priority, assignees, reporters,
  tags, comments, and filtering.
- JSON-backed local persistence.
- Embedded web assets served by the Go process.
- Webhooks for issue and repository events with `X-Pemmece-Signature`.
- Local Git repository scanning that links commits mentioning `#123` or
  `PME-123` to matching issues.

## Run

```sh
go run ./cmd/pemmece
```

Open http://127.0.0.1:8388 and create the first admin user.

Useful flags:

```sh
go run ./cmd/pemmece -addr 0.0.0.0:8388 -data ./pemmece-data.json
```

The same values can be supplied with `PEMMECE_ADDR` and `PEMMECE_DATA`.

## API

Authentication works with the web session cookie or an API token:

```sh
curl -H "Authorization: Bearer pme_..." http://127.0.0.1:8388/api/issues
```

Core endpoints:

- `GET /api/health`
- `GET /api/me`
- `POST /api/setup`
- `POST /api/login`
- `POST /api/logout`
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
- `PATCH /api/webhooks/{id}`
- `DELETE /api/webhooks/{id}`
- `POST /api/webhooks/{id}/test`
- `GET /api/repo`
- `PATCH /api/repo`
- `POST /api/repo/scan`

Webhook events:

- `issue.created`
- `issue.updated`
- `issue.commented`
- `repo.scanned`
