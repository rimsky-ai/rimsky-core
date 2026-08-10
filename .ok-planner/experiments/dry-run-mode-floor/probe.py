"""Probe: does a dry-run grant pin the key to attempt-only?

Usage: probe.py <base-url> <admin-key> <attempt-only-key> <execute-key> <mixed-key>

  attempt-only  grant: tag:create pinned to dry_run, plus reads
  execute       grant: tag:create with no mode, plus reads  (control)
  mixed         grant: tag:create pinned to dry_run AND tag:* unpinned
                (the story's proviso: the key holds another grant that
                 authorizes execute-mode on the same action)
"""

import json
import sys
import urllib.error
import urllib.request

BASE, ADMIN, ATTEMPT, EXECUTE, MIXED = sys.argv[1:6]
failures = []


def call(method, path, key, body=None):
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(BASE + path, data=data, method=method)
    req.add_header("Authorization", "Bearer " + key)
    if data is not None:
        req.add_header("Content-Type", "application/json")
    try:
        with urllib.request.urlopen(req, timeout=15) as resp:
            return resp.status, resp.read().decode()
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode()


def tag_exists(tag):
    _, body = call("GET", "/v1/tags", ADMIN)
    return tag in body


def synthetic(body):
    try:
        d = json.loads(body)
    except ValueError:
        return False
    return d.get("dry_run") is True and "would_have_created_tag" in d


SPEC = {"name": "floor-probe", "version": "1",
        "nodes": [{"type": "verify", "executor": "verifier-shape-checks"}]}
status, body = call("POST", "/v1/templates", ADMIN, {"spec": SPEC})
if status >= 400:
    print("setup failed:", status, body)
    sys.exit(1)
HASH = json.loads(body).get("template_id") or json.loads(body).get("id")


def attempt(label, key, tag, query="", want_synthetic=True):
    s, b = call("POST", "/v1/tags" + query, key, {"tag": tag, "template": HASH})
    landed = tag_exists(tag)
    if want_synthetic:
        ok = synthetic(b) and not landed
        verdict = "synthetic envelope, nothing persisted" if ok else \
                  "status %d body %s landed=%s" % (s, b.strip()[:160], landed)
    else:
        ok = s < 400 and landed
        verdict = "real write, tag persisted" if ok else \
                  "status %d body %s landed=%s" % (s, b.strip()[:160], landed)
    print(("PASS  " if ok else "FAIL  ") + label + ": " + verdict)
    if not ok:
        failures.append(label)


print("== attempt-only key: a write with no flag at all ==")
attempt("dry-run-pinned key creating a tag", ATTEMPT, "floor-a@1")

print("== attempt-only key: holder tries to escalate ==")
attempt("dry-run-pinned key with ?dry_run=false", ATTEMPT, "floor-b@1", "?dry_run=false")

print("== control: same grant without the dry-run mode ==")
attempt("execute-mode key creating a tag", EXECUTE, "floor-c@1", want_synthetic=False)

print("== the story's proviso: another grant authorizes execute on the same action ==")
attempt("key holding both a dry-run and an unpinned grant", MIXED, "floor-d@1", want_synthetic=False)

print("== the attempt-only key still reads ==")
s, b = call("GET", "/v1/tags", ATTEMPT)
if s == 200:
    print("PASS  attempt-only key can still read tags")
else:
    print("FAIL  attempt-only key lost its read grant: %d %s" % (s, b[:120]))
    failures.append("attempt-only read")

print()
print("RESULT: PASS" if not failures else "RESULT: FAIL (%s)" % ", ".join(failures))
sys.exit(1 if failures else 0)
