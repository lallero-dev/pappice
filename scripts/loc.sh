#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: scripts/loc.sh [--files] [git-ref]

Counts tracked text lines by area. Without a git ref, counts the current
working tree contents for files tracked by git. With a ref, counts that tree.

Options:
  --files   include per-file counts below the category summary
  --help    show this help
EOF
}

show_files=0
ref=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --files)
      show_files=1
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    -*)
      echo "unknown option: $1" >&2
      usage >&2
      exit 2
      ;;
    *)
      if [[ -n "$ref" ]]; then
        echo "only one git ref can be provided" >&2
        usage >&2
        exit 2
      fi
      ref="$1"
      ;;
  esac
  shift
done

category_for() {
  local file="$1"
  case "$file" in
    internal/server/web/*)
      echo "frontend"
      ;;
    *_test.go|test/*|test/e2e/*)
      echo "tests"
      ;;
    cmd/*.go|cmd/*/*.go|internal/*.go|internal/*/*.go)
      echo "backend"
      ;;
    benchmark/*.md)
      echo "docs"
      ;;
    benchmark/*|demo/*|scripts/*|test/tools/*)
      echo "scripts"
      ;;
    ops/*)
      echo "ops"
      ;;
    deploy/*|.env.example|.gitignore|go.mod|go.sum|package.json)
      echo "ops-config"
      ;;
    CHANGELOG.md|README.md|LICENSE|VERSION)
      echo "docs"
      ;;
    *)
      echo "other"
      ;;
  esac
}

is_counted_file() {
  local file="$1"
  case "$file" in
    assets/*.gif|assets/*.jpg|assets/*.jpeg|assets/*.png|assets/*.webp)
      return 1
      ;;
    *)
      return 0
      ;;
  esac
}

tmp="$(mktemp "${TMPDIR:-/tmp}/pappice-loc.XXXXXX")"
trap 'rm -f "$tmp"' EXIT

count_worktree_file() {
  local file="$1"
  [[ -f "$file" ]] || return 0
  local category lines nonblank
  category="$(category_for "$file")"
  lines="$(wc -l < "$file" | awk '{ print $1 + 0 }')"
  nonblank="$(awk 'NF { count++ } END { print count + 0 }' "$file")"
  printf "%s\t%d\t%d\t%s\n" "$category" "$lines" "$nonblank" "$file" >> "$tmp"
}

count_ref_file() {
  local file="$1"
  local object="$ref:$file"
  local category lines nonblank
  category="$(category_for "$file")"
  lines="$(git show "$object" | wc -l | awk '{ print $1 + 0 }')"
  nonblank="$(git show "$object" | awk 'NF { count++ } END { print count + 0 }')"
  printf "%s\t%d\t%d\t%s\n" "$category" "$lines" "$nonblank" "$file" >> "$tmp"
}

if [[ -z "$ref" ]]; then
  source_label="working tree"
  while IFS= read -r -d '' file; do
    is_counted_file "$file" || continue
    count_worktree_file "$file"
  done < <(git ls-files -z)
else
  git rev-parse --verify --quiet "$ref^{tree}" >/dev/null || {
    echo "unknown git ref: $ref" >&2
    exit 1
  }
  source_label="$ref"
  while IFS= read -r -d '' file; do
    is_counted_file "$file" || continue
    count_ref_file "$file"
  done < <(git ls-tree -r -z --name-only "$ref")
fi

printf "Pappice LOC (%s)\n\n" "$source_label"
awk -F '\t' -v show_files="$show_files" '
BEGIN {
  split("backend frontend tests scripts ops ops-config docs other", order, " ")
}
{
  category = $1
  lines = $2 + 0
  nonblank = $3 + 0
  file = $4
  files_by_category[category]++
  lines_by_category[category] += lines
  nonblank_by_category[category] += nonblank
  total_files++
  total_lines += lines
  total_nonblank += nonblank
  file_rows[category] = file_rows[category] sprintf("%-11s %7d %9d  %s\n", category, lines, nonblank, file)
}
END {
  printf "%-11s %7s %9s %9s\n", "category", "files", "lines", "nonblank"
  printf "%-11s %7s %9s %9s\n", "--------", "-----", "-----", "--------"
  for (i = 1; i <= length(order); i++) {
    category = order[i]
    if (files_by_category[category] == 0) {
      continue
    }
    printf "%-11s %7d %9d %9d\n", category, files_by_category[category], lines_by_category[category], nonblank_by_category[category]
  }
  printf "%-11s %7s %9s %9s\n", "--------", "-----", "-----", "--------"
  printf "%-11s %7d %9d %9d\n", "total", total_files, total_lines, total_nonblank

  if (show_files == 1) {
    printf "\n%-11s %7s %9s  %s\n", "category", "lines", "nonblank", "file"
    printf "%-11s %7s %9s  %s\n", "--------", "-----", "--------", "----"
    for (i = 1; i <= length(order); i++) {
      category = order[i]
      printf "%s", file_rows[category]
    }
  }
}
' "$tmp"
