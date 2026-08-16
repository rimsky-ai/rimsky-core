#!/bin/bash
# Experiment: assumption cli-duration-flags-share-syntax.
#
# Feeds one vocabulary of duration literals to every duration-shaped flag and
# config key the prior names, and records which parse where. Three surfaces:
#
#   locally parsed flags  --poll-interval, --older-than, --timeout
#   server-parsed flags   --expires, --grace
#   config keys           dispatch_defaults.*, retention.*, blob retention
#
# The locally parsed flags need no server. The server-parsed ones and the
# config keys need a live deployment, so the run boots one rimsky-all-in-one
# and, for the config keys, boots a second container per config file and asks
# whether it comes up healthy.
#
# Requires: docker, python3, a rimsky CLI binary (RIMSKY_BIN), RIMSKY_IMAGE_TAG.
set -u

RIMSKY_BIN=${RIMSKY_BIN:?set RIMSKY_BIN to the rimsky CLI binary}
TAG=${RIMSKY_IMAGE_TAG:?set RIMSKY_IMAGE_TAG}
free_port() { python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()'; }
PORT=${PORT:-$(free_port)}
NAME=exp-assumption-cli-duration
CFGNAME=exp-assumption-cli-duration-cfg
BASE="http://127.0.0.1:$PORT"

WORK=$(mktemp -d); export HOME="$WORK/home"; mkdir -p "$HOME"
unset RIMSKY_API_KEY RIMSKY_CONTEXT
cleanup() { docker rm -f "$NAME" "$CFGNAME" >/dev/null 2>&1; }
trap cleanup EXIT

fail=0
pass() { echo "PASS  $1"; }
bad()  { echo "FAIL  $1"; fail=1; }

VALUES=( 30s 5m 24h 1h30m 500ms 30d 1w )

echo "== locally parsed duration flags =="
export RIMSKY_CONTROL_API_URL=http://127.0.0.1:1
LOCAL=( "instance events i --poll-interval" "messages tail --instance i --poll-interval"
        "watch i --poll-interval" "parked list --older-than" "lineage prune --older-than"
        "run f.yml --timeout" "compose run --timeout" "conformance executor --timeout" )
declare -a rows
for probe in "${LOCAL[@]}"; do
  read -r -a words <<<"$probe"
  row=""
  for v in "${VALUES[@]}"; do
    out=$("$RIMSKY_BIN" "${words[@]}" "$v" 2>&1 | head -1)
    case "$out" in *"invalid value"*) row="$row  no" ;; *) row="$row yes" ;; esac
  done
  echo "     $(printf '%-46s' "$probe")$row"
  rows+=("$row")
done
printf '     %-46s' "values"; printf '%4s' "${VALUES[@]}"; echo
uniq_rows=$(printf '%s\n' "${rows[@]}" | sort -u | wc -l | tr -d ' ')
if [ "$uniq_rows" = 1 ]; then
  echo "     all ${#LOCAL[@]} locally parsed flags agree with each other"
else
  bad "the locally parsed flags disagree among themselves ($uniq_rows distinct grammars)"
fi
local_row=${rows[0]}

echo
echo "== a duration-shaped flag with no units at all =="
for v in 30 30s 5m; do
  out=$("$RIMSKY_BIN" conformance executor --retention-test-seconds "$v" 2>&1 | head -1 | cut -c1-64)
  printf '     --retention-test-seconds %-8s → %s\n' "$v" "$out"
done
unset RIMSKY_CONTROL_API_URL

echo
echo "== server-parsed duration flags =="
docker rm -f "$NAME" >/dev/null 2>&1
docker run -d --name "$NAME" -p "$PORT:8080" "rimsky-all-in-one:$TAG" >/dev/null || exit 1
for _ in $(seq 1 90); do
  [ "$(curl -s -m 2 -o /dev/null -w '%{http_code}' "$BASE/v1/health")" = 200 ] && break; sleep 1
done
[ "$(curl -s -m 2 -o /dev/null -w '%{http_code}' "$BASE/v1/health")" = 200 ] || { echo "FAIL  stack did not come up"; exit 1; }
export RIMSKY_CONTROL_API_URL="$BASE"
ADMIN=$("$RIMSKY_BIN" auth init | awk 'NF==1 && length($1)>20 {print $1; exit}')
export RIMSKY_API_KEY="$ADMIN"

exp_row=""; n=0
for v in "${VALUES[@]}"; do
  n=$((n+1))
  out=$("$RIMSKY_BIN" auth create-key --name="exp-$n" --role=read-only --expires "$v" 2>&1)
  case "$out" in *"invalid"*|*"400"*) exp_row="$exp_row  no" ;; *) exp_row="$exp_row yes" ;; esac
done
"$RIMSKY_BIN" auth create-key --name=rotated --role=read-only >/dev/null 2>&1
grace_row=""
for v in "${VALUES[@]}"; do
  out=$("$RIMSKY_BIN" auth rotate rotated --grace "$v" 2>&1)
  case "$out" in *"invalid"*|*"400"*) grace_row="$grace_row  no" ;; *) grace_row="$grace_row yes" ;; esac
done
printf '     %-46s' "values"; printf '%4s' "${VALUES[@]}"; echo
echo "     $(printf '%-46s' "--poll-interval / --older-than / --timeout")$local_row"
echo "     $(printf '%-46s' "auth create-key --expires")$exp_row"
echo "     $(printf '%-46s' "auth rotate --grace")$grace_row"
if [ "$exp_row" = "$local_row" ] && [ "$grace_row" = "$local_row" ]; then
  pass "every duration flag takes the same grammar"
else
  bad "the duration flags do not share one grammar"
fi

echo
echo "== duration-shaped config keys =="
mk_cfg() { # mk_cfg <file> <value-for-sync_rpc_deadline>
  cat > "$1" <<CFG
persistence:
  driver: sqlite
  sqlite:
    path: /var/lib/rimsky/state.db
  blob:
    backend: memory
    retention:
      orphan_sweep_interval: 30s
      retention_after_unreferenced: 5m
retention:
  recent_frames_kept: 5
  trace_trailing: 30s
  lineage_trailing: 5m
  claim_handles_trailing: 24h
  message_idempotencies_trailing: 30s
dispatch_defaults:
  sync_rpc_deadline: $2
  max_quiet_period: 5m
  max_runtime: 24h
claim_producers: {}
named_locks: {}
executors: {}
CFG
}
boots() { # boots <config-file> -> healthy|dead, prints the reason when dead
  local p; p=$(free_port)
  docker rm -f "$CFGNAME" >/dev/null 2>&1
  docker run -d --name "$CFGNAME" -p "$p:8080" -v "$1:/etc/rimsky/rimsky.yml:ro" \
    "rimsky-all-in-one:$TAG" >/dev/null || { echo dead; return; }
  for _ in $(seq 1 30); do
    [ "$(curl -s -m 2 -o /dev/null -w '%{http_code}' "http://127.0.0.1:$p/v1/health")" = 200 ] && {
      docker rm -f "$CFGNAME" >/dev/null 2>&1; echo healthy; return; }
    sleep 1
  done
  docker logs "$CFGNAME" 2>&1 | grep -i 'cannot unmarshal\|parse config' | head -1
  docker rm -f "$CFGNAME" >/dev/null 2>&1
  echo dead
}
cfg_row=""
for v in 30s 5m 24h 30d; do
  mk_cfg "$WORK/cfg.yml" "$v"
  r=$(boots "$WORK/cfg.yml" | tail -1)
  printf '     dispatch_defaults.sync_rpc_deadline: %-6s → %s\n' "$v" "$r"
  case "$r" in healthy) cfg_row="$cfg_row yes" ;; *) cfg_row="$cfg_row  no" ;; esac
done
if printf '%s' "$exp_row" | grep -q 'yes' && [ "${cfg_row##* }" = "no" ]; then
  bad "\`30d\` is a legal key lifetime on --expires and an unbootable config value"
fi

echo
if [ $fail -eq 0 ]; then echo "RESULT: PASS"; else echo "RESULT: FAIL"; fi
exit $fail
