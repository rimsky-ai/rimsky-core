#!/bin/sh
# Prune rimsky-built images (label org.rimsky.project=rimsky-core) older than
# REAP_HOURS (default 72), keeping any image ID still pointed to by a :latest
# or :$VERSION tag, then prune dangling rimsky-labeled layers. Content-
# addressed :src-* tags accumulate one per source tree built; this is the
# retention side of that scheme. Invoked by `make reap-images`.
set -eu
hours="${REAP_HOURS:-72}"
version="${VERSION:-}"
cutoff=$(date -u -v-"${hours}"H +%Y-%m-%dT%H:%M:%S 2>/dev/null \
  || date -u -d "${hours} hours ago" +%Y-%m-%dT%H:%M:%S)

refs=$(docker images --filter label=org.rimsky.project=rimsky-core \
  --format '{{.Repository}}:{{.Tag}} {{.ID}}' | grep -v '<none>' || true)

keep_ids=" "
while IFS=' ' read -r ref id; do
  [ -n "$ref" ] || continue
  case "$ref" in
    *:latest) keep_ids="${keep_ids}${id} " ;;
    *:"$version") [ -n "$version" ] && keep_ids="${keep_ids}${id} " ;;
  esac
done <<EOF
$refs
EOF

removed=0
while IFS=' ' read -r ref id; do
  [ -n "$ref" ] || continue
  case "$keep_ids" in *" $id "*) continue ;; esac
  created=$(docker image inspect -f '{{.Created}}' "$ref" | cut -c1-19)
  if [ "$created" \< "$cutoff" ]; then
    echo "reap: removing $ref (created ${created}Z, cutoff ${cutoff}Z)"
    docker rmi "$ref" >/dev/null
    removed=$((removed + 1))
  fi
done <<EOF
$refs
EOF

echo "reap: removed $removed tag(s) older than ${hours}h; pruning dangling rimsky layers"
docker image prune -f --filter label=org.rimsky.project=rimsky-core
