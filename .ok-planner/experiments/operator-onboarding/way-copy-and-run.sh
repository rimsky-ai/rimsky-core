#!/usr/bin/env bash
# Experiment: operator-onboarding
# A new operator copies the shipped example workflow out of the tree,
# runs one CLI verb against a local stack, and watches the resulting
# instance to completion. Public CLI verbs only, against a
# rimsky-all-in-one container.
set -uo pipefail

TAG="${RIMSKY_IMAGE_TAG:?set RIMSKY_IMAGE_TAG to the image tag built from this tree}"
HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/../../.." && pwd)"
CLI="$ROOT/bin/rimsky"
SHIPPED_TEMPLATE="$ROOT/test/fixtures/demos/onboarding-template.yaml"
SHIPPED_DEMO="$ROOT/test/fixtures/demos/onboarding-demo.sh"
NAME="rimsky-exp-onboarding-$$"

fails=0
ok()   { echo "PASS  $*"; }
bad()  { echo "FAIL  $*"; fails=$((fails+1)); }
has()  { case "$2" in *"$1"*) ok "$3";; *) bad "$3 (missing '$1' in: $2)";; esac; }

[ -x "$CLI" ] || make -C "$ROOT" cli >/dev/null || { echo "HARNESS ERROR: cannot build the CLI"; exit 2; }

echo "--- what the operator has to copy"
[ -f "$SHIPPED_TEMPLATE" ] && ok "the tree ships an example workflow the README's walkthrough names" \
    || bad "no shipped example workflow at the path the README names"
[ -f "$SHIPPED_DEMO" ] && ok "the tree ships the two-verb walkthrough script beside it" \
    || bad "no shipped walkthrough script"
grep -q "onboarding-template.yaml" "$ROOT/README.md" \
    && ok "the README's first-steps walkthrough points a newcomer at that file" \
    || bad "the README does not name the shipped example workflow"

COPY="$(mktemp -d)"
trap 'docker rm -f "$NAME" >/dev/null 2>&1; rm -rf "$COPY"' EXIT
cp "$SHIPPED_TEMPLATE" "$SHIPPED_DEMO" "$COPY/"
ok "the operator copies the example out of the tree into $COPY"

docker run -d --name "$NAME" -p 127.0.0.1:0:8080 "rimsky-all-in-one:$TAG" >/dev/null \
    || { echo "HARNESS ERROR: docker run failed"; exit 2; }
PORT="$(docker port "$NAME" 8080 | head -n1 | sed 's/.*://')"
E="http://127.0.0.1:$PORT"
until "$CLI" health --endpoint "$E" >/dev/null 2>&1; do sleep 0.2; done
ok "a local stack is up at $E"

echo "--- one CLI verb against the copy"
OUT="$("$CLI" run --endpoint "$E" --instance-key "onboarding-$$" "$COPY/onboarding-template.yaml" 2>&1)"
rc=$?
[ "$rc" -eq 0 ] && ok "the single run verb exits 0" || bad "the single run verb exited $rc: $OUT"
has "instance_id=" "$OUT" "the run verb prints the instance id the walkthrough tells the operator to use"
ID="$(printf '%s\n' "$OUT" | sed -n 's/^instance_id=\([0-9a-fA-F-]\{36\}\)[[:space:]]*$/\1/p' | head -n1)"

echo "--- watch it to completion"
W="$("$CLI" watch --endpoint "$E" --poll-interval 250ms "$ID" 2>&1)"
rc=$?
[ "$rc" -eq 0 ] && ok "watch returns 0 once the instance reaches terminal" || bad "watch exited $rc: $W"
ST="$("$CLI" instance status "$ID" --endpoint "$E" -o json 2>&1)"
has '"terminal/success"' "$ST" "the instance the operator ran settled at terminal success"

echo "--- the shipped walkthrough script, run from the copy"
D="$(RIMSKY_BIN="$CLI" RIMSKY_CONTROL_API_URL="$E" bash "$COPY/onboarding-demo.sh" 2>&1)"
rc=$?
[ "$rc" -eq 0 ] && ok "the copied walkthrough script runs end to end and exits 0" \
    || bad "the copied walkthrough script exited $rc: $D"
has "dev-loop walkthrough complete" "$D" "the walkthrough reports the dev loop complete"

echo
if [ "$fails" -eq 0 ]; then echo "EXPERIMENT PASS"; else echo "EXPERIMENT FAIL ($fails)"; fi
exit "$fails"
