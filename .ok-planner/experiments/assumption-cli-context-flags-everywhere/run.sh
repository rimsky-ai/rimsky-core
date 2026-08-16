#!/bin/bash
# Experiment: assumption cli-context-flags-everywhere.
#
# Two questions:
#   1. Do --control-api and --api-key, the flag names the prior uses, exist on
#      the CLI's verbs? The parser settles it over a sample from every verb
#      family, with --endpoint and --key measured beside them.
#   2. Where the flags parse, are they honoured? A flag that is accepted and
#      ignored is worse than one that is rejected, so the run boots a
#      rimsky-all-in-one, mints an admin key, and points one verb per family
#      at it explicitly, with no context configured and no environment
#      fallback in play.
#
# Requires: docker, python3, a rimsky CLI binary (RIMSKY_BIN), RIMSKY_IMAGE_TAG.
set -u

RIMSKY_BIN=${RIMSKY_BIN:?set RIMSKY_BIN to the rimsky CLI binary}
TAG=${RIMSKY_IMAGE_TAG:?set RIMSKY_IMAGE_TAG}
free_port() { python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()'; }
PORT=${PORT:-$(free_port)}
NAME=exp-assumption-cli-context-flags
BASE="http://127.0.0.1:$PORT"

WORK=$(mktemp -d); export HOME="$WORK/home"; mkdir -p "$HOME"
unset RIMSKY_API_KEY RIMSKY_CONTEXT
cleanup() { docker rm -f "$NAME" >/dev/null 2>&1; }
trap cleanup EXIT

fail=0
pass() { echo "PASS  $1"; }
bad()  { echo "FAIL  $1"; fail=1; }

# One verb per family. The compose family stops parsing at its first
# positional, so `compose run` is probed with the flag ahead of the manifest.
VERBS=(
  "health" "ls templates" "template list" "tag list" "instance list" "node get n"
  "parked list" "messages show m" "asset list --instance i" "lineage prune"
  "logs i" "watch i" "admin reset n" "register f.yml" "run f.yml"
  "auth list" "auth status" "ctx list" "ctx current" "agent status" "agent start"
  "compose status" "compose plan" "conformance executor" "conformance publisher"
)
tail_arg() { case "$1" in "compose run") printf 'm.yml' ;; *) printf '' ;; esac; }

echo "== stage 1: which spellings does each verb take? =="
export RIMSKY_CONTROL_API_URL=http://127.0.0.1:1
n_endpoint=0; n_key=0; n_control_api=0; n_api_key=0
for v in "${VERBS[@]}" "compose run"; do
  read -r -a words <<<"$v"
  extra=$(tail_arg "$v")
  row=""
  for f in --endpoint --key --control-api --api-key; do
    case "$f" in --endpoint|--control-api) val=http://127.0.0.1:1 ;; *) val=K ;; esac
    if [ -n "$extra" ]; then
      out=$("$RIMSKY_BIN" "${words[@]}" "$f" "$val" "$extra" 2>&1 | head -1)
    else
      out=$("$RIMSKY_BIN" "${words[@]}" "$f" "$val" 2>&1 | head -1)
    fi
    case "$out" in
      *"not defined"*) row="$row $(printf '%-14s' "$f:no")" ;;
      *) row="$row $(printf '%-14s' "$f:yes")"
         case "$f" in
           --endpoint)    n_endpoint=$((n_endpoint+1)) ;;
           --key)         n_key=$((n_key+1)) ;;
           --control-api) n_control_api=$((n_control_api+1)) ;;
           --api-key)     n_api_key=$((n_api_key+1)) ;;
         esac ;;
    esac
  done
  printf '  %-30s%s\n' "$v" "$row"
done
total=$(( ${#VERBS[@]} + 1 ))
echo
echo "  --endpoint    accepted by $n_endpoint of $total verbs"
echo "  --key         accepted by $n_key of $total verbs"
echo "  --control-api accepted by $n_control_api of $total verbs"
echo "  --api-key     accepted by $n_api_key of $total verbs"
if [ "$n_control_api" = "$total" ] && [ "$n_api_key" = "$total" ]; then
  pass "--control-api and --api-key are accepted everywhere"
else
  bad "--control-api reaches $n_control_api of $total verbs and --api-key $n_api_key; the CLI's own names are --endpoint and --key"
fi
unset RIMSKY_CONTROL_API_URL

echo
echo "== stage 2: where the endpoint/key flags parse, are they honoured? =="
docker rm -f "$NAME" >/dev/null 2>&1
docker run -d --name "$NAME" -p "$PORT:8080" "rimsky-all-in-one:$TAG" >/dev/null || exit 1
for _ in $(seq 1 90); do
  [ "$(curl -s -m 2 -o /dev/null -w '%{http_code}' "$BASE/v1/health")" = 200 ] && break; sleep 1
done
[ "$(curl -s -m 2 -o /dev/null -w '%{http_code}' "$BASE/v1/health")" = 200 ] || { echo "FAIL  stack did not come up"; exit 1; }
ADMIN=$("$RIMSKY_BIN" auth init --endpoint "$BASE" | awk 'NF==1 && length($1)>20 {print $1; exit}')
[ -n "$ADMIN" ] || { echo "FAIL  could not mint the admin key"; exit 1; }

cat > "$WORK/t.yml" <<'EOF'
name: context-flags-probe
version: "1"
nodes:
  - type: a
    executor: verifier-shape-checks
EOF
cat > "$WORK/rimsky-compose.yml" <<'EOF'
project: context-flags-probe
templates:
  - path: t.yml
    tag: probe
    state: deployed
EOF

honoured=0; ignored=()
probe_honoured() { # probe_honoured <label> <words...>
  local label=$1; shift
  local out; out=$("$@" 2>&1 | head -1)
  if printf '%s' "$out" | grep -q '401'; then
    ignored+=("$label"); printf '  %-22s the flags parsed and the request went out unauthenticated: %s\n' "$label" "$out"
  else
    honoured=$((honoured+1)); printf '  %-22s authenticated: %s\n' "$label" "${out:-(empty listing)}"
  fi
}
probe_honoured "template list" "$RIMSKY_BIN" template list --endpoint "$BASE" --key "$ADMIN"
probe_honoured "instance list" "$RIMSKY_BIN" instance list --endpoint "$BASE" --key "$ADMIN"
probe_honoured "auth list" "$RIMSKY_BIN" auth list --endpoint "$BASE" --key "$ADMIN"
probe_honoured "parked list" "$RIMSKY_BIN" parked list --endpoint "$BASE" --key "$ADMIN"
( cd "$WORK" && "$RIMSKY_BIN" compose status --endpoint "$BASE" --key "$ADMIN" > "$WORK/co" 2>&1 )
out=$(head -1 "$WORK/co")
if printf '%s' "$out" | grep -q '401'; then
  ignored+=("compose status"); printf '  %-22s the flags parsed and the request went out unauthenticated: %s\n' "compose status" "$out"
else
  honoured=$((honoured+1)); printf '  %-22s authenticated: %s\n' "compose status" "$out"
fi
( cd "$WORK" && RIMSKY_CONTROL_API_URL="$BASE" RIMSKY_API_KEY="$ADMIN" "$RIMSKY_BIN" compose status > "$WORK/ce" 2>&1 )
printf '  %-22s %s\n' "compose status (env)" "$(head -1 "$WORK/ce")"

if [ ${#ignored[@]} -eq 0 ]; then
  pass "every probed verb honoured --endpoint and --key"
else
  bad "${#ignored[@]} verb(s) accept the flags and ignore them: ${ignored[*]}"
fi

echo
if [ $fail -eq 0 ]; then echo "RESULT: PASS"; else echo "RESULT: FAIL"; fi
exit $fail
