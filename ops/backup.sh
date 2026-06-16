#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

trim() {
  local value="$1"
  value="${value#"${value%%[![:space:]]*}"}"
  value="${value%"${value##*[![:space:]]}"}"
  printf "%s" "$value"
}

config_value() {
  local key="$1"
  local fallback="$2"
  local current="${!key:-}"
  local line value first last
  if [[ -n "$current" ]]; then
    printf "%s" "$current"
    return
  fi
  if [[ -f .env ]]; then
    while IFS= read -r line; do
      line="$(trim "$line")"
      [[ -z "$line" || "$line" == \#* ]] && continue
      line="$(trim "${line#export }")"
      if [[ "$line" == "$key="* ]]; then
        value="$(trim "${line#*=}")"
        first="${value:0:1}"
        last="${value: -1}"
        if [[ ${#value} -ge 2 && ( ( "$first" == "\"" && "$last" == "\"" ) || ( "$first" == "'" && "$last" == "'" ) ) ]]; then
          value="${value:1:${#value}-2}"
        fi
        printf "%s" "$value"
        return
      fi
    done < .env
  fi
  printf "%s" "$fallback"
}

DB_PATH="$(config_value PAPPICE_DB ./pappice.db)"
UPLOAD_DIR="$(config_value PAPPICE_UPLOAD_DIR ./pappice-uploads)"
BACKUP_DIR="$(config_value PAPPICE_BACKUP_DIR ./pappice-backups)"

if [[ ! -f "$DB_PATH" ]]; then
  echo "Database not found: $DB_PATH" >&2
  exit 1
fi

if ! command -v sqlite3 >/dev/null 2>&1; then
  echo "sqlite3 is required for a consistent online backup." >&2
  exit 1
fi

timestamp="$(date -u +%Y%m%dT%H%M%SZ)"
destination="$BACKUP_DIR/$timestamp"
backup_db="$destination/pappice.db"
mkdir -p "$destination"

sql_quote() {
  printf "%s" "$1" | sed "s/'/''/g"
}

sqlite3 "$DB_PATH" "PRAGMA wal_checkpoint(PASSIVE);" >/dev/null
sqlite3 "$DB_PATH" ".backup '$(sql_quote "$backup_db")'"

has_uploads=false
if [[ -d "$UPLOAD_DIR" ]]; then
  tar -C "$UPLOAD_DIR" -cf "$destination/uploads.tar" .
  has_uploads=true
fi

cat >"$destination/manifest.env" <<EOF
created_at=$timestamp
db_path=$DB_PATH
upload_dir=$UPLOAD_DIR
has_uploads=$has_uploads
EOF

echo "Backup created: $destination"
echo "Restore with: ops/restore.sh $destination"
