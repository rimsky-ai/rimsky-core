#!/usr/bin/env bash
# Experiment: template-lifecycle
# Drives the whole template catalog lifecycle through the public CLI against a
# rimsky-all-in-one container: register, list/get, deploy, instantiate,
# undeploy (new instances refused), remove, gone from the catalog.
set -uo pipefail

TAG="${RIMSKY_IMAGE_TAG:?set RIMSKY_IMAGE_TAG to the image tag built from this tree}"
HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/../../.." && pwd)"
CLI="$ROOT/bin/rimsky"
NAME="rimsky-exp-template-lifecycle"
PORT="${PORT:-18101}"
E="http://127.0.0.1:$PORT"

fails=0
ok()   { echo "PASS  $*"; }
bad()  { echo "FAIL  $*"; fails=$((fails+1)); }
check(){ if [ "$1" = "$2" ]; then ok "$3"; else bad "$3 (want '$1', got '$2')"; fi; }
has()  { case "$2" in *"$1"*) ok "$3";; *) bad "$3 (missing '$1' in: $2)";; esac; }

docker rm -f "$NAME" >/dev/null 2>&1
docker run -d --name "$NAME" -p "127.0.0.1:$PORT:8080" "rimsky-all-in-one:$TAG" >/dev/null
trap 'docker rm -f "$NAME" >/dev/null 2>&1' EXIT
until "$CLI" health --endpoint "$E" >/dev/null 2>&1; do sleep 0.2; done

TPL="$HERE/template.yml"

echo "--- register"
out="$("$CLI" template register "$TPL" --endpoint "$E" -o json 2>&1)"
ID="$(printf '%s' "$out" | sed -n 's/.*"template_id": "\([^"]*\)".*/\1/p')"
case "$ID" in sha256-*) ok "register returns a template id: $ID";; *) bad "register returned no template id: $out";; esac

echo "--- catalog shows it as registered"
lst="$("$CLI" template list --endpoint "$E" -o json 2>&1)"
has "$ID" "$lst" "template list carries the new template"
has '"state": "registered"' "$lst" "a freshly registered template is in state registered"
get="$("$CLI" template get "$ID" --endpoint "$E" -o json 2>&1)"
has "audit-lifecycle" "$get" "template get returns the stored spec"

echo "--- not runnable until deployed"
out="$("$CLI" instance create "$ID" --endpoint "$E" -o json 2>&1)"
has "requires template state 'deployed'" "$out" "instance create is refused before deploy"

echo "--- deploy makes it runnable"
out="$("$CLI" template deploy "$ID" --endpoint "$E" -o json 2>&1)"
has '"state": "deployed"' "$out" "deploy moves the template to deployed"
out="$("$CLI" instance create "$ID" --endpoint "$E" -o json 2>&1)"
IN="$(printf '%s' "$out" | sed -n 's/.*"instance_id": "\([^"]*\)".*/\1/p')"
[ -n "$IN" ] && ok "instance create returns an instance id: $IN" || bad "instance create failed: $out"

echo "--- a template with live instances cannot be retired or removed"
out="$("$CLI" template undeploy "$ID" --endpoint "$E" -o json 2>&1)"
has "template has active instances" "$out" "undeploy is refused while an instance is live"
out="$("$CLI" template rm "$ID" --endpoint "$E" --yes -o json 2>&1)"
has "undeploy first" "$out" "rm is refused while the template is deployed"

echo "--- retire: no new instances"
"$CLI" instance kill "$IN" --force --endpoint "$E" -o json >/dev/null 2>&1
out="$("$CLI" template undeploy "$ID" --endpoint "$E" -o json 2>&1)"
has '"state": "undeployed"' "$out" "undeploy moves the template to undeployed"
out="$("$CLI" instance create "$ID" --endpoint "$E" -o json 2>&1)"
has "requires template state 'deployed'" "$out" "an undeployed template accepts no new instances"

echo "--- remove once nothing is using it"
out="$("$CLI" template rm "$ID" --endpoint "$E" --yes -o json 2>&1)"
has "FOREIGN KEY" "$out" "rm is refused while instance records still reference the template"
"$CLI" instance delete "$IN" --endpoint "$E" --yes >/dev/null 2>&1
out="$("$CLI" template rm "$ID" --endpoint "$E" --yes -o json 2>&1)"
has "removed" "$out" "rm succeeds once nothing references the template"
lst="$("$CLI" template list --endpoint "$E" -o json 2>&1)"
case "$lst" in *"$ID"*) bad "removed template still appears in the catalog";; *) ok "removed template is gone from the catalog";; esac

echo
if [ "$fails" -eq 0 ]; then echo "EXPERIMENT PASS"; else echo "EXPERIMENT FAIL ($fails)"; fi
exit "$fails"
