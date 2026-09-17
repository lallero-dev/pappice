# Pappice Architecture

Pappice runs as one Go process with embedded browser assets, SQLite for metadata,
and a filesystem directory for uploads. It has no external queue or worker service.

## Packages

| Package | Responsibility |
|---|---|
| `cmd/pappice` | CLI, configuration, startup, shutdown, and worker orchestration |
| `internal/server` | HTTP routing, authentication, CSRF, uploads, event projection, webhooks, and embedded assets |
| `internal/store` | SQLite, migrations, transactional mutations, access rules, and durable outboxes |
| `internal/notify` | SMTP validation, message rendering, and email delivery |
| `internal/backup` | Database and upload snapshots, restore, and recovery |
| `internal/security` | Password hashing, tokens, and HMAC helpers |

## Request Flow

For an authenticated browser mutation:

1. The server authenticates the session and checks CSRF and HTTP permissions.
2. The handler calls a store mutation.
3. The store validates domain data and writes changes and domain events in one transaction.
4. The handler calls `dispatchEventsSoon` to wake event projection.
5. Workers project events into audit rows and email/webhook notifications, then deliver them.

Handlers do not build SQL. The store does not depend on cookies, request origins,
CSRF, or response formats.

## Persistence

`store.Open` installs the schema for an empty database. It rejects existing
databases with pending migrations or a schema newer than the binary.
`pappice doctor` reports schema state without applying changes; migration commands
belong to the [deployment procedure](../deploy/README.md#upgrade).

SQLite stores attachment metadata and storage keys; files hold the content.
Each upload owns a separate file, so failed-request cleanup cannot affect a
concurrent upload. SHA-256 is content metadata, not a storage key. Deletion
removes a stored file only when no attachment references it.

Restore stages replacements and saves originals beside each destination, using
`.<name>.restore-pre-<timestamp>` recovery directories. Failed operations roll back
completed renames; failed rollback preserves originals and reports their paths.
The rename sequence is not atomic across a process crash or power loss.
See [backup and restore operations](../deploy/README.md#restore).

## Domain Events And Outboxes

Event projection, webhook delivery, and SMTP delivery run in separate workers.
Projection writes audit entries and queues notifications; committed webhook work
wakes its worker. The webhook worker polls for delayed work and drains full
batches without waiting for another poll. SQLite outboxes use leases and track
delivery attempts so work can resume after a restart.

Webhook delivery IDs and payloads remain fixed once delivery is attempted;
only unattempted notifications can be coalesced. Ticket idempotency keys and
request hashes live in `ticket_requests`, committed with the mutation and its
domain events. See [API and webhook contracts](./integrations.md) for retry semantics.

Webhook DNS lookups and HTTP requests inherit their worker or manual-test request
context. SMTP uses one timeout across connection, TLS, and message exchange;
cancellation closes the connection. Shutdown cancels and waits for workers
before closing SQLite.

## Authentication And Authorization

Browser authentication uses secure session cookies and an API-issued CSRF token.
Session creation requires HTTPS, JSON, and a matching `Origin` or `Referer`.
API automation uses bearer tokens; browser-only operations reject token auth.

Browser and API requests share authorization checks. Ticket reading, internal-note
access, and editing are separate permissions, including for downloads and unread
counts. See [account types and product roles](./access.md).

Passwords use PBKDF2-SHA256. Successful login rehashes passwords whose stored
parameters fall below the requirements in `internal/security`. Account setup and
password reset consume one-time links, set the password, and update reset state
in one transaction.

## Frontend

`internal/server/web` contains plain JavaScript modules and CSS embedded in the
binary. `app.js` handles orchestration; feature modules own rendering and behavior.

## Testing

Run the [local quality gate](../README.md#development). Test store invariants and
HTTP contracts directly. Force state transitions or notification timestamps
instead of waiting on wall-clock sleeps.

## Change Guidelines

- Prefer the standard library and existing package boundaries.
- Keep CLI and HTTP code thin over store methods; use one transaction per mutation.
- Add layers only to remove duplication or isolate a product boundary.
- Add external services only when the single-process model cannot meet requirements.
- Document current behavior in reference docs; put release-specific changes in the
  changelog and link shared rules instead of repeating them.
