#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

usage() {
  cat <<'USAGE'
Usage: scripts/release.sh [--dry-run] [--draft] [--prerelease] [--target OS/ARCH]

Build the release archives, verify them, tag the current commit from VERSION,
push the branch and tag, then create or update the GitHub release assets.

By default, releases include linux/amd64 and linux/arm64 archives. Set GOOS and
GOARCH, or pass --target one or more times, to publish a custom target set.

Options:
  --dry-run   Build and verify the archives, then print the publish steps.
  --draft     Create a draft GitHub release when the release does not exist.
  --prerelease
              Mark the GitHub release as a prerelease.
  --target    Build and publish one OS/ARCH target; can be repeated.
  -h, --help  Show this help.
USAGE
}

dry_run=0
draft=0
prerelease=0
targets=()

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
    --target)
      if (($# < 2)); then
        echo "--target requires OS/ARCH" >&2
        usage >&2
        exit 1
      fi
      targets+=("$2")
      shift
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

validate_targets() {
  local supported_targets target target_os target_arch target_extra

  supported_targets="$(go tool dist list)"
  for target in "${targets[@]}"; do
    target_extra=""
    IFS=/ read -r target_os target_arch target_extra <<< "$target"
    if [[ -z "$target_os" || -z "$target_arch" || -n "$target_extra" ]]; then
      echo "Invalid target '$target'; expected OS/ARCH" >&2
      exit 1
    fi
    if ! grep -Fxq -- "$target" <<< "$supported_targets"; then
      echo "Unsupported Go target '$target'" >&2
      exit 1
    fi
  done
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
require_cmd go

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

if ((${#targets[@]} == 0)); then
  if [[ -n "${GOOS:-}" || -n "${GOARCH:-}" ]]; then
    targets+=("${GOOS:-linux}/${GOARCH:-amd64}")
  else
    targets+=(linux/amd64 linux/arm64)
  fi
fi
validate_targets

tag="$version"
artifacts=()

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
  require_cmd gh
  gh auth status >/dev/null
  git fetch --tags origin
fi

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

for target in "${targets[@]}"; do
  IFS=/ read -r target_os target_arch target_extra <<< "$target"

  archive="dist/pappice-${version}-${target_os}-${target_arch}.tar.gz"
  checksum="$archive.sha256"

  GOOS="$target_os" GOARCH="$target_arch" scripts/build-release.sh
  verify_checksum "$checksum"
  artifacts+=("$archive" "$checksum")
done

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
  printf "+ gh release create-or-upload %s" "$tag"
  printf " %s" "${artifacts[@]}"
  if ((${#release_flags[@]})); then
    printf " %s" "${release_flags[@]}"
  fi
  printf "\n"
  exit 0
fi

if ((tag_exists == 0)); then
  git tag -a "$tag" -m "Pappice $version"
fi

git push origin "$branch"
git push origin "$tag"

if gh release view "$tag" >/dev/null 2>&1; then
  gh release upload "$tag" "${artifacts[@]}" --clobber
else
  gh release create "$tag" "${artifacts[@]}" \
    --title "Pappice $version" \
    --notes "See CHANGELOG.md for release notes." \
    "${release_flags[@]}"
fi

echo "Released $tag"
