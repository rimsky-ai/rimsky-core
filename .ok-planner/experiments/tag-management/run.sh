#!/usr/bin/env bash
# Experiment: tag-management
# Creates a movable name over a template hash, lists and resolves it,
# re-points it at a second hash, checks an instance created under the old
# binding keeps running against the hash it was created from, then removes
# the tag. Public CLI against a rimsky-all-in-one container.
set -uo pipefail

TAG="${RIMSKY_IMAGE_TAG:?set RIMSKY_IMAGE_TAG to the image tag built from this tree}"
HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/../../.." && pwd)"
CLI="$ROOT/bin/rimsky"
NAME="rimsky-exp-tag-management"
PORT="${PORT:-18102}"
E="http://127.0.0.1:$PORT"

fails=0
ok()   { echo "PASS  $*"; }
bad()  { echo "FAIL  $*"; fails=$((fails+1)); }
has()  { case "$2" in *"$1"*) ok "$3";; *) bad "$3 (missing '$1' in: $2)";; esac; }
hasnt(){ case "$2" in *"$1"*) bad "$3 (unexpected '$1' in: $2)";; *) ok "$3";; esac; }

docker rm -f "$NAME" >/dev/null 2>&1
docker run -d --name "$NAME" -p "127.0.0.1:$PORT:8080" "rimsky-all-in-one:$TAG" >/dev/null
trap 'docker rm -f "$NAME" >/dev/null 2>&1' EXIT
until "$CLI" health --endpoint "$E" >/dev/null 2>&1; do sleep 0.2; done

deploy() {
  local f="$1" id
  id="$("$CLI" template register "$f" --endpoint "$E" -o json 2>&1 | sed -n 's/.*"template_id": "\([^"]*\)".*/\1/p')"
  "$CLI" template deploy "$id" --endpoint "$E" -o json >/dev/null 2>&1
  printf '%s' "$id"
}

A="$(deploy "$HERE/template-v1.yml")"
B="$(deploy "$HERE/template-v2.yml")"
echo "v1 hash: $A"
echo "v2 hash: $B"
[ "$A" != "$B" ] && ok "the two template versions hash differently" || bad "both versions produced the same hash"

echo "--- create a movable name"
out="$("$CLI" tag create pipeline --template "$A" --endpoint "$E" 2>&1)"
has "pipeline" "$out" "tag create binds a name to a template"

echo "--- list and resolve"
lst="$("$CLI" tag list --endpoint "$E" -o json 2>&1)"
has "pipeline" "$lst" "tag list carries the new tag"
has "$A" "$lst" "tag list shows the bound template hash"
g="$("$CLI" tag get pipeline --endpoint "$E" -o json 2>&1)"
has "$A" "$g" "tag get resolves the tag to the bound hash"

echo "--- the tag is usable as a template ref"
out="$("$CLI" instance create pipeline --endpoint "$E" -o json 2>&1)"
IN1="$(printf '%s' "$out" | sed -n 's/.*"instance_id": "\([^"]*\)".*/\1/p')"
has "$A" "$out" "an instance created via the tag binds to the tagged hash"

echo "--- roll forward"
out="$("$CLI" tag mv pipeline --template "$B" --endpoint "$E" 2>&1)"
g="$("$CLI" tag get pipeline --endpoint "$E" -o json 2>&1)"
has "$B" "$g" "tag mv re-points the name at the new hash"
hasnt "$A" "$g" "the old hash is no longer what the name resolves to"
out="$("$CLI" instance create pipeline --endpoint "$E" -o json 2>&1)"
IN2="$(printf '%s' "$out" | sed -n 's/.*"instance_id": "\([^"]*\)".*/\1/p')"
has "$B" "$out" "an instance created after the move binds to the new hash"

echo "--- the in-flight instance is undisturbed"
g="$("$CLI" instance get "$IN1" --endpoint "$E" -o json 2>&1)"
has "$A" "$g" "the instance created before the move still runs the hash it was created from"
hasnt '"terminated_at"' "$g" "moving the tag did not terminate the in-flight instance"

echo "--- roll back"
"$CLI" tag mv pipeline --template "$A" --endpoint "$E" >/dev/null 2>&1
g="$("$CLI" tag get pipeline --endpoint "$E" -o json 2>&1)"
has "$A" "$g" "tag mv rolls the name back to the earlier hash"

echo "--- remove the name"
out="$("$CLI" tag rm pipeline --endpoint "$E" --yes 2>&1)"
lst="$("$CLI" tag list --endpoint "$E" -o json 2>&1)"
hasnt "pipeline" "$lst" "a removed tag is gone from the tag list"
out="$("$CLI" instance create pipeline --endpoint "$E" -o json 2>&1)"
hasnt '"instance_id"' "$out" "a removed tag no longer resolves as a template ref"
g="$("$CLI" instance get "$IN2" --endpoint "$E" -o json 2>&1)"
has "$B" "$g" "removing the tag left the instances it created running"

echo
if [ "$fails" -eq 0 ]; then echo "EXPERIMENT PASS"; else echo "EXPERIMENT FAIL ($fails)"; fi
exit "$fails"
