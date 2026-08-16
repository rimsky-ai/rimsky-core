#!/bin/bash
# Experiment: assumption cli-ls-aliases-match-grouped-verbs.
#
# Runs both spellings of every dev-loop shortcut against one live deployment
# and diffs what came back. The population is the five pairs the prior names:
#
#   ls templates  ~ template list
#   deploy        ~ template deploy
#   undeploy      ~ template undeploy
#   instantiate   ~ instance create
#   rm-instance   ~ instance delete
#
# Read pairs are run twice over the same world and diffed byte for byte, in
# both output formats. Write pairs cannot be: the first spelling consumes the
# subject. So the run builds two interchangeable subjects (two templates whose
# only difference is their name, two instances of the same template), drives
# one through each spelling, and diffs the outputs with hashes and ids
# normalized away. Each pair's flag set is diffed from the verb's own -h.
#
# ls instances / ls tags are measured alongside as context, not as population.
#
# Requires: docker, python3, a rimsky CLI binary (RIMSKY_BIN), RIMSKY_IMAGE_TAG.
set -u

RIMSKY_BIN=${RIMSKY_BIN:?set RIMSKY_BIN to the rimsky CLI binary}
TAG=${RIMSKY_IMAGE_TAG:?set RIMSKY_IMAGE_TAG}
free_port() { python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()'; }
PORT=${PORT:-$(free_port)}
NAME=exp-assumption-cli-ls-aliases
BASE="http://127.0.0.1:$PORT"

WORK=$(mktemp -d)
export HOME="$WORK/home"; mkdir -p "$HOME"
unset RIMSKY_API_KEY RIMSKY_CONTEXT
cleanup() { docker rm -f "$NAME" >/dev/null 2>&1; }
trap cleanup EXIT

fail=0
pass() { echo "PASS  $1"; }
bad()  { echo "FAIL  $1"; fail=1; }

norm() { # collapse the identifiers two runs cannot share
  sed -E -e 's/sha256-[0-9a-f]+/SHA/g' -e 's/sha256-[0-9a-f]+…/SHA/g' \
         -e 's/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/ID/g' \
         -e 's/[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9:.]+Z/TS/g'
}

# cmp_pair <label> <normalize:yes|no> -- <shortcut words> -- <grouped words>
cmp_pair() {
  local label=$1 normalize=$2; shift 3
  local short=() grouped=() cur=short
  for a in "$@"; do
    if [ "$a" = "--" ]; then cur=grouped; continue; fi
    if [ $cur = short ]; then short+=("$a"); else grouped+=("$a"); fi
  done
  local so go src grc
  so=$("$RIMSKY_BIN" "${short[@]}" 2>&1); src=$?
  go=$("$RIMSKY_BIN" "${grouped[@]}" 2>&1); grc=$?
  if [ "$normalize" = yes ]; then
    so=$(printf '%s\n' "$so" | norm); go=$(printf '%s\n' "$go" | norm)
  fi
  if [ "$so" = "$go" ] && [ $src -eq $grc ]; then
    pass "$label — identical output and exit $src"
  else
    bad "$label — differ (exit $src vs $grc)"
    diff <(printf '%s\n' "$so") <(printf '%s\n' "$go") | sed 's/^/        /'
  fi
}

cmp_flags() { # cmp_flags <label> <shortcut> <grouped>
  local label=$1; shift
  local a b
  a=$("$RIMSKY_BIN" $1 -h 2>&1 | grep -E '^  -' | sort)
  b=$("$RIMSKY_BIN" $2 -h 2>&1 | grep -E '^  -' | sort)
  if [ "$a" = "$b" ] && [ -n "$a" ]; then
    pass "$label — identical flag set"
  else
    bad "$label — flag sets differ"
    diff <(printf '%s\n' "$a") <(printf '%s\n' "$b") | sed 's/^/        /'
  fi
}

docker rm -f "$NAME" >/dev/null 2>&1
docker run -d --name "$NAME" -p "$PORT:8080" "rimsky-all-in-one:$TAG" >/dev/null || exit 1
for _ in $(seq 1 90); do
  [ "$(curl -s -m 2 -o /dev/null -w '%{http_code}' "$BASE/v1/health")" = 200 ] && break; sleep 1
done
[ "$(curl -s -m 2 -o /dev/null -w '%{http_code}' "$BASE/v1/health")" = 200 ] || { echo "FAIL  stack did not come up"; exit 1; }
export RIMSKY_CONTROL_API_URL="$BASE"

cat > "$WORK/a.yml" <<'EOF'
name: alias-probe-a
version: "1"
nodes:
  - type: verify
    executor: verifier-shape-checks
EOF
sed 's/alias-probe-a/alias-probe-b/' "$WORK/a.yml" > "$WORK/b.yml"
hash_of() { "$RIMSKY_BIN" template register "$1" -o json | python3 -c 'import json,sys;print(json.load(sys.stdin)["template_id"])'; }
A=$(hash_of "$WORK/a.yml"); B=$(hash_of "$WORK/b.yml")
[ -n "$A" ] && [ -n "$B" ] && [ "$A" != "$B" ] || { echo "FAIL  could not seed two templates"; exit 1; }
echo "seeded A=$A"
echo "seeded B=$B"

echo
echo "== flag sets =="
cmp_flags "ls templates ~ template list" "ls templates" "template list"
cmp_flags "deploy ~ template deploy"     "deploy"       "template deploy"
cmp_flags "undeploy ~ template undeploy" "undeploy"     "template undeploy"
cmp_flags "instantiate ~ instance create" "instantiate" "instance create"
cmp_flags "rm-instance ~ instance delete" "rm-instance" "instance delete"

echo
echo "== pair 1: ls templates ~ template list =="
cmp_pair "ls templates ~ template list (human)" no -- ls templates -- template list
cmp_pair "ls templates ~ template list (-o json)" no -- ls templates -o json -- template list -o json
cmp_pair "ls templates --state ~ template list --state" no \
  -- ls templates --state registered -- template list --state registered
cmp_pair "ls templates on an unknown flag" no -- ls templates --nope -- template list --nope

echo
echo "== pair 2: deploy ~ template deploy =="
cmp_pair "deploy A ~ template deploy B" yes -- deploy "$A" -- template deploy "$B"
cmp_pair "deploy of a missing ref" yes -- deploy sha256-dead -- template deploy sha256-dead

echo
echo "== pair 3: instantiate ~ instance create =="
cmp_pair "instantiate A ~ instance create A" yes -- instantiate "$A" -- instance create "$A"
cmp_pair "instantiate of an undeployed template" yes \
  -- instantiate sha256-dead -- instance create sha256-dead

echo
echo "== pair 4: rm-instance ~ instance delete =="
ids=$("$RIMSKY_BIN" instance list -o json | python3 -c 'import json,sys;print(" ".join(i["id"] for i in json.load(sys.stdin)))')
set -- $ids
I1=${1:-}; I2=${2:-}
if [ -z "$I1" ] || [ -z "$I2" ]; then
  bad "rm-instance ~ instance delete — could not build two instances to delete"
else
  "$RIMSKY_BIN" instance kill "$I1" --force >/dev/null 2>&1
  "$RIMSKY_BIN" instance kill "$I2" --force >/dev/null 2>&1
  for _ in $(seq 1 60); do
    n=$("$RIMSKY_BIN" instance list -o json | python3 -c 'import json,sys;print(sum(1 for i in json.load(sys.stdin) if i.get("terminated_at")))')
    [ "$n" -ge 2 ] && break; sleep 1
  done
  cmp_pair "rm-instance ~ instance delete" yes -- rm-instance "$I1" --yes -- instance delete "$I2" --yes
  cmp_pair "delete of an unknown instance" yes \
    -- rm-instance 00000000-0000-0000-0000-000000000001 --yes \
    -- instance delete 00000000-0000-0000-0000-000000000002 --yes
fi

echo
echo "== pair 5: undeploy ~ template undeploy =="
cmp_pair "undeploy A ~ template undeploy B" yes -- undeploy "$A" -- template undeploy "$B"
cmp_pair "undeploy of a missing ref" yes -- undeploy sha256-dead -- template undeploy sha256-dead

echo
echo "== context: the other two ls forms =="
cmp_pair "ls instances ~ instance list" no -- ls instances -- instance list
cmp_pair "ls tags ~ tag list" no -- ls tags -- tag list

echo
if [ $fail -eq 0 ]; then echo "RESULT: PASS"; else echo "RESULT: FAIL"; fi
exit $fail
