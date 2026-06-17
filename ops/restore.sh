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

assume_yes=false
if [[ "${1:-}" == "--yes" ]]; then
  assume_yes=true
  shift
fi

backup="${1:-latest}"

latest_backup() {
  local newest=""
  local candidate
  if [[ ! -d "$BACKUP_DIR" ]]; then
    return 1
  fi
  for candidate in "$BACKUP_DIR"/*; do
    [[ -d "$candidate" && -f "$candidate/pappice.db" ]] || continue
    newest="$candidate"
  done
  [[ -n "$newest" ]] || return 1
  printf "%s" "$newest"
}

if [[ "$backup" == "latest" ]]; then
  if ! backup="$(latest_backup)"; then
    echo "No backups found in $BACKUP_DIR" >&2
    exit 1
  fi
fi

if [[ ! -f "$backup/pappice.db" ]]; then
  echo "Backup database not found: $backup/pappice.db" >&2
  exit 1
fi

if [[ "$assume_yes" != true ]]; then
  echo "Stop Pappice before restoring. This will replace:"
  echo "  DB:      $DB_PATH"
  echo "  Uploads: $UPLOAD_DIR"
  printf "Restore from %s? [y/N] " "$backup"
  read -r answer
  case "$answer" in
    y|Y|yes|YES) ;;
    *) echo "Restore cancelled."; exit 1 ;;
  esac
fi

timestamp="$(date -u +%Y%m%dT%H%M%SZ)"
safety_dir="$BACKUP_DIR/restore-pre-$timestamp"
mkdir -p "$safety_dir"

mkdir -p "$(dirname "$DB_PATH")"
if [[ -e "$DB_PATH" ]]; then
  mv "$DB_PATH" "$safety_dir/$(basename "$DB_PATH")"
fi
for suffix in -wal -shm; do
  if [[ -e "$DB_PATH$suffix" ]]; then
    mv "$DB_PATH$suffix" "$safety_dir/$(basename "$DB_PATH$suffix")"
  fi
done
cp "$backup/pappice.db" "$DB_PATH"

if [[ -e "$UPLOAD_DIR" ]]; then
  mv "$UPLOAD_DIR" "$safety_dir/uploads"
fi
mkdir -p "$UPLOAD_DIR"
if [[ -f "$backup/uploads.tar" ]]; then
  tar -C "$UPLOAD_DIR" -xf "$backup/uploads.tar"
fi

echo "Restore complete from: $backup"
echo "Previous files saved in: $safety_dir"
