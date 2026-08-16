#!/bin/bash
# Experiment: assumption cli-short-flags-single-dash.
#
# Drives the shipped CLI's parser over the four claims a getopt-shaped short
# form carries:
#   1. -o is the short form of --output (they bind the same value),
#   2. -f is the short form of --follow / --force wherever those exist,
#   3. short forms cluster and take an attached value (-ojson, -yh),
#   4. -v and -h work per verb the way they work at the top level.
#
# The parser settles every one without a server. The endpoint points at a
# closed port, so "connection refused" means the flag was accepted and
# "flag provided but not defined" means it was not.
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

accepted() {
  local out
  out=$("$RIMSKY_BIN" "$@" 2>&1)
  case "$out" in
    *"flag provided but not defined"*) return 1 ;;
    *"unknown output format"*)         return 1 ;;
  esac
  return 0
}
firstline() { "$RIMSKY_BIN" "$@" 2>&1 | head -1; }

echo "== claim 1: -o is --output =="
"$RIMSKY_BIN" ctx add probe --endpoint http://127.0.0.1:1 >/dev/null 2>&1
a=$("$RIMSKY_BIN" ctx list -o json 2>&1)
b=$("$RIMSKY_BIN" ctx list --output json 2>&1)
if [ "$a" = "$b" ] && [ -n "$a" ]; then
  pass "-o json and --output json render identically"
else
  bad "-o json and --output json differ"
fi

echo
echo "== claim 2: -f is --follow / --force =="
# Every verb in the shipped CLI that defines --follow or --force.
LONG_F=( "instance events i:--follow" "messages tail --instance i:--follow" \
         "logs i:--follow" "instance kill i:--force" )
short_ok=0; total=0
for entry in "${LONG_F[@]}"; do
  verb=${entry%%:*}; long=${entry##*:}
  read -r -a words <<<"$verb"
  total=$((total+1))
  if accepted "${words[@]}" "$long"; then lo=yes; else lo=no; fi
  if accepted "${words[@]}" -f; then sh=yes; short_ok=$((short_ok+1)); else sh=no; fi
  echo "     rimsky $verb : $long=$lo  -f=$sh"
done
if [ $short_ok -eq $total ]; then
  pass "-f is accepted on all $total verbs carrying --follow/--force"
else
  bad "-f is accepted on $short_ok of $total verbs carrying --follow/--force"
fi
# Where -f IS defined, does it mean follow/force?
echo "     rimsky compose plan -f  : $(firstline compose plan -f /nonexistent/manifest.yml)"
if accepted compose plan -f /nonexistent/manifest.yml; then
  echo "     (-f is defined in the compose family, where it names the manifest path)"
fi

echo
echo "== claim 3: clustering and attached values =="
for probe in "-ojson" "-yh" "-hy"; do
  if accepted ls templates $probe; then
    pass "ls templates $probe accepted"
  else
    bad "ls templates $probe rejected: $(firstline ls templates $probe)"
  fi
done
if accepted ls templates -o=json; then
  echo "     (-o=json, the Go flag spelling, is accepted)"
fi

echo
echo "== claim 4: -v and -h per verb =="
top_v=$("$RIMSKY_BIN" -v 2>&1 | head -1)
echo "     rimsky -v : $top_v"
if accepted ls templates -v; then
  pass "-v accepted on a verb"
else
  bad "-v accepted at the top level but not on a verb: $(firstline ls templates -v)"
fi
if "$RIMSKY_BIN" ls templates -h 2>&1 | grep -q "Usage of"; then
  pass "-h prints usage on a verb"
else
  bad "-h does not print usage on a verb"
fi
if accepted ls templates --yes -y; then
  echo "     (-y is a short form of --yes)"
else
  echo "     (--yes has no -y short form: $(firstline ls templates -y))"
fi

echo
if [ $fail -eq 0 ]; then echo "RESULT: PASS"; else echo "RESULT: FAIL"; fi
exit $fail
