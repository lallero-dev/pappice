#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

version="$(tr -d '[:space:]' < VERSION)"
if [[ -z "$version" ]]; then
  echo "VERSION is empty" >&2
  exit 1
fi

target_os="${GOOS:-linux}"
target_arch="${GOARCH:-amd64}"
dist_dir="dist"
binary="$dist_dir/pappice"
archive_root="pappice-${version}-${target_os}-${target_arch}"
package_dir="$dist_dir/$archive_root"
archive="$dist_dir/$archive_root.tar.gz"
checksum="$archive.sha256"

sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1"
  else
    shasum -a 256 "$1"
  fi
}

rm -rf "$package_dir" "$archive" "$checksum"
mkdir -p "$dist_dir" "$package_dir"

CGO_ENABLED="${CGO_ENABLED:-0}" GOOS="$target_os" GOARCH="$target_arch" go build \
  -trimpath \
  -ldflags "-s -w -X main.version=$version" \
  -o "$binary" \
  ./cmd/pappice

install -m 0755 "$binary" "$package_dir/pappice"
install -m 0644 VERSION LICENSE README.md CHANGELOG.md "$package_dir/"
install -m 0644 .env.example "$package_dir/.env.example"
mkdir -p "$package_dir/deploy/env" "$package_dir/deploy/nginx" "$package_dir/deploy/systemd" "$package_dir/ops"
install -m 0644 deploy/README.md "$package_dir/deploy/README.md"
install -m 0644 deploy/env/pappice.env.example "$package_dir/deploy/env/pappice.env.example"
install -m 0644 deploy/nginx/pappice.conf.example "$package_dir/deploy/nginx/pappice.conf.example"
install -m 0644 deploy/systemd/pappice.service "$package_dir/deploy/systemd/pappice.service"
install -m 0644 deploy/systemd/pappice-backup.service "$package_dir/deploy/systemd/pappice-backup.service"
install -m 0644 deploy/systemd/pappice-backup.timer "$package_dir/deploy/systemd/pappice-backup.timer"
install -m 0755 ops/backup.sh "$package_dir/ops/backup.sh"
install -m 0755 ops/restore.sh "$package_dir/ops/restore.sh"

tar -C "$dist_dir" -czf "$archive" "$archive_root"
(cd "$dist_dir" && sha256_file "$(basename "$archive")" > "$(basename "$checksum")")

echo "Built $binary ($version, $target_os/$target_arch)"
echo "Built $archive"
echo "Built $checksum"
