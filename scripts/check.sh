#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

unformatted="$(find cmd internal -type f -name '*.go' -exec gofmt -l {} +)"
if [[ -n "$unformatted" ]]; then
  printf 'Run gofmt on:\n%s\n' "$unformatted" >&2
  exit 1
fi

for script in scripts/*.sh; do
  bash -n "$script"
done

go vet ./...
go test -race ./...
go test -tags debug ./cmd/pappice
npm run test:e2e
