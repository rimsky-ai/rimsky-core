#!/bin/bash
# Experiment: assumption cli-json-flag-universal.
#
# Two questions, in order:
#   1. Which of the CLI's read verbs accept --json at all? The parser answers
#      this without a server: the endpoint points at a closed port, so
#      "connection refused" means the flag was accepted and "flag provided but
#      not defined" means it was not.
#   2. Where --json IS accepted, does it put parseable JSON on stdout and the
#      human chatter on stderr? That needs a live deployment, so the run boots
#      a rimsky-all-in-one, mints an admin key, and separates the two streams
#      into files. `compose run --json` self-hosts and is driven the same way.
#
# Requires: docker, python3, a rimsky CLI binary (RIMSKY_BIN), RIMSKY_IMAGE_TAG.
set -u

RIMSKY_BIN=${RIMSKY_BIN:?set RIMSKY_BIN to the rimsky CLI binary}
TAG=${RIMSKY_IMAGE_TAG:?set RIMSKY_IMAGE_TAG}
free_port() { python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()'; }
PORT=${PORT:-$(free_port)}
NAME=exp-assumption-cli-json-flag-universal
BASE="http://127.0.0.1:$PORT"

WORK=$(mktemp -d)
export HOME="$WORK/home"; mkdir -p "$HOME"
unset RIMSKY_API_KEY RIMSKY_CONTEXT
cleanup() { docker rm -f "$NAME" >/dev/null 2>&1; }
trap cleanup EXIT

fail=0
pass() { echo "PASS  $1"; }
bad()  { echo "FAIL  $1"; fail=1; }

READ_VERBS=(
  "health" "ls templates" "ls instances" "ls tags"
  "template list" "template get t" "tag list" "tag get t"
  "instance list" "instance get i" "instance status i" "instance nodes i"
  "instance events i" "node get n" "messages tail --instance i" "messages show m"
  "asset list --instance i" "asset show --instance i a" "asset versions --instance i a"
  "asset lineage --instance i a" "parked list" "auth list" "auth show k" "auth status"
  "ctx list" "ctx current" "agent status" "compose status" "compose plan" "logs i"
)

echo "== stage 1: does --json parse? (${#READ_VERBS[@]} read verbs) =="
export RIMSKY_CONTROL_API_URL=http://127.0.0.1:1
accepts=(); rejects=()
for v in "${READ_VERBS[@]}"; do
  read -r -a words <<<"$v"
  out=$("$RIMSKY_BIN" "${words[@]}" --json 2>&1)
  case "$out" in
    *"flag provided but not defined"*) rejects+=("$v"); echo "     rimsky $v --json : REJECTED" ;;
    *)                                 accepts+=("$v"); echo "     rimsky $v --json : accepted" ;;
  esac
done
echo "     ${#accepts[@]} of ${#READ_VERBS[@]} read verbs accept --json"
if [ ${#rejects[@]} -eq 0 ]; then
  pass "--json is accepted by every read verb"
else
  bad "--json is undefined on ${#rejects[@]} of ${#READ_VERBS[@]} read verbs (accepted only by: ${accepts[*]:-none})"
fi
unset RIMSKY_CONTROL_API_URL

echo
echo "== stage 2: stream placement where --json exists =="
docker rm -f "$NAME" >/dev/null 2>&1
docker run -d --name "$NAME" -p "$PORT:8080" "rimsky-all-in-one:$TAG" >/dev/null || exit 1
for _ in $(seq 1 90); do
  [ "$(curl -s -m 2 -o /dev/null -w '%{http_code}' "$BASE/v1/health")" = 200 ] && break; sleep 1
done
[ "$(curl -s -m 2 -o /dev/null -w '%{http_code}' "$BASE/v1/health")" = 200 ] || { echo "FAIL  stack did not come up"; exit 1; }

ADMIN=$("$RIMSKY_BIN" auth init --endpoint "$BASE" | awk 'NF==1 && length($1)>20 {print $1; exit}')
[ -n "$ADMIN" ] || { echo "FAIL  could not mint the admin key"; exit 1; }
export RIMSKY_CONTROL_API_URL="$BASE" RIMSKY_API_KEY="$ADMIN"

# strict_json <file>: exit 0 iff the whole file is one JSON document
strict_json() { python3 -c 'import json,sys;json.load(open(sys.argv[1]))' "$1" >/dev/null 2>&1; }

"$RIMSKY_BIN" auth list --json >"$WORK/o" 2>"$WORK/e"
echo "     rimsky auth list --json : stdout $(wc -c <"$WORK/o") bytes, stderr $(wc -c <"$WORK/e") bytes"
if strict_json "$WORK/o" && [ ! -s "$WORK/e" ]; then
  pass "auth list --json puts a JSON document on stdout and nothing on stderr"
else
  bad "auth list --json stream placement: stdout parseable=$(strict_json "$WORK/o" && echo yes || echo no)"
fi

echo "     -- the same request through the spelling the other verbs use --"
"$RIMSKY_BIN" ls templates -o json >"$WORK/o2" 2>"$WORK/e2"
if strict_json "$WORK/o2" && [ ! -s "$WORK/e2" ]; then
  echo "     rimsky ls templates -o json : JSON on stdout, stderr empty"
else
  echo "     rimsky ls templates -o json : stdout parseable=$(strict_json "$WORK/o2" && echo yes || echo no)"
fi

echo "     -- what a --json pipeline does on a verb that does not define it --"
"$RIMSKY_BIN" ls templates --json >"$WORK/o3" 2>"$WORK/e3"; rc=$?
echo "     rimsky ls templates --json : exit $rc, stdout $(wc -c <"$WORK/o3") bytes, stderr $(wc -c <"$WORK/e3") bytes"
if [ $rc -eq 0 ] && strict_json "$WORK/o3"; then
  pass "ls templates --json | jq would work"
else
  bad "ls templates --json exits $rc with an empty stdout: a --json pipeline breaks here"
fi

echo "     -- compose run --json --"
CR="$WORK/compose"; mkdir -p "$CR"
cat > "$CR/t.yml" <<'EOF'
name: assumption-json-probe
version: "1"
nodes:
  - type: verify
    executor: verifier-shape-checks
EOF
cat > "$CR/rimsky-compose.yml" <<'EOF'
project: exp-assumption-json
templates:
  - path: t.yml
    tag: probe
    state: deployed
instances:
  - template: probe
    name: one
EOF
( cd "$CR" && env -u RIMSKY_CONTROL_API_URL -u RIMSKY_API_KEY \
    "$RIMSKY_BIN" compose run --json --timeout 45s rimsky-compose.yml ) >"$CR/o" 2>"$CR/e"
echo "     rimsky compose run --json : stdout $(wc -c <"$CR/o") bytes, stderr $(wc -c <"$CR/e") bytes"
if [ -s "$CR/o" ]; then
  pass "compose run --json writes its JSON to stdout"
else
  bad "compose run --json writes everything to stderr and leaves stdout empty"
fi

echo
if [ $fail -eq 0 ]; then echo "RESULT: PASS"; else echo "RESULT: FAIL"; fi
exit $fail
