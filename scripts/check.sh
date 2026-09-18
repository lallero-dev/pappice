#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

strict="${PAPPICE_CHECK_STRICT:-0}"
case "$strict" in
  0|1) ;;
  *)
    echo "PAPPICE_CHECK_STRICT must be 0 or 1" >&2
    exit 2
    ;;
esac

skip_or_fail() {
  local message="$1"
  if [[ "$strict" == "1" ]]; then
    echo "Required check unavailable: $message" >&2
    exit 1
  fi
  echo "Skipping optional check: $message" >&2
}

unformatted="$(find cmd internal demo -type f -name '*.go' -exec gofmt -l {} +)"
if [[ -n "$unformatted" ]]; then
  printf 'Run gofmt on:\n%s\n' "$unformatted" >&2
  exit 1
fi

for script in scripts/*.sh; do
  bash -n "$script"
done

race_errors="$(mktemp "${TMPDIR:-/tmp}/pappice-race.XXXXXX")"
trap 'rm -f "$race_errors"' EXIT
for module in . demo/browser; do
  (
    cd "$module"
    go vet ./...
    if go test -race ./... 2>"$race_errors"; then
      cat "$race_errors" >&2
    elif grep -Eq -- '-race (is not supported|requires cgo)' "$race_errors"; then
      race_reason="$(tr '\n' ' ' < "$race_errors")"
      skip_or_fail "${race_reason% }"
      go test ./...
    else
      cat "$race_errors" >&2
      exit 1
    fi
  )
done
rm -f "$race_errors"
trap - EXIT

go test -tags debug ./internal/app
go build -tags debug ./cmd/pappice ./demo/native

if ! command -v node >/dev/null 2>&1 || ! command -v npm >/dev/null 2>&1; then
  skip_or_fail "Node.js or npm is unavailable"
elif chromium="$(node test/tools/chromium.mjs)"; then
  PAPPICE_E2E_CHROMIUM="$chromium" npm run test:e2e
  PAPPICE_E2E_CHROMIUM="$chromium" npm run test:browser
else
  chromium_status=$?
  if ((chromium_status == 2)); then
    exit 1
  fi
  skip_or_fail "Chromium was not found; set PAPPICE_E2E_CHROMIUM to its executable"
fi
