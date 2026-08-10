#!/usr/bin/env bash
# Experiment: instance-lifecycle
# Creates a live instance of a deployed template, watches its progress
# (nodes + event log), pauses it and shows queued work stays undelivered,
# resumes it and shows the work runs, force-terminates a second instance,
# and removes both records. Public CLI plus the public control-api routes
# against a rimsky-all-in-one container.
set -uo pipefail

TAG="${RIMSKY_IMAGE_TAG:?set RIMSKY_IMAGE_TAG to the image tag built from this tree}"
HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/../../.." && pwd)"
CLI="$ROOT/bin/rimsky"
NAME="rimsky-exp-instance-lifecycle"
PORT="${PORT:-18103}"
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

ID="$("$CLI" template register "$HERE/template.yml" --endpoint "$E" -o json 2>&1 | sed -n 's/.*"template_id": "\([^"]*\)".*/\1/p')"
"$CLI" template deploy "$ID" --endpoint "$E" -o json >/dev/null 2>&1

create() { "$CLI" instance create "$ID" --endpoint "$E" -o json 2>&1 | sed -n 's/.*"instance_id": "\([^"]*\)".*/\1/p'; }
wake()   { curl -sS -XPOST -H 'Content-Type: application/json' -H "Idempotency-Key: $2" \
             -d '{"type":""}' "$E/v1/instances/$1/messages" >/dev/null; }
completed() { "$CLI" instance events "$1" --endpoint "$E" -o json 2>/dev/null | grep -c work_completed; }

echo "--- create a live instance"
A="$(create)"
[ -n "$A" ] && ok "instance create returns an instance id: $A" || bad "instance create failed"
n="$("$CLI" instance nodes "$A" --endpoint "$E" -o json 2>&1)"
has '"node_type": "root"' "$n" "the new instance materializes its root node"

echo "--- watch its progress"
wake "$A" "wake-a"
until [ "$(completed "$A")" -gt 0 ]; do sleep 0.2; done
ok "the event log reports work_completed for the instance's node"
n="$("$CLI" instance nodes "$A" --endpoint "$E" -o json 2>&1)"
has '"fresh_count": 1' "$n" "instance nodes reports the node fresh after its run"
st="$("$CLI" instance status "$A" --endpoint "$E" -o json 2>&1)"
has '"terminal/success"' "$st" "instance status reports the node's settling signal"

echo "--- pause holds work"
B="$(create)"
p="$(curl -sS -XPOST "$E/v1/instances/$B/pause" 2>&1)"
has '"paused":true' "$p" "pause reports the instance paused"
g="$(curl -sS "$E/v1/instances/$B" 2>&1)"
has '"paused":true' "$g" "the instance reads back as paused"
wake "$B" "wake-b"
until curl -sS "$E/v1/instances/$B/messages" | grep -q '"id"'; do sleep 0.2; done
m="$(curl -sS "$E/v1/instances/$B/messages" 2>&1)"
hasnt '"delivered_at"' "$m" "a message posted to a paused instance stays undelivered"
[ "$(completed "$B")" -eq 0 ] && ok "no work ran while the instance was paused" || bad "work ran while paused"

echo "--- resume releases it"
r="$(curl -sS -XPOST "$E/v1/instances/$B/resume" 2>&1)"
has '"resumed":true' "$r" "resume reports the instance resumed"
until [ "$(completed "$B")" -gt 0 ]; do sleep 0.2; done
ok "the held work runs once the instance is resumed"

echo "--- force-terminate"
t="$(curl -sS -XPOST -H 'Content-Type: application/json' -d '{"reason":"audit probe","force":true}' \
      "$E/v1/instances/$A/terminate" 2>&1)"
has '"terminated_at"' "$t" "terminate stamps the instance terminated"
g="$(curl -sS "$E/v1/instances/$A" 2>&1)"
has '"terminated_at"' "$g" "the terminated instance reads back terminated"
k="$("$CLI" instance kill "$B" --force --endpoint "$E" -o json 2>&1)"
has '"terminated_at"' "$k" "instance kill terminates from the CLI too"

echo "--- remove the records"
"$CLI" instance delete "$A" --endpoint "$E" --yes >/dev/null 2>&1
"$CLI" instance delete "$B" --endpoint "$E" --yes >/dev/null 2>&1
lst="$("$CLI" instance list --endpoint "$E" -o json 2>&1)"
hasnt "$A" "$lst" "the deleted instance is gone from the instance list"
hasnt "$B" "$lst" "the second deleted instance is gone from the instance list"

echo
if [ "$fails" -eq 0 ]; then echo "EXPERIMENT PASS"; else echo "EXPERIMENT FAIL ($fails)"; fi
exit "$fails"
