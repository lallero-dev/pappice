# Changelog

## Unreleased

- Move the native demo out of the production binary. Run `npm run demo:native`
  or `go run ./demo/native` from a source checkout instead of `pappice demo`.

- Add an experimental static browser demo running the Go backend and SQLite in
  WebAssembly, with private per-tab data and reset on reload.

- Consolidate Docker Compose and binary/systemd deployment into
  one guide and a configuration reference, with small environment templates
  and complete deployment documentation in release archives.
- Make Docker's temporary filesystem writable by the application user and use
  the data volume for migration dry runs.
- Remove the bundled backup service and daily timer; scheduling is operator-managed.
  Keep the backup/restore CLI and document direct backups before upgrades.

Existing systemd installations: disable the retired backup timer with
`sudo systemctl disable --now pappice-backup.timer` and manage scheduling externally.

## v0.15.0 - 2026-09-14

- Add the Internal contributor product role for staff who can read and add
  internal notes without sending public replies or editing tickets.
- Support idempotency keys for ticket updates and comments, and stable webhook
  delivery IDs so integrations can safely deduplicate retries.
- Run webhook delivery independently of event projection, and bound SMTP sends
  with timeouts and context cancellation.
- Reject cross-site session creation requests and apply login rate limits
  consistently across equivalent email address formats.
- Keep concurrent attachment uploads independent during cleanup, and roll back
  failed restores while preserving originals beside their destinations.
- Show storage usage in maintenance, fix mobile settings scrolling, and remove
  the duplicate Add Member action from an empty product member list.
- Include access permissions, integration guidance, and the security policy in
  release archives.

Upgrading from v0.14.0 requires schema migration 8. Back up the database and
uploads, stop Pappice, then use the updated binary with the existing storage
configuration to run `pappice db migrate --dry-run` and `pappice db migrate`
before restarting. The migration adds webhook delivery IDs and the ticket
idempotency-key table; it preserves queued notifications and their states.

## v0.14.0 - 2026-08-15

- Simplify tickets to open and closed states, with public replies reopening
  closed conversations and selected closed tickets remaining visible.
- Unify the ticket inspector, improve its mobile confirmation flow, and add a
  vertically resizable reply composer with aligned actions.
- Keep ticket update times tied to status changes and conversation activity,
  excluding internal priority and assignee changes.
- Make domain-event dispatch asynchronous and retry-safe, and improve the
  portability of local quality checks.

## v0.13.0 - 2026-07-24

- Model ticket requesters and creators explicitly and preserve accounts that
  are referenced by ticket history.
- Record status transitions transactionally and show them live in ticket
  conversations.
- Skip queued mail for disabled accounts and tighten persistence, attachment,
  notification, and backup error handling.
- Strengthen API, authorization, migration, failure-path, and browser coverage.
- Simplify the README and deployment guidance for evaluation and production
  setup.

## v0.12.0 - 2026-07-11

- Paginate and optimize the ticket inbox with lightweight summaries, aggregate
  counts, and selected-ticket conversation loading.
- Normalize ticket relationships around user IDs and restrict assignees to
  staff assigned to the ticket's product.
- Harden authentication, API-token boundaries, audit context, rate limits,
  routing, persisted timestamps, and notification failure reporting.
- Add product general settings and reorganize the browser application into
  focused, dependency-checked feature modules.
- Add opt-in debug-build pprof support and a single local quality gate enforced
  by the release script.
- Adopt the standard-library PBKDF2 implementation and apply focused runtime
  and allocation improvements.

## v0.11.0 - 2026-07-04

- Add linux/arm64 release archives alongside linux/amd64 and update release
  tooling and deployment docs for architecture-specific downloads.
- Keep release assets versioned and resolve the latest tag from deploy docs.
- Keep local ticket reply drafts when navigating or refreshing tickets.
- Refresh open ticket conversations in the background without replacing the
  reply composer.
- Improve image-preview escape handling.
- Fix mobile admin menu spacing.

## v0.10.0 - 2026-06-29

- Add `pappice demo` for a temporary seeded local instance.
- Simplify the root README around quick evaluation and defer production setup
  to the deploy guide.
- Publish regular GitHub releases by default; prerelease publishing is now an
  explicit maintainer option.

## v0.9.0-alpha - 2026-06-29

- Rename product roles from `owner` to `manager` and from `agent` to `staff`
  for clearer staff/customer terminology.
- Add a database migration that updates existing product memberships to the new
  role names.

## v0.8.2-alpha - 2026-06-22

- Prevent stale ticket-list responses from overwriting a freshly updated ticket
  conversation.
- Refresh the browser CSRF token and retry once after a stale-token error.

## v0.8.1-alpha - 2026-06-18

- Keep alpha install docs on explicit release URLs and make release downloads
  fail on HTTP errors.
- Make `pappice doctor` report database schema status, including empty
  databases and pending migrations.
- Replace stale uploads during restore even when a backup does not include an
  upload archive.
- Upgrade password hashes after successful legacy-hash login.
- Add binary-native `pappice backup` and `pappice restore` commands, replacing
  the operational backup/restore shell scripts.
- Simplify deployment onboarding with Docker named volumes, systemd working
  directories under `/var/lib/pappice`, and opt-in SMTP for first boot.
- Update the nginx template to the current HTTP/2 directive style.

## v0.8.0-alpha - 2026-06-16

- Show full message timestamps in ticket conversations.
- Tighten the README, demo GIF workflow, E2E smoke tests, and API contract
  coverage.
- Add small-environment memory benchmarking and document the baseline result.
- Improve production deployment docs around release archives, checksums,
  backups, and migrations.
- Add local maintainer scripts for building and publishing release archives.

## v0.7.0-alpha - 2026-06-09

- Linkify URLs in ticket conversations while keeping messages stored as plain
  text.
- Improve chat rendering for long words, image previews, and conversation
  icons.
- Remove legacy username migration support after the only early production
  instance was upgraded.
- Document Pappice as a chat-style ticketing system and add this changelog.

## v0.6.0-alpha - 2026-06-09

- Add an explicit database migration workflow with `pappice db status` and
  `pappice db migrate`.
- Hide ticket assignees and source chips from customers.
- Refine ticket filters, popovers, and shell spacing.

## v0.5.0-alpha

- Allow admins to create accounts with manual passwords.
- Remove obsolete early-alpha migration artifacts.

## v0.4.0-alpha

- Improve responsive ticket UI and conversation layout.
- Coalesce pending webhook notifications.
- Consolidate backend and frontend structure.
- Add a LOC measurement script.

## Earlier Alpha Releases

- Establish the product-based ticketing model.
- Add tickets, products, customers, staff accounts, attachments, unread state,
  email notifications, webhooks, audit logs, admin tools, and local CLI support.
