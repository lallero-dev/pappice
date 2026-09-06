# Pappice Architecture

This document describes the current shape of Pappice so changes stay simple and
consistent. It is intentionally a map of the codebase, not a framework plan.

## Design Intent

Pappice is a single-process support desk:

- one Go binary with embedded browser assets
- SQLite for durable application state
- a filesystem upload directory for attachment bytes
- no external queue, cache, worker service, or frontend build step at runtime

The preferred design is standard-library-first and package-local. Add a new
layer only when it removes real duplication or isolates a boundary that already
exists in the product.

## Runtime Shape

`cmd/pappice` owns process concerns:

- CLI command routing
- flag and `.env` configuration
- database migration commands
- `doctor` diagnostics
- binary-native backup and restore commands
- HTTP server startup and graceful shutdown
- independent event, webhook, and optional SMTP workers

`internal/server` owns HTTP concerns:

- routing, request decoding, response formatting, and security headers
- browser session and API-token authentication
- CSRF enforcement for browser mutations
- upload/download handling
- domain-event projection into audit, email, and webhook work
- webhook delivery
- embedded frontend assets

`server.go` assembles the server and its options; `routes.go` registers routes
and serves the app shell. Authentication and session checks live in `auth.go`.
The feature handlers live in `account_handlers.go`, `product_handlers.go`,
`ticket_handlers.go`, `webhook_handlers.go`, `email_handlers.go`, and
`maintenance_handlers.go`, all in the same package.

`internal/store` owns persistence and domain invariants:

- SQLite connection setup
- schema install, migration inspection, migration execution, and validation
- transactional ticket, user, product, webhook, audit, email, and event writes
- role and visibility checks that are independent of HTTP details
- outbox claim/mark operations for email and webhook delivery

`internal/notify` owns email delivery:

- SMTP configuration validation
- message rendering and send
- durable outbox worker loop using store claim/mark methods

`internal/security` owns low-level security primitives:

- password hashing and verification
- token generation and token hashing
- HMAC helpers

## Request Flow

For a typical authenticated browser mutation:

1. `internal/server` authenticates the session cookie.
2. The handler checks CSRF and HTTP-level permissions.
3. The handler calls one `internal/store` mutation.
4. The store mutation validates domain data and writes changes transactionally.
5. The store records a domain event in the same transaction when side effects
   are needed.
6. The handler calls `dispatchEventsSoon`.
7. Domain events are projected into audit rows, email notifications, and webhook
   notifications.

The HTTP layer should not hand-build SQL. The store should not know about
cookies, CSRF, request origins, or response formats.

## Persistence

SQLite is the source of truth for application metadata. Attachment bytes live in
the upload directory, and SQLite stores their metadata and storage keys.

`store.Open` installs the current schema for an empty database. For an existing
database it refuses to start when migrations are pending or when the schema is
newer than the binary. Operators should run:

```sh
pappice db migrate --dry-run
pappice db migrate
```

`pappice doctor` reports schema state without applying changes.

## Domain Events And Outboxes

Domain events are the internal boundary between core mutations and side effects.
They are written transactionally with the business change that caused them.

The server projects domain events into:

- audit events
- email notifications
- webhook notifications

Email and webhook notifications are durable SQLite outboxes. Workers claim due
rows with leases, mark success or failure, and retry according to the store
rules. This keeps side effects recoverable after process restarts without adding
an external queue.

Event projection and webhook delivery run in separate workers. Projection only
writes audit entries and queues notifications; committed webhook work wakes the
webhook worker. A slow receiver cannot hold up new audit entries or email work.
The webhook worker also polls for delayed notifications and retries, and drains
full batches without waiting for the next poll.

Each webhook notification has a durable delivery ID. Retries reuse that ID and
payload; only notifications with no delivery attempts can be coalesced. The ID
is included in the signed JSON body and in `X-Pappice-Delivery-ID`. Receivers
must still deduplicate incoming work because an acknowledgment can be lost.

Ticket mutations optionally accept `Idempotency-Key`. The store reserves the key
with a request hash in `ticket_requests`, in the same transaction as the ticket
change and its domain events. A matching retry returns the current ticket without
writing another change; different content conflicts. Keys belong to a user and
ticket and are retained until either is deleted. See [the integration contract](./integrations.md).

Webhook DNS lookups and HTTP requests inherit the worker context, or the request
context for a manual test delivery. Shutdown cancels and waits for all workers
before closing SQLite.

SMTP sends have one timeout covering the connection, TLS negotiation, and the
whole SMTP exchange. Context cancellation closes the connection immediately so
a stalled server cannot hold the email worker indefinitely.

In tests, prefer direct dispatch or explicit database timestamps over wall-clock
sleeping. A test should force a notification to be due rather than wait for it
to become due.

## Auth Model

Browser auth uses secure session cookies plus a CSRF token returned by the API.
Requests that create a session (initial setup, login, and account-link completion)
must use HTTPS, `Content-Type: application/json`, and a matching `Origin` or
`Referer` header.
API automation uses bearer tokens. Browser-only operations, such as password
change, must reject API-token auth.

Account types set global privileges; product roles apply within memberships.
Admin accounts have full product access, while Customer accounts retain their
own-ticket and public-conversation restrictions. The `internal_contributor`
product role lets Staff accounts read and add internal notes without public
replies or ticket edits. Ticket access checks keep internal-note access separate
from editing, including attachment downloads and unread counts. Browser and API
requests use the same server checks. See [access permissions](./access.md).

Passwords are stored as encoded PBKDF2-SHA256 hashes. New hashes use the current
work factor from `internal/security`; older valid hashes may be accepted and
opportunistically upgraded after successful login.

Account setup and password reset use one-time account links. Consuming a link
sets the password, marks the link used, and clears or enforces reset state in
one transaction.

## Uploads

Uploads are split between:

- metadata in SQLite
- content files under `PAPPICE_UPLOAD_DIR`

Each new upload owns a separate file, even when its content matches an existing
attachment. This keeps failed-request cleanup independent of concurrent uploads
and deletions. SHA-256 remains content metadata. Existing attachments with shared
storage keys remain readable and are removed only after their last reference.

The database and upload directory must be backed up and restored together.
Stop Pappice before restoring. Restore stages replacements beside each target
and saves the original database files and uploads in separate sibling directories
named `.<name>.restore-pre-<timestamp>`. The CLI prints both recovery locations.
This avoids moving live files to the backup filesystem.

If a filesystem operation fails, restore reverses completed renames. If rollback
also fails, it preserves the saved originals and reports their paths in the
error. This handles reported errors; the sequence is not atomic across a process
crash or power loss.

## Frontend

The frontend is served from `internal/server/web` and is embedded into the Go
binary. It uses plain JavaScript modules and CSS. There is intentionally no
runtime build step.

Keep frontend growth modular by feature. `app.js` should remain orchestration
and routing glue over time; feature-specific rendering and behavior should move
into dedicated modules when it becomes large or repetitive.

## Testing

The local quality gate is:

```sh
scripts/check.sh
```

The script runs race-enabled tests when the current Go target supports them and
browser tests when Chromium or Chrome is available. Unsupported optional checks
are reported and skipped so the gate remains useful across Android/Termux,
Linux, and macOS. Set `PAPPICE_CHECK_STRICT=1` to require every check; releases
always use this strict mode.

Use focused tests for store invariants and HTTP contract tests for handler
behavior. Avoid sleeps in tests when a state transition can be forced directly.

## Change Guidelines

- Keep new behavior close to the existing package boundary that owns it.
- Prefer one transaction for one domain mutation.
- Keep CLI and HTTP code thin over store methods.
- Do not add external services unless the single-binary model can no longer meet
  the product requirement.
- Split files by existing feature boundaries before inventing new abstractions.
