#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

version="$(tr -d '[:space:]' < VERSION)"
if [[ -z "$version" ]]; then
  echo "VERSION is empty" >&2
  exit 1
fi

output="${1:-dist/pappice}"
mkdir -p "$(dirname "$output")"

go build \
  -trimpath \
  -ldflags "-s -w -X main.version=$version" \
  -o "$output" \
  ./cmd/pappice

echo "Built $output ($version)"
