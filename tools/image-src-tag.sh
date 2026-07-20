#!/bin/sh
# Content-addressed image tag for the current working tree. Copies the real
# git index to a temp file (keeping the stat cache), stages the full working
# tree into that temp index, and hashes the resulting tree object — the real
# index and the working tree are never modified. Same tree -> same tag; any
# tracked or untracked (non-ignored) change -> different tag. Consumed by the
# Makefile image targets and by the services test harness image resolver
# (lib/services/test/harness/image_tag.go); both sides MUST derive tags from
# this one script.
set -eu
root=$(git rev-parse --show-toplevel)
index=$(git rev-parse --git-path index)
tmp=$(mktemp "${TMPDIR:-/tmp}/rimsky-src-tag.XXXXXX")
trap 'rm -f "$tmp"' EXIT
if [ -f "$index" ]; then cp "$index" "$tmp"; else rm -f "$tmp"; fi
cd "$root"
GIT_INDEX_FILE="$tmp" git add -A
tree=$(GIT_INDEX_FILE="$tmp" git write-tree)
printf 'src-%.12s\n' "$tree"
