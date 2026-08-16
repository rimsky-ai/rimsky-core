#!/bin/bash
# Experiment: assumption compose-plan-previews-up.
#
# Runs `compose plan` and `compose up` in lockstep over four manifest states
# -- an empty project, a first apply, a re-apply with nothing changed, and a
# removal -- and compares the plan's change list against the lines `up`
# reports applying. Between the plan and the apply the run re-reads the live
# world to confirm the plan mutated nothing.
#
# Requires: docker, python3, a rimsky CLI binary (RIMSKY_BIN), RIMSKY_IMAGE_TAG.
set -u

RIMSKY_BIN=${RIMSKY_BIN:?set RIMSKY_BIN to the rimsky CLI binary}
TAG=${RIMSKY_IMAGE_TAG:?set RIMSKY_IMAGE_TAG}
free_port() { python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()'; }
PORT=${PORT:-$(free_port)}
NAME=exp-assumption-compose-plan-previews-up
BASE="http://127.0.0.1:$PORT"

WORK=$(mktemp -d); export HOME="$WORK/home"; mkdir -p "$HOME"
PROJ="$WORK/proj"; mkdir -p "$PROJ"
unset RIMSKY_API_KEY RIMSKY_CONTEXT
cleanup() { docker rm -f "$NAME" >/dev/null 2>&1; }
trap cleanup EXIT

fail=0
pass() { echo "PASS  $1"; }
bad()  { echo "FAIL  $1"; fail=1; }

docker rm -f "$NAME" >/dev/null 2>&1
docker run -d --name "$NAME" -p "$PORT:8080" "rimsky-all-in-one:$TAG" >/dev/null || exit 1
for _ in $(seq 1 90); do
  [ "$(curl -s -m 2 -o /dev/null -w '%{http_code}' "$BASE/v1/health")" = 200 ] && break; sleep 1
done
[ "$(curl -s -m 2 -o /dev/null -w '%{http_code}' "$BASE/v1/health")" = 200 ] || { echo "FAIL  stack did not come up"; exit 1; }
export RIMSKY_CONTROL_API_URL="$BASE"

printf 'name: plan-previews-probe\nversion: "1"\nnodes:\n  - type: a\n    executor: verifier-shape-checks\n' > "$PROJ/t.yml"

world() { # a stable fingerprint of everything compose could touch
  {
    "$RIMSKY_BIN" template list -o json
    "$RIMSKY_BIN" tag list -o json
    "$RIMSKY_BIN" instance list -o json
  } 2>&1 | python3 -c 'import sys;print(sys.stdin.read())'
}

# plan_ops / up_ops: the operation and its object from each line, canonicalised
# so that the two renderings' cosmetic differences -- a truncated hash in one
# and the full hash in the other, `tag-delete` in one and `tag-rm` in the
# other -- do not read as a difference in what was planned versus applied.
canon() {
  sed -E 's/(sha256-[0-9a-f]{12})[0-9a-f]*…?/\1/' \
    | sed -E 's/^tag-rm /tag-delete /; s/^tag /tag-create /; s/^instance-delete /instance-delete /' \
    | sed -E 's/^create /instance-create /'
}
plan_ops() { grep -E '^[[:space:]]+[+-] ' | sed -E 's/^[[:space:]]*[+-] //' | awk '{print $1, $2}' | canon | sort; }
up_ops()   { grep -E '^[[:space:]]+\S+ .* ok$' | sed -E 's/^[[:space:]]*//; s/ ok$//' | awk '{print $1, $2}' | canon | sort; }

round() { # round <label>
  local label=$1 p u before after
  before=$(world)
  p=$( cd "$PROJ" && "$RIMSKY_BIN" compose plan 2>&1 )
  after=$(world)
  if [ "$before" != "$after" ]; then
    bad "$label — compose plan changed the world"
  fi
  u=$( cd "$PROJ" && "$RIMSKY_BIN" compose up --yes 2>&1 )
  local po uo
  po=$(printf '%s\n' "$p" | plan_ops)
  uo=$(printf '%s\n' "$u" | up_ops)
  echo "  -- $label"
  echo "     plan:    $(printf '%s' "$po" | tr '\n' ';')"
  echo "     applied: $(printf '%s' "$uo" | tr '\n' ';')"
  if [ "$po" = "$uo" ]; then
    pass "$label — the plan is exactly what up applied"
  else
    bad "$label — plan and apply differ"
    diff <(printf '%s\n' "$po") <(printf '%s\n' "$uo") | sed 's/^/        /'
  fi
}

echo "== round 1: first apply =="
cat > "$PROJ/rimsky-compose.yml" <<'EOF'
project: plan-previews
templates:
  - path: t.yml
    tag: probe
    state: deployed
instances:
  - template: probe
    name: one
EOF
round "first apply"

echo
echo "== round 2: nothing changed =="
round "no-op re-apply"

echo
echo "== round 3: remove the instance entry (after it goes terminal) =="
for id in $("$RIMSKY_BIN" instance list -o json | python3 -c 'import json,sys;print(" ".join(i["id"] for i in (json.load(sys.stdin) or []) if not i.get("terminated_at")))'); do
  "$RIMSKY_BIN" instance kill "$id" --force >/dev/null 2>&1
done
cat > "$PROJ/rimsky-compose.yml" <<'EOF'
project: plan-previews
templates:
  - path: t.yml
    tag: probe
    state: deployed
EOF
round "instance removed from manifest"

echo
echo "== round 4: remove the template entry =="
printf 'project: plan-previews\n' > "$PROJ/rimsky-compose.yml"
round "template removed from manifest"

echo
if [ $fail -eq 0 ]; then echo "RESULT: PASS"; else echo "RESULT: FAIL"; fi
exit $fail
