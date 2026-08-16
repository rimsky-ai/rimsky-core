#!/bin/bash
# Experiment: assumption cli-time-window-flags-uniform.
#
# Builds the acceptance matrix for the four time-window flags the prior names
# -- --since, --until, --before, --older-than -- across every time-ordered
# read the CLI offers, then asks a live deployment what timestamp grammar the
# accepted ones take. The parser settles acceptance without a server; the
# endpoint points at a closed port, so "connection refused" means accepted and
# "flag provided but not defined" means not.
#
# Requires: docker, python3, a rimsky CLI binary (RIMSKY_BIN), RIMSKY_IMAGE_TAG.
set -u

RIMSKY_BIN=${RIMSKY_BIN:?set RIMSKY_BIN to the rimsky CLI binary}
TAG=${RIMSKY_IMAGE_TAG:?set RIMSKY_IMAGE_TAG}
free_port() { python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()'; }
PORT=${PORT:-$(free_port)}
NAME=exp-assumption-cli-time-window
BASE="http://127.0.0.1:$PORT"
TS=2026-01-01T00:00:00Z

WORK=$(mktemp -d); export HOME="$WORK/home"; mkdir -p "$HOME"
unset RIMSKY_API_KEY RIMSKY_CONTEXT
cleanup() { docker rm -f "$NAME" >/dev/null 2>&1; }
trap cleanup EXIT

fail=0
pass() { echo "PASS  $1"; }
bad()  { echo "FAIL  $1"; fail=1; }

VERBS=( "logs i" "instance events i" "messages tail --instance i" "watch i"
        "parked list" "lineage prune" "asset lineage --instance i a" "instance nodes i" )
FLAGS=( --since --until --before --older-than )

echo "== acceptance matrix: ${#FLAGS[@]} flags over ${#VERBS[@]} time-ordered reads =="
export RIMSKY_CONTROL_API_URL=http://127.0.0.1:1
declare -a wholly_uniform
for f in "${FLAGS[@]}"; do
  taken=0; row=""
  for v in "${VERBS[@]}"; do
    read -r -a words <<<"$v"
    out=$("$RIMSKY_BIN" "${words[@]}" "$f" "$TS" 2>&1 | head -1)
    case "$out" in
      *"not defined"*) mark="-" ;;
      *"must be idle"*) mark="!" ;;
      *) mark="y"; taken=$((taken+1)) ;;
    esac
    row="$row $(printf '%-28s' "$v")$mark"$'\n'
  done
  echo "  $f: accepted by $taken of ${#VERBS[@]}"
  printf '%s' "$row" | sed 's/^/     /'
  [ $taken -eq ${#VERBS[@]} ] && wholly_uniform+=("$f")
done
echo "  legend: y accepted, - undefined, ! accepted but refuses a timestamp"
if [ ${#wholly_uniform[@]} -eq ${#FLAGS[@]} ]; then
  pass "all four flags are accepted by every time-ordered read"
else
  bad "${#wholly_uniform[@]} of ${#FLAGS[@]} flags are accepted uniformly"
fi

echo
echo "== the flag that is accepted everywhere it appears but means something else =="
out=$("$RIMSKY_BIN" watch i --until "$TS" 2>&1 | head -1)
echo "     rimsky watch <id> --until $TS → $out"
if printf '%s' "$out" | grep -q "must be idle"; then
  bad "watch --until takes a state name, not a timestamp, while instance events --until takes a timestamp"
else
  pass "watch --until takes the same timestamp grammar as instance events --until"
fi

echo
echo "== is every time-ordered read reachable at all? =="
if "$RIMSKY_BIN" --help 2>&1 | grep -qi 'audit'; then
  pass "the audit log has a CLI verb"
else
  bad "GET /v1/audit — the platform's own chronological read — has no CLI verb to put a window on"
fi
unset RIMSKY_CONTROL_API_URL

echo
echo "== timestamp grammar, against a live deployment =="
docker rm -f "$NAME" >/dev/null 2>&1
docker run -d --name "$NAME" -p "$PORT:8080" "rimsky-all-in-one:$TAG" >/dev/null || exit 1
for _ in $(seq 1 90); do
  [ "$(curl -s -m 2 -o /dev/null -w '%{http_code}' "$BASE/v1/health")" = 200 ] && break; sleep 1
done
[ "$(curl -s -m 2 -o /dev/null -w '%{http_code}' "$BASE/v1/health")" = 200 ] || { echo "FAIL  stack did not come up"; exit 1; }
export RIMSKY_CONTROL_API_URL="$BASE"
cat > "$WORK/t.yml" <<'EOF'
name: time-window-probe
version: "1"
nodes:
  - type: verify
    executor: verifier-shape-checks
EOF
H=$("$RIMSKY_BIN" template register "$WORK/t.yml" -o json | python3 -c 'import json,sys;print(json.load(sys.stdin)["template_id"])')
"$RIMSKY_BIN" template deploy "$H" >/dev/null
I=$("$RIMSKY_BIN" instance create "$H" -o json | python3 -c 'import json,sys;print(json.load(sys.stdin)["instance_id"])')
for val in "$TS" "2026-01-01" "5m" "1754000000"; do
  out=$("$RIMSKY_BIN" instance events "$I" --since "$val" 2>&1 | head -1 | cut -c1-72)
  printf '     instance events --since %-22s → %s\n' "$val" "${out:-(accepted, no events)}"
done
for val in "$TS" "5m"; do
  out=$("$RIMSKY_BIN" lineage prune --before "$val" 2>&1 | head -1 | cut -c1-72)
  printf '     lineage prune  --before %-22s → %s\n' "$val" "$out"
done

echo
if [ $fail -eq 0 ]; then echo "RESULT: PASS"; else echo "RESULT: FAIL"; fi
exit $fail
