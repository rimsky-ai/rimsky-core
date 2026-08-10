#!/usr/bin/env bash
# Experiment: mandatory-instantiation-gate
# Registers and deploys templates whose statically-known attribute config is
# supplied only through defaults.attributes.by_executor — the site registration
# does not compose-check — then creates instances and observes the create-time
# gate. Covers a value constraint (an empty array against minItems: 1), a type
# constraint on a second executor, and a well-formed control that creates
# cleanly. Public CLI plus the public control-api create route against a
# rimsky-all-in-one container.
set -uo pipefail

TAG="${RIMSKY_IMAGE_TAG:?set RIMSKY_IMAGE_TAG to the image tag built from this tree}"
HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/../../.." && pwd)"
CLI="$ROOT/bin/rimsky"
NAME="rimsky-exp-mandatory-instantiation-gate"
PORT="${PORT:-18105}"
E="http://127.0.0.1:$PORT"

fails=0
ok()   { echo "PASS  $*"; }
bad()  { echo "FAIL  $*"; fails=$((fails+1)); }
has()  { case "$2" in *"$1"*) ok "$3";; *) bad "$3 (missing '$1' in: $2)";; esac; }
hasnt(){ case "$2" in *"$1"*) bad "$3 (unexpected '$1' in: $2)";; *) ok "$3";; esac; }

docker rm -f "$NAME" >/dev/null 2>&1
docker run -d --name "$NAME" -p "127.0.0.1:$PORT:8080" "rimsky-all-in-one:$TAG" >/dev/null
trap 'docker rm -f "$NAME" >/dev/null 2>&1' EXIT
until "$CLI" health --endpoint "$E" >/dev/null 2>&1; do sleep 0.2; done

deploy() {
  local f="$1" out id
  out="$("$CLI" template register "$f" --endpoint "$E" -o json 2>&1)"
  id="$(printf '%s' "$out" | sed -n 's/.*"template_id": "\([^"]*\)".*/\1/p')"
  if [ -z "$id" ]; then echo "REGISTER FAILED for $f: $out" >&2; return 1; fi
  "$CLI" template deploy "$id" --endpoint "$E" -o json >/dev/null 2>&1
  printf '%s' "$id"
}
create() {
  curl -sS -XPOST -H 'Content-Type: application/json' \
    -d "{\"template\":\"$1\",\"target_agent\":\"audit-probe\"}" "$E/v1/instances"
}

echo "--- a value constraint the executor schema imposes (checks: minItems 1)"
ID="$(deploy "$HERE/template-empty-checks.yml")" \
  && ok "the template registers and deploys: the offending value lives in defaults, not in a node schema" \
  || bad "the value-constraint template did not register"
r="$(create "$ID")"
has '"error":"template validation failed"' "$r" "instance create is refused"
has 'nodes[shape].attributes' "$r" "the refusal names the offending node"
has "'/checks'" "$r" "the refusal names the offending attribute"
has 'minItems' "$r" "the refusal names the violated value constraint, not merely a shape mismatch"
hasnt '"instance_id"' "$r" "no instance is created"

echo "--- a type constraint on a second referenced service"
ID="$(deploy "$HERE/template-bad-url-type.yml")" \
  && ok "the two-executor template registers and deploys" \
  || bad "the type-constraint template did not register"
r="$(create "$ID")"
has '"error":"template validation failed"' "$r" "instance create is refused"
has 'nodes[fetch].attributes' "$r" "the refusal names the node bound to the second service"
has "'/url'" "$r" "the refusal names the offending attribute of the second service"
has 'expected string' "$r" "the refusal names the violated type constraint"
hasnt '"instance_id"' "$r" "no instance is created"

echo "--- the well-formed control"
ID="$(deploy "$HERE/template-valid.yml")" \
  && ok "the well-formed template registers and deploys" \
  || bad "the well-formed template did not register"
r="$(create "$ID")"
has '"instance_id"' "$r" "config satisfying both services' schemas creates cleanly"
has '"node_count":2' "$r" "the created instance carries both nodes"

echo "--- what the CLI relays"
c="$("$CLI" instance create "$(deploy "$HERE/template-empty-checks.yml")" --endpoint "$E" -o json 2>&1)"
has 'template validation failed' "$c" "the CLI reports the refusal"
echo "NOTE  CLI relay of the refusal detail: $c"

echo
if [ "$fails" -eq 0 ]; then echo "EXPERIMENT PASS"; else echo "EXPERIMENT FAIL ($fails)"; fi
exit "$fails"
