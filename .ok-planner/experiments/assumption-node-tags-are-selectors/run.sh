#!/bin/bash
# Experiment: assumption node-tags-are-selectors.
#
# Registers a template whose two nodes carry different `nodes[].tags`, creates
# an instance, then asks three questions of one live deployment: does any
# surface select nodes by those tags; do the CLI's --tag and --tag-prefix
# flags bind to them; and does the CLI show them at all.
#
# Requires: docker, python3, a rimsky CLI binary (RIMSKY_BIN), RIMSKY_IMAGE_TAG.
set -u

RIMSKY_BIN=${RIMSKY_BIN:?set RIMSKY_BIN to the rimsky CLI binary}
TAG=${RIMSKY_IMAGE_TAG:?set RIMSKY_IMAGE_TAG}
free_port() { python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()'; }
PORT=${PORT:-$(free_port)}
NAME=exp-assumption-node-tags-are-selectors
BASE="http://127.0.0.1:$PORT"

WORK=$(mktemp -d); export HOME="$WORK/home"; mkdir -p "$HOME"
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

cat > "$WORK/t.yml" <<'EOF'
name: node-tags-probe
version: "1"
nodes:
  - type: alpha
    executor: verifier-shape-checks
    tags: [team-a, critical]
  - type: beta
    executor: verifier-shape-checks
    tags: [team-b]
EOF
H=$("$RIMSKY_BIN" template register "$WORK/t.yml" -o json | python3 -c 'import json,sys;print(json.load(sys.stdin)["template_id"])')
"$RIMSKY_BIN" template deploy "$H" >/dev/null
I=$("$RIMSKY_BIN" instance create "$H" -o json | python3 -c 'import json,sys;print(json.load(sys.stdin)["instance_id"])')
echo "instance $I  nodes alpha[team-a,critical] beta[team-b]"

echo
echo "== does any surface select by node tag? =="
sel=$(curl -s "$BASE/v1/instances/$I/nodes?tag=team-a" | python3 -c '
import json,sys
d=json.load(sys.stdin); rows = d if isinstance(d,list) else d.get("nodes",[])
print(",".join(sorted(n.get("node_type","") for n in rows)))')
echo "     GET /v1/instances/{id}/nodes?tag=team-a → nodes: ${sel:-none}"
if [ "$sel" = "alpha" ]; then
  pass "the HTTP route filters nodes by their tags"
else
  bad "the HTTP route did not filter by node tag (got: ${sel:-none})"
fi

echo
echo "== do --tag and --tag-prefix bind to node tags? =="
for probe in "instance nodes $I --tag team-a" "instance nodes $I --tag-prefix team" \
             "instance list --tag team-a" "node get $I --tag team-a"; do
  read -r -a words <<<"$probe"
  out=$("$RIMSKY_BIN" "${words[@]}" 2>&1 | head -1)
  printf '     rimsky %-44s → %s\n' "$probe" "$(printf '%s' "$out" | cut -c1-46)"
done
tp=$("$RIMSKY_BIN" ls templates --tag-prefix team 2>&1 | head -3 | tr '\n' ' ')
echo "     rimsky ls templates --tag-prefix team        → $tp"
if "$RIMSKY_BIN" instance nodes "$I" --tag team-a >/dev/null 2>&1; then
  pass "--tag filters nodes"
else
  bad "--tag is undefined on every node-listing verb; where it exists it names a template tag"
fi

echo
echo "== does the CLI show node tags at all? =="
shown=$("$RIMSKY_BIN" instance nodes "$I" -o json | python3 -c '
import json,sys
print(json.dumps([(n.get("node_type"), n.get("tags")) for n in json.load(sys.stdin)]))')
echo "     rimsky instance nodes -o json → $shown"
if printf '%s' "$shown" | grep -q 'team-a'; then
  pass "the CLI shows node tags"
else
  bad "the CLI's node listing drops the tags entirely"
fi

echo
if [ $fail -eq 0 ]; then echo "RESULT: PASS"; else echo "RESULT: FAIL"; fi
exit $fail
