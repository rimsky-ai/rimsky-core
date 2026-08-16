#!/bin/bash
# Experiment: assumption cli-output-flag-is-json-superset.
#
# Drives the shipped CLI's own parser to settle three claims:
#   1. -o json is interchangeable with --json (some verb accepts both),
#   2. -o yaml is available,
#   3. -o table is available as a format distinct from human.
#
# The parser answers all three without a server: an undefined flag and an
# unknown format value are both rejected before any request is dialled, and
# the endpoint is pointed at a closed port so a connection refusal is the
# signal that the flag WAS accepted. ctx list needs no server at all, so the
# table-vs-human comparison runs against real bytes.
#
# Requires: a rimsky CLI binary (RIMSKY_BIN, default ./bin/rimsky).
set -u

RIMSKY_BIN=${RIMSKY_BIN:?set RIMSKY_BIN to the rimsky CLI binary}
WORK=$(mktemp -d)
export HOME="$WORK/home"; mkdir -p "$HOME"
unset RIMSKY_API_KEY RIMSKY_CONTEXT
export RIMSKY_CONTROL_API_URL=http://127.0.0.1:1

fail=0
pass() { echo "PASS  $1"; }
bad()  { echo "FAIL  $1"; fail=1; }

# accepted <verb-words...> <flag-words...>: did the parser take the flags?
# "flag provided but not defined" / "unknown output format" => rejected.
accepted() {
  local out
  out=$("$RIMSKY_BIN" "$@" 2>&1)
  case "$out" in
    *"flag provided but not defined"*) return 1 ;;
    *"unknown output format"*)         return 1 ;;
  esac
  return 0
}

READ_VERBS=(
  "health" "ls templates" "ls instances" "ls tags"
  "template list" "template get t" "tag list" "tag get t"
  "instance list" "instance get i" "instance status i" "instance nodes i"
  "instance events i" "node get n" "messages tail --instance i" "messages show m"
  "asset list --instance i" "asset show --instance i a" "asset versions --instance i a"
  "asset lineage --instance i a" "parked list" "auth list" "auth show k" "auth status"
  "ctx list" "ctx current" "agent status" "compose status" "compose plan" "logs i"
)

echo "== claim 1: is any verb willing to take both spellings? =="
both=0; only_o=0; only_json=0; neither=0
for v in "${READ_VERBS[@]}"; do
  read -r -a words <<<"$v"
  o=no; j=no
  accepted "${words[@]}" -o json && o=yes
  accepted "${words[@]}" --json  && j=yes
  echo "     rimsky $v : -o json=$o  --json=$j"
  if [ $o = yes ] && [ $j = yes ]; then both=$((both+1));
  elif [ $o = yes ]; then only_o=$((only_o+1));
  elif [ $j = yes ]; then only_json=$((only_json+1));
  else neither=$((neither+1)); fi
done
echo "     of ${#READ_VERBS[@]} read verbs: both=$both  only -o json=$only_o  only --json=$only_json  neither=$neither"
if [ $both -gt 0 ]; then
  pass "-o json and --json are interchangeable on $both verb(s)"
else
  bad "no verb accepts both spellings: the two flags live on disjoint verb sets"
fi

echo
echo "== claim 2: -o yaml =="
if accepted ls templates -o yaml; then
  pass "-o yaml accepted"
else
  bad "-o yaml rejected: $("$RIMSKY_BIN" ls templates -o yaml 2>&1 | head -1)"
fi

echo
echo "== claim 3: -o table as a format distinct from human =="
"$RIMSKY_BIN" ctx add probe --endpoint http://127.0.0.1:1 >/dev/null 2>&1
human=$("$RIMSKY_BIN" ctx list -o human 2>&1)
table=$("$RIMSKY_BIN" ctx list -o table 2>&1)
jsonf=$("$RIMSKY_BIN" ctx list -o json 2>&1)
if ! accepted ctx list -o table; then
  bad "-o table rejected outright"
elif [ "$table" = "$human" ]; then
  bad "-o table is accepted but returns human byte for byte — it is a synonym, not a format"
  printf '%s\n' "$human" | sed 's/^/        /'
else
  pass "-o table produces its own rendering"
fi
if [ "$jsonf" != "$human" ]; then
  echo "     (-o json does render differently from human, so the format switch itself works)"
fi

echo
if [ $fail -eq 0 ]; then echo "RESULT: PASS"; else echo "RESULT: FAIL"; fi
exit $fail
