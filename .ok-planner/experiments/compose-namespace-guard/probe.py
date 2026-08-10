"""Probe: is the compose: namespace refused at the server for every client surface?

Usage: probe.py <base-url> <admin-key> <operator-key>

The operator key carries a full operational grant but not the compose-origin
capability. Every attempt below tries to CREATE a compose-prefixed tag or
instance key; the run passes when each is refused and nothing lands.
"""

import json
import sys
import urllib.error
import urllib.request

BASE, ADMIN, OPERATOR = sys.argv[1], sys.argv[2], sys.argv[3]
PREFIXED_TAG = "compose:intruder:sneaky@1"
PREFIXED_KEY = "compose:intruder:sneaky"

failures = []


def call(method, path, key, body=None, headers=None):
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(BASE + path, data=data, method=method)
    req.add_header("Authorization", "Bearer " + key)
    if data is not None:
        req.add_header("Content-Type", "application/json")
    for k, v in (headers or {}).items():
        req.add_header(k, v)
    try:
        with urllib.request.urlopen(req, timeout=15) as resp:
            return resp.status, resp.read().decode(), dict(resp.headers)
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode(), dict(e.headers)


def refused(label, status, body):
    if status < 400:
        failures.append(label)
        print("FAIL  %s: server accepted it (status %d) %s" % (label, status, body[:200]))
    else:
        print("PASS  %s: refused with %d %s" % (label, status, body.strip()[:120]))


SPEC = {
    "name": "intruder-template",
    "version": "1",
    "nodes": [{"type": "verify", "executor": "verifier-shape-checks"}],
}

# A template to hang tags and instances off, registered with no reserved name.
status, body, _ = call("POST", "/v1/templates", ADMIN, {"spec": SPEC})
if status >= 400:
    print("setup failed: register template:", status, body)
    sys.exit(1)
HASH = json.loads(body).get("template_id") or json.loads(body).get("id")
call("POST", "/v1/templates/%s/deploy" % HASH, ADMIN)

print("== HTTP surface, operator key (no compose-origin capability) ==")
s, b, _ = call("POST", "/v1/tags", OPERATOR, {"tag": PREFIXED_TAG, "template": HASH})
refused("tag create with compose prefix", s, b)

s, b, _ = call("POST", "/v1/tags", OPERATOR, {"tag": PREFIXED_TAG, "template": HASH},
               {"X-Rimsky-Compose-Origin": "1"})
refused("tag create with spoofed compose-origin header", s, b)

s, b, _ = call("POST", "/v1/templates", OPERATOR, {"spec": SPEC, "tag": PREFIXED_TAG})
refused("template register carrying a compose-prefixed tag", s, b)

s, b, _ = call("POST", "/v1/instances", OPERATOR, {"template": HASH, "instance_key": PREFIXED_KEY})
refused("instance create with compose-prefixed key", s, b)

s, b, _ = call("POST", "/v1/instances", OPERATOR, {"template": HASH, "instance_key": PREFIXED_KEY},
               {"X-Rimsky-Compose-Origin": "1"})
refused("instance create with spoofed compose-origin header", s, b)

print("== HTTP surface, admin key without the compose-origin header ==")
s, b, _ = call("POST", "/v1/tags", ADMIN, {"tag": PREFIXED_TAG, "template": HASH})
refused("admin tag create with compose prefix, no header", s, b)

s, b, _ = call("POST", "/v1/instances", ADMIN, {"template": HASH, "instance_key": PREFIXED_KEY})
refused("admin instance create with compose-prefixed key, no header", s, b)

print("== MCP surface (JSON-RPC over POST /v1/mcp) ==")


def mcp(key, name, arguments, headers=None):
    s, b, h = call("POST", "/v1/mcp", key,
                   {"jsonrpc": "2.0", "id": 1, "method": "initialize",
                    "params": {"protocolVersion": "2025-06-18", "capabilities": {},
                               "clientInfo": {"name": "probe", "version": "0"}}},
                   headers)
    sid = h.get("Mcp-Session-Id") or h.get("mcp-session-id")
    if not sid:
        return s, "initialize returned no session: " + b
    hdrs = dict(headers or {})
    hdrs["Mcp-Session-Id"] = sid
    return call("POST", "/v1/mcp", key,
                {"jsonrpc": "2.0", "id": 2, "method": "tools/call",
                 "params": {"name": name, "arguments": arguments}}, hdrs)[:2]


s, b = mcp(OPERATOR, "tag_create", {"tag": PREFIXED_TAG, "template": HASH})
if "reserved prefix" not in b:
    failures.append("mcp tag_create")
    print("FAIL  mcp tag_create with compose prefix: accepted %d %s" % (s, b[:200]))
else:
    print("PASS  mcp tag_create with compose prefix: refused %d %s" % (s, b.strip()[:160]))

s, b = mcp(OPERATOR, "instance_create", {"template": HASH, "instance_key": PREFIXED_KEY})
if "reserved prefix" not in b:
    failures.append("mcp instance_create")
    print("FAIL  mcp instance_create with compose-prefixed key: accepted %d %s" % (s, b[:200]))
else:
    print("PASS  mcp instance_create with compose-prefixed key: refused %d %s" % (s, b.strip()[:160]))

print("== nothing landed ==")
s, b, _ = call("GET", "/v1/tags", ADMIN)
if PREFIXED_TAG in b:
    failures.append("tag leaked into the store")
    print("FAIL  a compose-prefixed tag exists after the refusals")
else:
    print("PASS  no compose-prefixed tag in the store")
s, b, _ = call("GET", "/v1/instances", ADMIN)
if PREFIXED_KEY in b:
    failures.append("instance key leaked into the store")
    print("FAIL  a compose-prefixed instance key exists after the refusals")
else:
    print("PASS  no compose-prefixed instance key in the store")

print()
print("RESULT: PASS" if not failures else "RESULT: FAIL (%s)" % ", ".join(failures))
sys.exit(1 if failures else 0)
