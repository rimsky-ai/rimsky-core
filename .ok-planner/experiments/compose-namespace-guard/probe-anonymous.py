"""Probe: does the compose: reservation hold on an unauthenticated deployment?

Usage: probe-anonymous.py <base-url>

The shipped all-in-one image boots with no API keys, and the compose CLI sends
no credential of any kind, so this is the posture in which compose actually
works. The probe is an ordinary HTTP client -- not the compose CLI -- creating
a compose-prefixed tag and instance key while declaring itself compose by
setting the origin header. The story says such a client is refused at the
server; this run records what actually happens.
"""

import json
import sys
import urllib.error
import urllib.request

BASE = sys.argv[1]
PREFIXED_TAG = "compose:anon-intruder:sneaky@1"
PREFIXED_KEY = "compose:anon-intruder:sneaky"
ORIGIN = {"X-Rimsky-Compose-Origin": "1"}

failures = []


def call(method, path, body=None, headers=None):
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(BASE + path, data=data, method=method)
    if data is not None:
        req.add_header("Content-Type", "application/json")
    for k, v in (headers or {}).items():
        req.add_header(k, v)
    try:
        with urllib.request.urlopen(req, timeout=15) as resp:
            return resp.status, resp.read().decode()
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode()


def must_refuse(label, status, body):
    if status < 400:
        failures.append(label)
        print("FAIL  %s: server accepted it (status %d) %s" % (label, status, body.strip()[:200]))
    else:
        print("PASS  %s: refused with %d %s" % (label, status, body.strip()[:120]))


SPEC = {
    "name": "anon-intruder-template",
    "version": "1",
    "nodes": [{"type": "verify", "executor": "verifier-shape-checks"}],
}

status, body = call("POST", "/v1/templates", {"spec": SPEC})
if status >= 400:
    print("setup failed: register template:", status, body)
    sys.exit(1)
HASH = json.loads(body).get("template_id") or json.loads(body).get("id")
call("POST", "/v1/templates/%s/deploy" % HASH)

print("== unauthenticated deployment, ordinary HTTP client ==")
s, b = call("POST", "/v1/tags", {"tag": PREFIXED_TAG, "template": HASH})
must_refuse("tag create with compose prefix, no origin header", s, b)

s, b = call("POST", "/v1/tags", {"tag": PREFIXED_TAG, "template": HASH}, ORIGIN)
must_refuse("tag create with compose prefix, self-declared origin header", s, b)

# An unauthenticated create must name the agent it routes to; supplying one
# keeps this probe pointed at the namespace guard rather than at that check.
s, b = call("POST", "/v1/instances",
            {"template": HASH, "instance_key": PREFIXED_KEY, "target_agent": "probe-agent"},
            ORIGIN)
must_refuse("instance create with compose-prefixed key, self-declared origin header", s, b)

s, b = call("POST", "/v1/templates", {"spec": SPEC, "tag": "compose:anon-intruder:other@1"}, ORIGIN)
must_refuse("template register with compose-prefixed tag, self-declared origin header", s, b)

print("== what landed ==")
s, b = call("GET", "/v1/tags")
if PREFIXED_TAG in b:
    failures.append("tag landed")
    print("FAIL  a non-compose client's compose-prefixed tag is in the store")
else:
    print("PASS  no compose-prefixed tag in the store")
s, b = call("GET", "/v1/instances")
if PREFIXED_KEY in b:
    failures.append("instance key landed")
    print("FAIL  a non-compose client's compose-prefixed instance key is in the store")
else:
    print("PASS  no compose-prefixed instance key in the store")

print()
print("RESULT: PASS" if not failures else "RESULT: FAIL (%s)" % ", ".join(failures))
sys.exit(1 if failures else 0)
