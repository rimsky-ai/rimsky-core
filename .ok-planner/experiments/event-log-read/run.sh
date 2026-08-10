#!/usr/bin/env bash
# Experiment: event-log-read
#
# Provokes four different kinds of activity on ONE instance — node lifecycle
# transitions, a breakpoint hit, message activity in both directions, and the
# supervisor's own decisions (retry, park-resume, claim/lock bookkeeping) —
# then reconstructs the run from the single event feed:
#
#   one feed carries all four              -> the kinds are all present
#   true chronological order across kinds  -> the feed's order is the clock's
#   the kinds interleave                   -> the feed is not grouped by kind
#   filter by kind                         -> narrows, agrees with the whole
#   filter by time                         -> a window narrows both ends
#   malformed filters                      -> rejected rather than ignored
#
# Only built-in executors are used, so the run needs nothing but the stack.

set -u
cd "$(dirname "$0")"
: "${RIMSKY_IMAGE_TAG:?export RIMSKY_IMAGE_TAG=src-<tree hash> first}"
STACK=exp-eventlog-stack
PORT=${PORT:-19313}
BASE="http://127.0.0.1:$PORT"

fail=0
note() { printf '%s\n' "$*"; }
check() {
  if [ "$2" = "$3" ]; then printf 'PASS  %-64s %s\n' "$1" "$3"
  else printf 'FAIL  %-64s expected [%s] got [%s]\n' "$1" "$2" "$3"; fail=1; fi
}
cleanup() { docker rm -f "$STACK" >/dev/null 2>&1; }
trap cleanup EXIT

req() { local m=$1 p=$2 b=${3:-}
  curl -sS -m 20 -w '\n%{http_code}' -X "$m" -H 'content-type: application/json' \
    -H "Idempotency-Key: el-$RANDOM$RANDOM" ${b:+-d "$b"} "$BASE$p"; }
code() { req "$@" | tail -1; }
body() { req "$@" | sed '$d'; }

note "== bring up a stack =="
docker rm -f "$STACK" >/dev/null 2>&1
docker run -d --name "$STACK" -p "$PORT:8080" "rimsky-all-in-one:$RIMSKY_IMAGE_TAG" >/dev/null || exit 1
until [ "$(code GET /v1/health)" = 200 ]; do sleep 0.5; done

note
note "== a template that produces lifecycle, message and supervisor activity =="
SPEC='{"tag":"eventlog","spec":{"name":"eventlog","version":"1",
 "messages":[{"type":"msg/ping","body_schema":{"type":"object","properties":{"n":{"type":"integer"}}}}],
 "nodes":[
  {"type":"trigger","kind":"loop_counter","attributes":{"schema":{"type":"object","properties":{"max":{"type":"integer","default":3},"count":{"type":"integer"}}}}},
  {"type":"announcer","sends_message":"msg/ping","subscribes":[{"node":"trigger","type":"attribute/count/changed","force_upstream_refresh":false}],"attributes":{"schema":{"type":"object","properties":{"n":{"type":"integer","default":7}}}}},
  {"type":"listener","kind":"attribute_passthrough","subscribes":[{"node":"msg/ping","type":"terminal/success","force_upstream_refresh":false}],"attributes":{"schema":{"type":"object","properties":{"seen":{"type":"integer","default":1}}}}}
]}}'
REG=$(body POST /v1/templates "$SPEC")
TPL=$(printf '%s' "$REG" | jq -r '.template_id // empty')
check "template registered" yes "$([ -n "$TPL" ] && echo yes || echo no)"
[ -n "$TPL" ] || { note "register response: $REG"; note "EXPERIMENT FAIL"; exit 1; }
check "template deployed" 200 "$(code POST "/v1/templates/$TPL/deploy" '{}')"
IID=$(body POST /v1/instances "{\"template\":\"$TPL\",\"instance_key\":\"eventlog-1\",\"params\":{},\"target_agent\":\"eventlog-agent\"}" | jq -r '.instance_id // empty')
check "instance created" yes "$([ -n "$IID" ] && echo yes || echo no)"
[ -n "$IID" ] || { note "EXPERIMENT FAIL"; exit 1; }

BP=$(body POST "/v1/instances/$IID/breakpoints" '{"checkpoint":"before_dispatch","mode":"notify_only","matcher":{"node_type":"listener"}}' | jq -r '.breakpoint_id // empty')
check "a breakpoint was armed on the listener" yes "$([ -n "$BP" ] && echo yes || echo no)"

MARK=$(date -u +%Y-%m-%dT%H:%M:%SZ)
code POST "/v1/instances/$IID/messages" '{"type":""}' >/dev/null
code POST "/v1/instances/$IID/messages" '{"type":"msg/ping","payload":{"n":42}}' >/dev/null

feed() { body GET "/v1/events?instance_id=$IID&limit=1000$1" | jq '.events'; }
note "waiting for the breakpoint to be hit (blocks until it is)"
until [ "$(body GET "/v1/instances/$IID/breakpoint-hits" | jq '[.hits[]?]|length')" -ge 1 ]; do sleep 0.5; done
MSGNODE=$(body GET "/v1/instances/$IID/nodes" | jq -r '.nodes[]|select(.node_type=="msg/ping")|.id')
note "waiting for both messages to be delivered to the graph (blocks until they are)"
until [ "$(feed '' | jq --arg m "$MSGNODE" '[.[]|select(.node_id==$m and .kind=="terminal/success")]|length')" -ge 2 ]; do sleep 0.5; done
note "waiting for the listener to settle on the second delivery (blocks until it does)"
until [ "$(feed '' | jq '[.[]|select(.kind=="work_completed")]|length')" -ge 4 ]; do sleep 0.5; done
EV=$(feed '')

note
note "== one feed, four kinds of activity =="
note "$(printf '%s' "$EV" | jq -r '[.[]|.kind]|group_by(.)|map("\(.[0])=\(length)")|join(" ")')"
check "node lifecycle transitions are in the feed" yes \
  "$(printf '%s' "$EV" | jq -r 'if ([.[]|select(.kind=="work_started")]|length)>0 and ([.[]|select(.kind|startswith("terminal/"))]|length)>0 then "yes" else "no" end')"
check "the breakpoint hit is in the same feed" yes \
  "$(printf '%s' "$EV" | jq -r 'if ([.[]|select(.kind=="breakpoint.hit")]|length)>0 then "yes" else "no" end')"
check "message activity is in the same feed, both the sent and the posted one" 2 \
  "$(printf '%s' "$EV" | jq --arg m "$MSGNODE" '[.[]|select(.node_id==$m and .kind=="terminal/success")]|length')"
check "the posted message body reached the graph through the feed" 42 \
  "$(printf '%s' "$EV" | jq --arg m "$MSGNODE" '[.[]|select(.node_id==$m and .kind=="attribute/n/changed")|.payload.value]|max')"
check "supervisor decisions are in the same feed" yes \
  "$(printf '%s' "$EV" | jq -r 'if ([.[]|select(.kind=="attributes_substituted")]|length)>0 and ([.[]|select(.kind=="work_completed")|select(.payload.supervisor_id!=null)]|length)>0 then "yes" else "no" end')"

note
note "== true chronological order across kinds =="
check "the feed is ordered by its sequence" yes \
  "$(printf '%s' "$EV" | jq -r 'if ([.[]|.id]) == ([.[]|.id]|sort|reverse) then "yes" else "no" end')"
check "the sequence agrees with the wall clock" yes \
  "$(printf '%s' "$EV" | jq -r 'if (sort_by(.id)|[.[].occurred_at]) == (sort_by(.id)|[.[].occurred_at]|sort) then "yes" else "no" end')"
check "the kinds interleave rather than group" yes \
  "$(printf '%s' "$EV" | jq -r 'sort_by(.id)|[.[].kind] as $k | if ([range(1;($k|length))|select($k[.]!=$k[.-1])]|length) > (($k|unique|length)-1) then "yes" else "no" end')"
note "the reconstructed run:"
printf '%s' "$EV" | jq -r 'sort_by(.id)|.[]|select(.kind=="work_started" or .kind=="breakpoint.hit" or .kind=="message_sent" or .kind=="message_received" or (.kind|startswith("terminal/")))|"    \(.id)  \(.occurred_at)  \(.kind)"' | head -24

note
note "== filtering by kind =="
for K in work_started work_completed breakpoint.hit attributes_substituted; do
  N_ALL=$(printf '%s' "$EV" | jq --arg k "$K" '[.[]|select(.kind==$k)]|length')
  N_F=$(feed "&kind=$K" | jq 'length')
  OTHERS=$(feed "&kind=$K" | jq --arg k "$K" '[.[]|select(.kind!=$k)]|length')
  check "kind=$K returns only that kind" 0 "$OTHERS"
  check "kind=$K returns the same count the whole feed carries" "$N_ALL" "$N_F"
done

note
note "== filtering by time =="
secs() { jq --arg t "$1" -n '$t|sub("\\.[0-9]+Z$";"Z")|fromdate'; }
MID=$(printf '%s' "$EV" | jq -r 'sort_by(.id)|.[(length/2|floor)].occurred_at|sub("\\.[0-9]+Z$";"Z")')
EXPECTED_TAIL=$(printf '%s' "$EV" | jq --arg t "$MID" '[.[]|select((.occurred_at|sub("\\.[0-9]+Z$";"Z")|fromdate) >= ($t|fromdate))]|length')
check "since= a mid-run second returns exactly the events at or after it" "$EXPECTED_TAIL" "$(feed "&since=$MID" | jq 'length')"
check "since= returns nothing earlier than its bound" 0 \
  "$(feed "&since=$MID" | jq --arg t "$MID" '[.[]|select((.occurred_at|sub("\\.[0-9]+Z$";"Z")|fromdate) < ($t|fromdate))]|length')"
check "since= is a real narrowing of the whole feed" yes \
  "$([ "$EXPECTED_TAIL" -lt "$(printf '%s' "$EV" | jq 'length')" ] && echo yes || echo no)"
check "until= the mark taken before the run returns nothing for this instance" 0 "$(feed "&until=$MARK" | jq 'length')"
check "the whole feed is bracketed by the two bounds" "$(printf '%s' "$EV" | jq 'length')" \
  "$(feed "&since=$MARK" | jq 'length')"

note
note "== malformed filters are rejected, not ignored =="
check "an unknown kind is a 400" 400 "$(code GET "/v1/events?instance_id=$IID&kind=not-a-kind")"
check "a non-RFC3339 since is a 400" 400 "$(code GET "/v1/events?instance_id=$IID&since=yesterday")"
check "a malformed instance id is a 400" 400 "$(code GET "/v1/events?instance_id=nope")"

note
if [ "$fail" = 0 ]; then note "EXPERIMENT PASS"; else note "EXPERIMENT FAIL"; fi
exit "$fail"
