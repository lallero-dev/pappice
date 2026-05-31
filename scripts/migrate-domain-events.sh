#!/usr/bin/env bash
set -euo pipefail

db_path="${1:-pappice.db}"

if ! command -v sqlite3 >/dev/null 2>&1; then
  echo "sqlite3 is required" >&2
  exit 1
fi

if [[ ! -f "$db_path" ]]; then
  echo "database not found: $db_path" >&2
  exit 1
fi

sqlite3 "$db_path" <<'SQL'
PRAGMA foreign_keys = ON;
BEGIN;

CREATE TABLE IF NOT EXISTS domain_events (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	type TEXT NOT NULL,
	product_id INTEGER NOT NULL DEFAULT 0,
	ticket_id INTEGER NOT NULL DEFAULT 0,
	actor_user_id INTEGER NOT NULL DEFAULT 0,
	actor_username TEXT NOT NULL DEFAULT '',
	actor_display_name TEXT NOT NULL DEFAULT '',
	actor_email TEXT NOT NULL DEFAULT '',
	actor_role TEXT NOT NULL DEFAULT '',
	payload_json TEXT NOT NULL DEFAULT '{}',
	status TEXT NOT NULL CHECK (status IN ('pending', 'processing', 'processed', 'failed')) DEFAULT 'pending',
	attempts INTEGER NOT NULL DEFAULT 0,
	locked_until TEXT,
	last_error TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	processed_at TEXT
);

CREATE INDEX IF NOT EXISTS idx_domain_events_pending ON domain_events(status, locked_until, created_at);
CREATE INDEX IF NOT EXISTS idx_domain_events_ticket ON domain_events(ticket_id, id);

CREATE TABLE IF NOT EXISTS webhook_notifications (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	webhook_id INTEGER REFERENCES webhooks(id) ON DELETE CASCADE,
	product_id INTEGER REFERENCES products(id) ON DELETE CASCADE,
	ticket_id INTEGER REFERENCES tickets(id) ON DELETE CASCADE,
	event TEXT NOT NULL,
	payload_json TEXT NOT NULL,
	status TEXT NOT NULL CHECK (status IN ('pending', 'sending', 'sent', 'failed')) DEFAULT 'pending',
	attempts INTEGER NOT NULL DEFAULT 0,
	next_attempt_at TEXT NOT NULL,
	locked_until TEXT,
	last_error TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	sent_at TEXT
);

CREATE INDEX IF NOT EXISTS idx_webhook_notifications_pending ON webhook_notifications(status, next_attempt_at, locked_until);
CREATE INDEX IF NOT EXISTS idx_webhook_notifications_webhook ON webhook_notifications(webhook_id, created_at);

COMMIT;
SQL

if sqlite3 "$db_path" "SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'audit_events';" | grep -qx audit_events; then
  if ! sqlite3 "$db_path" "PRAGMA table_info(audit_events);" | awk -F'|' '{print $2}' | grep -qx domain_event_id; then
    sqlite3 "$db_path" "ALTER TABLE audit_events ADD COLUMN domain_event_id INTEGER NOT NULL DEFAULT 0;"
  fi
  sqlite3 "$db_path" "CREATE UNIQUE INDEX IF NOT EXISTS idx_audit_events_domain_event ON audit_events(domain_event_id) WHERE domain_event_id > 0;"
fi

echo "event and notification outboxes are ready in $db_path"
