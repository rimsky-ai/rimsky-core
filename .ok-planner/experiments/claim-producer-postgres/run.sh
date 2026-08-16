#!/usr/bin/env bash
# Experiment: claim-producer-postgres
# A Postgres database, two bundled postgres claim producers over it (one sync
# with a pick policy, one staged-async with its verifier executor enabled) and
# a rimsky stack pointed at both. The run shows a pick policy handing distinct
# rows to distinct claimants, the producer's own claim-unavailable class
# arriving as a subscribable signal, a staged-async claim resolving to a
# staging schema whose content replaces the canonical schema at commit, and a
# row-count-ratio verifier check over that staged content passing and failing.
set -uo pipefail

TAG="${RIMSKY_IMAGE_TAG:?set RIMSKY_IMAGE_TAG to the image tag built from this tree}"
HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/../../.." && pwd)"
CLI="$ROOT/bin/rimsky"
NET="rimsky-exp-cpp-net"
PG="rimsky-exp-cpp-pg"
CPQ="cp-queue"
CPS="cp-staged"
STACK="rimsky-exp-cpp-stack"
PORT="${PORT:-18208}"
REC_PORT="${REC_PORT:-19489}"
E="http://127.0.0.1:$PORT"
WORK="$(mktemp -d)"

fails=0
ok()  { echo "PASS  $*"; }
bad() { echo "FAIL  $*"; fails=$((fails+1)); }
has() { if printf '%s' "$2" | grep -qF -- "$1"; then ok "$3"; else bad "$3 (missing '$1')"; fi; }

PIDS=()
cleanup() {
  for p in "${PIDS[@]:-}"; do kill "$p" >/dev/null 2>&1; done
  docker rm -f "$STACK" "$CPQ" "$CPS" "$PG" >/dev/null 2>&1
  docker network rm "$NET" >/dev/null 2>&1
  rm -rf "$WORK"
}
trap cleanup EXIT

if nc -z 127.0.0.1 "$REC_PORT" >/dev/null 2>&1; then echo "port $REC_PORT already in use" >&2; exit 2; fi
if nc -z 127.0.0.1 "$PORT" >/dev/null 2>&1; then echo "port $PORT already in use" >&2; exit 2; fi

docker rm -f "$STACK" "$CPQ" "$CPS" "$PG" >/dev/null 2>&1
docker network rm "$NET" >/dev/null 2>&1
docker network create "$NET" >/dev/null

docker run -d --name "$PG" --network "$NET" --network-alias substrate-pg \
  -e POSTGRES_USER=store -e POSTGRES_PASSWORD=store -e POSTGRES_DB=storedb \
  postgres:15-alpine >/dev/null
until [ "$(docker logs "$PG" 2>&1 | grep -c 'database system is ready to accept connections')" -ge 2 ]; do sleep 0.3; done
until docker exec "$PG" pg_isready -U store -d storedb >/dev/null 2>&1; do sleep 0.3; done

psql() { docker exec -i "$PG" psql -U store -d storedb -v ON_ERROR_STOP=1 "$@"; }
psql -f - < "$HERE/seed.sql" >/dev/null
ok "the substrate database carries the pick policy's items table and a canonical schema"

docker run -d --name "$CPQ" --network "$NET" --network-alias "$CPQ" \
  -e RIMSKY_CLAIM_PRODUCER_POSTGRES_CONFIG=/etc/store/config.yml \
  -v "$HERE/cp-queue.yml:/etc/store/config.yml:ro" \
  "rimsky-claim-producer-postgres:$TAG" >/dev/null
docker run -d --name "$CPS" --network "$NET" --network-alias "$CPS" \
  -e RIMSKY_CLAIM_PRODUCER_POSTGRES_CONFIG=/etc/store/config.yml \
  -v "$HERE/cp-staged.yml:/etc/store/config.yml:ro" \
  "rimsky-claim-producer-postgres:$TAG" >/dev/null
for c in "$CPQ" "$CPS"; do
  until docker logs "$c" 2>&1 | grep -q 'claim-producer-postgres started'; do sleep 0.3; done
  [ "$(docker inspect -f '{{.State.Running}}' "$c")" = "true" ] || {
    echo "producer $c exited: $(docker logs "$c" 2>&1 | tail -2)" >&2; exit 2; }
done
ok "both bundled postgres claim producers started against that database"

python3 "$HERE/sqlwriter.py" "$REC_PORT" "$PG" >"$WORK/writer.log" 2>&1 &
PIDS+=("$!")
until curl -sS "http://127.0.0.1:$REC_PORT/log" >/dev/null 2>&1; do sleep 0.1; done

docker run -d --name "$STACK" --network "$NET" -p "127.0.0.1:$PORT:8080" \
  -e RIMSKY_EXECUTOR_HTTP_NODE_EGRESS_ALLOWLIST=0.0.0.0/0 \
  -v "$HERE/rimsky.yml:/etc/rimsky/rimsky.yml:ro" \
  "rimsky-all-in-one:$TAG" >/dev/null
until "$CLI" health --endpoint "$E" >/dev/null 2>&1; do sleep 0.2; done

register() {
  curl -sS -XPOST -H 'Content-Type: application/json' \
    -d "{\"spec\": $(cat "$1")}" "$E/v1/templates" \
    | sed -n 's/.*"template_id":"\([^"]*\)".*/\1/p'
}
start() {
  local f="$1" id in
  id="$(register "$f")"
  [ -z "$id" ] && { echo "REGISTER FAILED: $f -- $(curl -sS -XPOST -H 'Content-Type: application/json' -d "{\"spec\": $(cat "$f")}" "$E/v1/templates")" >&2; return 1; }
  "$CLI" template deploy "$id" --endpoint "$E" -o json >/dev/null 2>&1
  in="$("$CLI" instance create "$id" --endpoint "$E" -o json 2>&1 | sed -n 's/.*"instance_id": "\([^"]*\)".*/\1/p')"
  [ -z "$in" ] && { echo "CREATE FAILED: $f" >&2; return 1; }
  curl -sS -XPOST -H 'Content-Type: application/json' -H "Idempotency-Key: wake-$in" \
    -d '{"type":""}' "$E/v1/instances/$in/messages" >/dev/null
  printf '%s' "$in"
}
node_states() {
  "$CLI" instance nodes "$1" --endpoint "$E" -o json 2>/dev/null | python3 -c '
import sys, json
try: rows = json.load(sys.stdin)
except Exception: raise SystemExit
for d in rows:
    t = d.get("node_type")
    if not t: continue
    s = d["run_summary"]
    print("%s fresh=%d failed=%d signal=%s" % (t, s["fresh_count"], s["failed_count"], d.get("settling_signal_type")))'
}
settle() {
  until node_states "$1" | grep -qE "^$2 .*(fresh=[1-9]|failed=[1-9])"; do sleep 0.3; done
  node_states "$1" | grep -E "^$2 "
}
rec_for() {
  curl -sS "http://127.0.0.1:$REC_PORT/log" | python3 -c "
import sys, json
for e in json.load(sys.stdin):
    if e['path'] == '$1': print(json.dumps(e))"
}
handles() { curl -sS "$E/v1/observability/claim-handles?limit=200"; }

echo "--- a configurable pick policy hands a distinct row to each claimant"
A="$(start "$HERE/template-queue.json")" || bad "the queue template did not register"
settle "$A" worker >/dev/null
B="$(start "$HERE/template-queue.json")" || bad "the queue template did not register"
settle "$B" worker >/dev/null
st="$(node_states "$A")$(node_states "$B")"; echo "    $(printf '%s' "$st" | tr '\n' ' ')"
picked="$(rec_for /rec/worker | python3 -c "
import sys, json
ids = []
for line in sys.stdin:
    b = json.loads(line)['body']
    if b.get('item_id'): ids.append((b['item_id'], b.get('topic')))
print(json.dumps(sorted(set(ids))))")"
echo "    picked: $picked"
has 'job-alpha' "$picked" "the first claimant received one of the seeded rows"
has 'job-beta' "$picked" "the second claimant received the other seeded row, not the same one"
has 'alpha' "$picked" "the claimed row's payload reached the node's dispatch"
ch="$(handles | python3 -c "
import sys, json
for h in json.load(sys.stdin)['claim_handles']:
    if h.get('producer_name') == 'queue-store': print(json.dumps(h)); break")"
echo "    $ch"
has '"realized_write_semantics": "sync"' "$ch" "the pick-policy producer realizes its claims as synchronous writes"

echo "--- the producer's own claim-unavailable class arrives as a subscribable signal"
C="$(start "$HERE/template-drained.json")" || bad "the drained template did not register"
st="$(settle "$C" worker)"; echo "    $st"
has "signal=terminal/error/pg/claim_unavailable" "$st" "the drained pick policy settles the node on the producer's declared class"
st="$(settle "$C" watcher)"; echo "    $st"
has "watcher fresh=1 failed=0" "$st" "a node subscribed to the producer's error classes ran on that signal"

echo "--- a staged-async claim resolves to a staging schema, and commit swaps it in"
before="$(psql -tAc "SELECT count(*) FROM analytics_production.items;")"
echo "    canonical row count before: $before"
D="$(start "$HERE/template-staged-pass.json")" || bad "the staged template did not register"
st="$(settle "$D" stager)"; echo "    $st"
has "stager fresh=1 failed=0" "$st" "the staging node settled fresh"
stage="$(rec_for /stage | tail -1)"; echo "    $stage"
schema="$(printf '%s' "$stage" | python3 -c 'import sys,json; print(json.load(sys.stdin)["body"]["staging_schema"])')"
case "$schema" in rimsky_stg_*) ok "the claim's address is a staging schema ($schema), not the canonical one";;
  *) bad "the claim's address is $schema, which is not a staging schema";; esac
ch="$(handles | python3 -c "
import sys, json
for h in json.load(sys.stdin)['claim_handles']:
    if h.get('producer_name') == 'staged-store': print(json.dumps(h)); break")"
echo "    $ch"
has '"realized_write_semantics": "staged_async"' "$ch" "the claim handle records staged-async, not a downgrade to sync"

echo "--- the verifier's row-count-ratio check runs over the staged content"
st="$(settle "$D" verifier)"; echo "    $st"
has "verifier fresh=1 failed=0" "$st" "the row-count-ratio check passed on the staged schema"
until [ "$(psql -tAc "SELECT count(*) FROM analytics_production.items;" | tr -d ' ')" = "10" ]; do sleep 0.3; done
ok "after commit the canonical schema holds the 10 staged rows, replacing the 1 it had"
gone="$(psql -tAc "SELECT count(*) FROM information_schema.schemata WHERE schema_name = '$schema';" | tr -d ' ')"
[ "$gone" = "0" ] && ok "the staging schema is gone: it was swapped in, not copied" || bad "the staging schema still exists"

echo "--- a failing check settles the node on the producer's per-check error class"
psql -c "CREATE SCHEMA IF NOT EXISTS analytics_reporting; CREATE TABLE IF NOT EXISTS analytics_reporting.items (id INT PRIMARY KEY, label TEXT);" >/dev/null
G="$(start "$HERE/template-staged-fail.json")" || bad "the failing template did not register"
st="$(settle "$G" stager)"; echo "    $st"
st="$(settle "$G" checker)"; echo "    $st"
has "signal=terminal/error/pg/verifier_check_failed/row_count_ratio" "$st" "the node settles on the producer's per-check error class"
st="$(settle "$G" watcher)"; echo "    $st"
has "watcher fresh=1 failed=0" "$st" "a node subscribed to the producer's error classes ran on that signal"

echo "--- the error classes the producer advertises, as an operator reads them"
entry="$(curl -sS "$E/v1/observability/claim-producers" | python3 -c "
import sys, json
for e in json.load(sys.stdin)['claim_producers']:
    if e['name'] == 'staged-store': print(json.dumps(e))")"
declared="$(printf '%s' "$entry" | python3 -c "
import sys, json
d = json.load(sys.stdin)
print(json.dumps(d.get('observability_capabilities', {}).get('declared_error_classes', [])))")"
echo "    advertised: $declared"
has 'pg/claim_unavailable' "$declared" "the producer advertises its claim-unavailable class"
has 'pg/swap_failed' "$declared" "the producer advertises its swap-failed class"
has 'pg/not_atomically_replaceable' "$declared" "the producer advertises its not-atomically-replaceable class"
n="$(printf '%s' "$declared" | python3 -c 'import sys,json; print(len(json.load(sys.stdin)))')"
[ "$n" = "3" ] && ok "the advertisement carries exactly 3 classes" || bad "the advertisement carries $n classes, not 3"

echo "--- a canonical schema another object depends on is refused, and nothing is destroyed"
psql -c "CREATE SCHEMA IF NOT EXISTS analytics_swapfail;
         CREATE TABLE IF NOT EXISTS analytics_swapfail.items (id INT PRIMARY KEY, label TEXT);
         INSERT INTO analytics_swapfail.items (id, label) VALUES (1, 'stale') ON CONFLICT DO NOTHING;
         CREATE SCHEMA IF NOT EXISTS reporting_ext;
         CREATE OR REPLACE VIEW reporting_ext.items_view AS SELECT * FROM analytics_swapfail.items;" >/dev/null
ok "an operator's own reporting view outside the canonical schema depends on it"
S="$(start "$HERE/template-staged-dependent.json")" || bad "the dependent-schema template did not register"
st="$(settle "$S" stager)"; echo "    $st"
has "signal=terminal/error/pg/not_atomically_replaceable" "$st" "the claim is refused on the producer's not-atomically-replaceable class"
st="$(settle "$S" watcher)"; echo "    $st"
has "watcher fresh=1 failed=0" "$st" "a node subscribed to the producer's error classes ran on that signal"
kept="$(psql -tAc "SELECT count(*) FROM analytics_swapfail.items;" | tr -d ' ')"
[ "$kept" = "1" ] && ok "the canonical schema and its dependent are untouched" || bad "the canonical schema holds $kept rows"
echo "    not exercised: pg/swap_failed — advertised, but no public-surface route provoked it in this run"

echo
if [ "$fails" -eq 0 ]; then echo "EXPERIMENT PASS"; else echo "EXPERIMENT FAIL ($fails)"; fi
exit "$fails"
