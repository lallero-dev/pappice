# Changelog

## Unreleased

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
