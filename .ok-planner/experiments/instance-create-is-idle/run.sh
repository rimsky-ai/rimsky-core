#!/usr/bin/env bash
# Experiment: instance-create-is-idle
# Creates an instance and shows nothing runs as a side effect. The negative is
# anchored to a second instance rather than to the clock: instance B is created
# and woken, and only once B has completed its work — proving the scheduler ran
# — is the untouched instance A checked for having stayed idle. Then A is woken
# and runs, showing invoking work is a separate operator action.
set -uo pipefail

TAG="${RIMSKY_IMAGE_TAG:?set RIMSKY_IMAGE_TAG to the image tag built from this tree}"
HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/../../.." && pwd)"
CLI="$ROOT/bin/rimsky"
NAME="rimsky-exp-instance-create-is-idle"
PORT="${PORT:-18104}"
E="http://127.0.0.1:$PORT"

fails=0
ok()   { echo "PASS  $*"; }
bad()  { echo "FAIL  $*"; fails=$((fails+1)); }
has()  { case "$2" in *"$1"*) ok "$3";; *) bad "$3 (missing '$1' in: $2)";; esac; }

docker rm -f "$NAME" >/dev/null 2>&1
docker run -d --name "$NAME" -p "127.0.0.1:$PORT:8080" "rimsky-all-in-one:$TAG" >/dev/null
trap 'docker rm -f "$NAME" >/dev/null 2>&1' EXIT
until "$CLI" health --endpoint "$E" >/dev/null 2>&1; do sleep 0.2; done

ID="$("$CLI" template register "$HERE/template.yml" --endpoint "$E" -o json 2>&1 | sed -n 's/.*"template_id": "\([^"]*\)".*/\1/p')"
"$CLI" template deploy "$ID" --endpoint "$E" -o json >/dev/null 2>&1

create() { "$CLI" instance create "$ID" --endpoint "$E" -o json 2>&1 | sed -n 's/.*"instance_id": "\([^"]*\)".*/\1/p'; }
wake()   { curl -sS -XPOST -H 'Content-Type: application/json' -H "Idempotency-Key: $2" \
             -d '{"type":""}' "$E/v1/instances/$1/messages" >/dev/null; }
events() { "$CLI" instance events "$1" --endpoint "$E" -o json 2>/dev/null; }
count()  { events "$1" | grep -c '"kind"'; }

echo "--- create an instance"
A="$(create)"
[ -n "$A" ] && ok "instance create returns an instance id: $A" || bad "instance create failed"

echo "--- the graph exists but nothing has run"
n="$("$CLI" instance nodes "$A" --endpoint "$E" -o json 2>&1)"
has '"node_type": "root"' "$n" "creating an instance materializes its node graph"
case "$n" in
  *'"active_count": 0'*'"pending_count": 0'*'"fresh_count": 0'*)
    ok "every node run counter on the fresh instance is zero";;
  *) bad "a freshly created instance has non-zero run counters: $n";;
esac
[ "$(count "$A")" -eq 0 ] && ok "creating an instance emits no events at all" || bad "create emitted events: $(events "$A")"
m="$(curl -sS "$E/v1/instances/$A/messages" 2>&1)"
has '"messages":[]' "$m" "creating an instance enqueues no message"

echo "--- prove the scheduler is running, on a different instance"
B="$(create)"
wake "$B" "wake-b"
until [ "$(events "$B" | grep -c work_completed)" -gt 0 ]; do sleep 0.2; done
ok "instance B, explicitly woken, runs its node to completion"

echo "--- the untouched instance is still idle"
[ "$(count "$A")" -eq 0 ] && ok "instance A still has no events after B ran a full cycle" || bad "instance A emitted events without being invoked: $(events "$A")"
n="$("$CLI" instance nodes "$A" --endpoint "$E" -o json 2>&1)"
case "$n" in
  *'"active_count": 0'*'"pending_count": 0'*'"fresh_count": 0'*)
    ok "instance A's run counters are still zero";;
  *) bad "instance A ran something: $n";;
esac

echo "--- invoking work is the separate action"
wake "$A" "wake-a"
until [ "$(events "$A" | grep -c work_completed)" -gt 0 ]; do sleep 0.2; done
ok "instance A runs only once the operator invokes work on it"

echo
if [ "$fails" -eq 0 ]; then echo "EXPERIMENT PASS"; else echo "EXPERIMENT FAIL ($fails)"; fi
exit "$fails"
