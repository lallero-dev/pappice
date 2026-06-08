# Changelog

## Unreleased

- Linkify URLs in ticket conversations while keeping messages stored as plain
  text.
- Improve chat rendering for long words, image previews, and conversation
  icons.
- Remove legacy username migration support after the only early production
  instance was upgraded.

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
