#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

usage() {
  cat <<'USAGE'
Usage: scripts/release.sh [--dry-run] [--draft] [--prerelease]

Build the release archive, verify it, tag the current commit from VERSION,
push the branch and tag, then create or update the GitHub release assets.

Options:
  --dry-run   Build and verify the archive, then print the publish steps.
  --draft     Create a draft GitHub release when the release does not exist.
  --prerelease
              Mark the GitHub release as a prerelease.
  -h, --help  Show this help.
USAGE
}

dry_run=0
draft=0
prerelease=0

while (($#)); do
  case "$1" in
    --dry-run)
      dry_run=1
      ;;
    --draft)
      draft=1
      ;;
    --prerelease)
      prerelease=1
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown option: $1" >&2
      usage >&2
      exit 1
      ;;
  esac
  shift
done

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "Missing required command: $1" >&2
    exit 1
  fi
}

verify_checksum() {
  local checksum_file="$1"
  local checksum_dir checksum_name

  checksum_dir="$(dirname "$checksum_file")"
  checksum_name="$(basename "$checksum_file")"

  if command -v sha256sum >/dev/null 2>&1; then
    (cd "$checksum_dir" && sha256sum -c "$checksum_name")
  else
    (cd "$checksum_dir" && shasum -a 256 -c "$checksum_name")
  fi
}

require_cmd git
require_cmd gh

version="$(tr -d '[:space:]' < VERSION)"
if [[ -z "$version" ]]; then
  echo "VERSION is empty" >&2
  exit 1
fi

branch="$(git branch --show-current)"
if [[ -z "$branch" ]]; then
  echo "Refusing to release from a detached HEAD" >&2
  exit 1
fi

dirty="$(git status --porcelain)"
if [[ -n "$dirty" ]]; then
  if ((dry_run)); then
    echo "Working tree is dirty; a real release would stop here." >&2
  else
    echo "Working tree is dirty. Commit or stash changes before releasing." >&2
    git status --short >&2
    exit 1
  fi
fi

target_os="${GOOS:-linux}"
target_arch="${GOARCH:-amd64}"
tag="$version"
archive="dist/pappice-${version}-${target_os}-${target_arch}.tar.gz"
checksum="$archive.sha256"
latest_archive="dist/pappice-${target_os}-${target_arch}.tar.gz"
latest_checksum="$latest_archive.sha256"

release_flags=()
if ((prerelease)); then
  release_flags+=(--prerelease)
fi
if ((draft)); then
  release_flags+=(--draft)
fi

if ((dry_run)); then
  echo "Dry run: skipping git fetch and GitHub authentication." >&2
else
  gh auth status >/dev/null
  git fetch --tags origin
fi

scripts/build-release.sh
verify_checksum "$checksum"
verify_checksum "$latest_checksum"

tag_exists=0
if git rev-parse -q --verify "refs/tags/$tag" >/dev/null; then
  tag_exists=1
  tag_commit="$(git rev-list -n 1 "$tag")"
  head_commit="$(git rev-parse HEAD)"
  if [[ "$tag_commit" != "$head_commit" ]]; then
    echo "Tag $tag exists but does not point at HEAD" >&2
    exit 1
  fi
fi

if ((dry_run)); then
  echo
  echo "Dry run publish steps:"
  echo "+ git fetch --tags origin"
  if ((tag_exists)); then
    echo "# tag $tag already exists locally and points at HEAD"
  else
    echo "+ git tag -a $tag -m 'Pappice $version'"
  fi
  echo "+ git push origin $branch"
  echo "+ git push origin $tag"
  echo "+ gh release create-or-upload $tag $archive $checksum $latest_archive $latest_checksum ${release_flags[*]}"
  exit 0
fi

if ((tag_exists == 0)); then
  git tag -a "$tag" -m "Pappice $version"
fi

git push origin "$branch"
git push origin "$tag"

if gh release view "$tag" >/dev/null 2>&1; then
  gh release upload "$tag" "$archive" "$checksum" "$latest_archive" "$latest_checksum" --clobber
else
  gh release create "$tag" "$archive" "$checksum" "$latest_archive" "$latest_checksum" \
    --title "Pappice $version" \
    --notes "See CHANGELOG.md for release notes." \
    "${release_flags[@]}"
fi

echo "Released $tag"
