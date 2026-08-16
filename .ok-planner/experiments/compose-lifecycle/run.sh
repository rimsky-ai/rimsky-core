#!/bin/bash
# Experiment: story compose-lifecycle.
#
# Applies a manifest declaring two templates, their tags, and two instances to
# an already-running rimsky, and walks the whole lifecycle through the compose
# verbs only:
#
#   plan    -> what would change, before anything is applied
#   status  -> the declared-vs-actual view, before and after
#   up      -> reconcile: templates registered + deployed, tags created,
#              instances created, all under the compose:<project>: namespace
#   up      -> re-run converges to "no changes" (reconcile, not re-apply)
#   down    -> one command removes instances, deployments, tags, templates
#
# Requires: docker, a rimsky CLI binary (RIMSKY_BIN), RIMSKY_IMAGE_TAG.
set -u

RIMSKY_BIN=${RIMSKY_BIN:?set RIMSKY_BIN to the rimsky CLI binary}
TAG=${RIMSKY_IMAGE_TAG:?set RIMSKY_IMAGE_TAG}
free_port() { python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()'; }
PORT=${PORT:-$(free_port)}
NAME=rimsky-exp-compose-lifecycle
BASE="http://127.0.0.1:$PORT"
PROJECT=lifecycle-demo

HERE=$(cd "$(dirname "$0")" && pwd)
WORK=$(mktemp -d)
export HOME="$WORK/home"; mkdir -p "$HOME"
unset RIMSKY_CONTROL_API_URL RIMSKY_API_KEY RIMSKY_CONTEXT
cp "$HERE"/*.yml "$WORK/"
cd "$WORK" || exit 1

cleanup() { docker rm -f "$NAME" >/dev/null 2>&1; }
trap cleanup EXIT

fail=0
check() { if printf '%s' "$3" | grep -qF -- "$2"; then echo "PASS  $1"; else
  echo "FAIL  $1: expected [$2] in:"; printf '%s\n' "$3" | sed 's/^/        /'; fail=1; fi; }
absent() { if printf '%s' "$3" | grep -qF -- "$2"; then
  echo "FAIL  $1: did not expect [$2] in:"; printf '%s\n' "$3" | sed 's/^/        /'; fail=1
  else echo "PASS  $1"; fi; }

docker rm -f "$NAME" >/dev/null 2>&1
docker run -d --name "$NAME" -p "$PORT:8080" "rimsky-all-in-one:$TAG" >/dev/null || exit 1
for _ in $(seq 1 90); do
  [ "$(curl -s -m 2 -o /dev/null -w '%{http_code}' "$BASE/v1/health")" = 200 ] && break; sleep 1
done
"$RIMSKY_BIN" ctx add stack --endpoint "$BASE" >/dev/null

echo "== plan, before anything is applied =="
out=$("$RIMSKY_BIN" compose plan -f rimsky-compose.yml 2>&1)
printf '%s\n' "$out" | sed 's/^/    /'
check "plan names the register step"      "register"                    "$out"
check "plan names the deploy step"        "deploy"                      "$out"
check "plan names the compose-namespaced tag" "compose:$PROJECT:alpha@1" "$out"
check "plan names the compose-namespaced instance key" "compose:$PROJECT:one" "$out"

echo "== status, before anything is applied =="
out=$("$RIMSKY_BIN" compose status -f rimsky-compose.yml 2>&1)
printf '%s\n' "$out" | sed 's/^/    /'
check "status reports the project" "$PROJECT" "$out"

echo "== nothing exists yet =="
out=$("$RIMSKY_BIN" ls tags -o json 2>&1)
absent "no compose tags before up" "compose:$PROJECT:" "$out"

echo "== up: reconcile =="
out=$("$RIMSKY_BIN" compose up -f rimsky-compose.yml --yes 2>&1)
printf '%s\n' "$out" | sed 's/^/    /'
check "up applied changes" "applied" "$out"

echo "== resources exist, namespaced under the compose prefix =="
tags=$("$RIMSKY_BIN" ls tags -o json 2>&1)
check "tag alpha namespaced" "compose:$PROJECT:alpha@1" "$tags"
check "tag beta namespaced"  "compose:$PROJECT:beta@1"  "$tags"
insts=$("$RIMSKY_BIN" ls instances -o json 2>&1)
check "instance one namespaced" "compose:$PROJECT:one" "$insts"
check "instance two namespaced" "compose:$PROJECT:two" "$insts"
tpls=$("$RIMSKY_BIN" ls templates -o json 2>&1)
check "templates are deployed" "deployed" "$tpls"

echo "== up again: converges, no re-apply =="
out=$("$RIMSKY_BIN" compose up -f rimsky-compose.yml --yes 2>&1)
printf '%s\n' "$out" | sed 's/^/    /'
check "second up is a no-op" "no changes" "$out"

echo "== status, after apply =="
out=$("$RIMSKY_BIN" compose status -f rimsky-compose.yml 2>&1)
printf '%s\n' "$out" | sed 's/^/    /'
check "status shows the alpha tag" "alpha@1" "$out"
check "status shows instance one"  "one"     "$out"

# compose down refuses to abort live instances; drive them terminal first
# through the ordinary instance verb.
for key in one two; do
  "$RIMSKY_BIN" instance kill "compose:$PROJECT:$key" --yes >/dev/null 2>&1
done

echo "== down: one command tears it all down =="
out=$("$RIMSKY_BIN" compose down -f rimsky-compose.yml --yes 2>&1)
printf '%s\n' "$out" | sed 's/^/    /'
check "down completed" "compose down complete" "$out"

tags=$("$RIMSKY_BIN" ls tags -o json 2>&1)
absent "compose tags gone"      "compose:$PROJECT:" "$tags"
insts=$("$RIMSKY_BIN" ls instances -o json 2>&1)
absent "compose instances gone" "compose:$PROJECT:" "$insts"

echo
if [ $fail -eq 0 ]; then echo "RESULT: PASS"; else echo "RESULT: FAIL"; fi
exit $fail
