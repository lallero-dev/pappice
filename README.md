<p align="center">
  <img src="./internal/server/web/static/logo.svg" alt="Pappice logo" width="96">
</p>

# Pappice

Pappice is a small, self-hosted, chat-style support desk. Customers open tickets
from the portal; staff assign, reply, and track them.

We built Pappice for our consultancy because the support desks we tried were
too heavy, not fully open source, or missing the workflow we needed. We now use
it in production across multiple clients.

![Pappice chat-style ticketing demo](./assets/demo.gif)

Pappice is intentionally minimal and self-contained:

- One Go binary with embedded web assets.
- SQLite storage plus an upload directory.
- No external database, queue, or frontend build step at runtime.
- Standard library first; the only direct Go dependency is the embedded SQLite
  driver.
- Linux release binaries around 12 MiB.
- Small production instance measured at roughly 20-30 MiB of RAM.

## Features

- Products group tickets by service, customer, or team.
- Customers and staff use the same UI with role-based actions.
- Open/closed ticket workflow; a public reply reopens a closed conversation.
- Chat-style conversations with public replies, internal notes, unread state,
  assignees, priorities, filtering, and sorting.
- Drag/drop and pasted attachments, with inline image previews.
- Admin-created accounts with one-time setup/reset links or manual passwords.
- SMTP-backed no-reply notifications with a durable SQLite outbox.
- API tokens, webhooks, admin audit events, and a maintenance overview.

## Try Quickly

From a source checkout:

```sh
go run ./cmd/pappice demo
```

Or, after installing the release binary:

```sh
pappice demo
```

The demo starts a temporary HTTPS instance with sample users, product, tickets,
and replies. It prints the local URL and login credentials, and removes its
temporary data when stopped. Use `pappice demo -keep` to inspect the generated
SQLite database and upload directory.

## Project Status

Pappice is in 0.x and has not been externally security audited. API and schema
changes may require database migrations between releases.

## Install And Operate

Start with [Install Pappice](./deploy/README.md). **Docker Compose is recommended**;
binary + systemd is also supported. The guide covers HTTPS, backups, restore,
and upgrades. See [configuration](./docs/configuration.md) for optional settings.

For automation, see [API replies and webhook retries](./docs/integrations.md).
For account types and product roles, see [access permissions](./docs/access.md).

## Build From Source

Requires Go 1.26+.

```sh
go build -trimpath -o dist/pappice ./cmd/pappice
```

Create a release archive with `scripts/build-release.sh`.

## Development

Run the complete local quality gate with:

```sh
scripts/check.sh
```

Unavailable race or browser checks are skipped. Set `PAPPICE_CHECK_STRICT=1`
to require all checks; releases use strict mode.

The E2E test requires Node 22+, OpenSSL, and Chromium or Chrome. The launcher
searches `PATH` and standard macOS application locations. Set
`PAPPICE_E2E_CHROMIUM=/path/to/chromium` to override discovery.

See [benchmark/README.md](./benchmark/README.md) for the repeatable small-instance
memory benchmark. Debug builds can expose Go's standard pprof endpoints on an
explicit loopback listener.

## Documentation

- [Architecture and change guidelines](./docs/architecture.md)
- [Security policy](./SECURITY.md)
- [Changelog](./CHANGELOG.md)

## Contributing

Keep changes small and focused. The quality gate above must pass before opening
a pull request.

## License

Pappice is released under the GNU General Public License v3.0 only
(`GPL-3.0-only`). See [LICENSE](./LICENSE).

Copyright 2026 Paolo Marrone and contributors.
