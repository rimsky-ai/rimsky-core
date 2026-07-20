#!/bin/sh
# Copyright © 2026 Fall Guy Consulting.
# Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
# license. See LICENSE.agpl and COPYRIGHT at the repo root.

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
