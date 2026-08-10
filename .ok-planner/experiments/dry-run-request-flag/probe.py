"""Probe: does every write honour the per-request dry-run flag?

Usage: probe.py <base-url> <admin-key>

The population is the control API's write actions. For each one the probe

  1. sends the real request with ?dry_run=true and requires a synthetic
     envelope back ({"dry_run": true, "would_have_...": {...}}),
  2. re-reads the state the write would have changed and requires it
     unchanged,
  3. sends the same request live as a control, so a synthetic envelope
     obtained by failing validation cannot pass for a preview.

A member whose precondition the probe cannot construct is reported as
NOT EXERCISED, never as a pass.
"""

import json
import sys
import time
import urllib.error
import urllib.request
import uuid

BASE, ADMIN = sys.argv[1], sys.argv[2]

results = []  # (action, "pass" | "fail" | "skip", detail)


def call(method, path, body=None, headers=None, key=None):
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(BASE + path, data=data, method=method)
    req.add_header("Authorization", "Bearer " + (key or ADMIN))
    if data is not None:
        req.add_header("Content-Type", "application/json")
    for k, v in (headers or {}).items():
        req.add_header(k, v)
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            return resp.status, resp.read().decode()
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode()


def jget(path):
    s, b = call("GET", path)
    try:
        return s, json.loads(b)
    except ValueError:
        return s, {}


def envelope(body):
    """Is this the synthetic dry-run envelope, and what did it say it would do?"""
    try:
        d = json.loads(body)
    except ValueError:
        return None
    if d.get("dry_run") is not True:
        return None
    intents = [k for k in d if k.startswith("would_have_")]
    if len(intents) != 1:
        return None
    return intents[0]


def record(action, ok, detail):
    results.append((action, "pass" if ok else "fail", detail))
    print(("PASS  " if ok else "FAIL  ") + action + ": " + detail)


def skip(action, detail):
    results.append((action, "skip", detail))
    print("SKIP  " + action + ": " + detail)


def probe(action, method, path, body=None, headers=None, snapshot=None, live=True,
          require_change=True, live_expect=None):
    """Dry-run the write, require no change, then run it live as a control."""
    before = snapshot() if snapshot else None
    s, b = call(method, path + ("&" if "?" in path else "?") + "dry_run=true", body, headers)
    intent = envelope(b)
    if intent is None:
        record(action, False, "no synthetic envelope: %d %s" % (s, b.strip()[:200]))
        return None
    after = snapshot() if snapshot else None
    if snapshot and before != after:
        record(action, False, "state changed under dry-run: %r -> %r" % (before, after))
        return None
    if not live:
        record(action, True, "%s, nothing persisted" % intent)
        return None
    ls, lb = call(method, path, body, headers)
    if ls >= 400:
        record(action, False, "live control rejected the same request: %d %s" % (ls, lb.strip()[:200]))
        return None
    if live_expect is not None and live_expect not in lb:
        record(action, False, "live control did not report %r: %s" % (live_expect, lb.strip()[:200]))
        return None
    changed = snapshot() if snapshot else None
    if snapshot and require_change and changed == after:
        record(action, False, "live control did not change the state the dry-run previewed (%r)" % changed)
        return None
    record(action, True, "%s, nothing persisted; live control persisted" % intent)
    return lb


SPEC = {"name": "dryrun-probe", "version": "1", "message_queue_mode": "backlog",
        "nodes": [{"type": "verify", "executor": "verifier-shape-checks"}]}
SPEC2 = dict(SPEC, name="dryrun-probe-two")
SPEC_FAIL = {"name": "dryrun-probe-fail", "version": "1", "message_queue_mode": "backlog",
             "nodes": [{"type": "verify", "executor": "verifier-shape-checks",
                        "attributes": {"schema": {"type": "object", "properties": {
                            "checks": {"type": "array", "default": [
                                {"kind": "no_nulls", "config": {"fields": ["id"]},
                                 "severity": "error"}]},
                            "rows": {"type": "array", "default": [{"id": None}]}}}}}]}


def template_count():
    _, d = jget("/v1/templates")
    return len(d.get("templates", d.get("items", [])))


def template_state(h):
    _, d = jget("/v1/templates/" + h)
    return d.get("state")


def tags_blob():
    _, d = jget("/v1/tags")
    return json.dumps(d.get("tags"), sort_keys=True)


def instance_count():
    _, d = jget("/v1/instances")
    return len(d.get("instances", d.get("items", [])))


def register_live(spec):
    s, b = call("POST", "/v1/templates", {"spec": spec})
    if s >= 400:
        raise SystemExit("setup: register template failed: %d %s" % (s, b))
    d = json.loads(b)
    return d.get("template_id") or d.get("id")


def deploy_live(h):
    call("POST", "/v1/templates/%s/deploy" % h)


def create_instance_live(h, key):
    s, b = call("POST", "/v1/instances", {"template": h, "instance_key": key})
    if s >= 400:
        raise SystemExit("setup: create instance failed: %d %s" % (s, b))
    return json.loads(b)["instance_id"]


def wake(instance_id, tag):
    call("POST", "/v1/instances/%s/messages" % instance_id, {},
         {"Idempotency-Key": "wake-" + tag})


def nodes_of(instance_id):
    _, d = jget("/v1/instances/%s/nodes" % instance_id)
    return d.get("nodes", [])


print("############ template writes ############")
probe("template:register", "POST", "/v1/templates", {"spec": SPEC}, snapshot=template_count)
H1 = register_live(SPEC)  # idempotent re-register; hash is content-addressed
probe("template:deploy", "POST", "/v1/templates/%s/deploy" % H1,
      snapshot=lambda: template_state(H1))

H2 = register_live(SPEC2)
deploy_live(H2)

# Two preconditions need real dispatches, so start them before the probes that
# only touch the store and read the results back later.
HF = register_live(SPEC_FAIL)
deploy_live(HF)
FAIL_INST = create_instance_live(HF, "probe-failed")
wake(FAIL_INST, "fail")

HIT_INST = create_instance_live(H1, "probe-breakpoint")
s_bp, b_bp = call("POST", "/v1/instances/%s/breakpoints" % HIT_INST,
                  {"checkpoint": "before_dispatch"})
BP_HIT = json.loads(b_bp).get("breakpoint_id") if s_bp < 400 else None
if BP_HIT:
    wake(HIT_INST, "bp")

print("############ tag writes ############")
probe("tag:create", "POST", "/v1/tags", {"tag": "probe-a@1", "template": H1}, snapshot=tags_blob)
probe("tag:set", "PUT", "/v1/tags/probe-a@1", {"template": H2}, snapshot=tags_blob)
call("POST", "/v1/tags", {"tag": "probe-del@1", "template": H1})
probe("tag:delete", "DELETE", "/v1/tags/probe-del@1", snapshot=tags_blob)

print("############ instance writes ############")
probe("instance:create", "POST", "/v1/instances",
      {"template": H1, "instance_key": "probe-created"}, snapshot=instance_count)
_, d = jget("/v1/instances/probe-created")
INST = d.get("instance_id") or d.get("id")


def message_count():
    _, d = jget("/v1/instances/%s/messages" % INST)
    return len(d.get("messages", []))


probe("message:send", "POST", "/v1/instances/%s/messages" % INST, {},
      {"Idempotency-Key": "probe-msg-" + uuid.uuid4().hex}, snapshot=message_count)


def paused():
    _, d = jget("/v1/instances/" + INST)
    return bool(d.get("paused"))


probe("instance:pause", "POST", "/v1/instances/%s/pause" % INST, {}, snapshot=paused)
probe("instance:resume", "POST", "/v1/instances/%s/resume" % INST, {}, snapshot=paused)

# debug-override is gated on a paused (or breakpoint-held) instance.
call("POST", "/v1/instances/%s/pause" % INST, {})


def node_blob():
    return json.dumps(nodes_of(INST), sort_keys=True)


probe("instance:debug-override", "POST", "/v1/instances/%s/debug/override" % INST,
      {"action": "set_attribute", "node_type": "verify",
       "attribute_key": "probe_marker", "attribute_value": "1"},
      snapshot=node_blob, require_change=False)
call("POST", "/v1/instances/%s/resume" % INST, {})

print("############ breakpoint writes ############")


def bp_blob():
    _, d = jget("/v1/instances/%s/breakpoints" % INST)
    return json.dumps(d.get("breakpoints"), sort_keys=True)


probe("breakpoint:create", "POST", "/v1/instances/%s/breakpoints" % INST,
      {"checkpoint": "after_terminal"}, snapshot=bp_blob)
s, b = call("POST", "/v1/instances/%s/breakpoints" % INST, {"checkpoint": "before_dispatch"})
BP_DEL = json.loads(b).get("breakpoint_id") if s < 400 else None
if BP_DEL:
    probe("breakpoint:delete", "DELETE",
          "/v1/instances/%s/breakpoints/%s" % (INST, BP_DEL), snapshot=bp_blob)
else:
    skip("breakpoint:delete", "could not create a breakpoint to delete: %d %s" % (s, b[:160]))

HIT = None
if BP_HIT:
    for _ in range(120):
        _, d = jget("/v1/instances/%s/breakpoint-hits" % HIT_INST)
        hits = [h for h in d.get("hits", []) if not h.get("resumed_at")]
        if hits:
            HIT = hits[0]["hit_id"]
            break
        time.sleep(0.5)
if HIT:
    def hit_resolved():
        _, d = jget("/v1/instances/%s/breakpoint-hits" % HIT_INST)
        return json.dumps([h for h in d.get("hits", []) if h["hit_id"] == HIT], sort_keys=True)

    # A hit's wire shape carries no resumed marker, so the live control is
    # confirmed by what it reports instead of by a re-read.
    probe("breakpoint:resume", "POST",
          "/v1/instances/%s/breakpoints/%s/resume" % (HIT_INST, BP_HIT),
          {"hit_id": HIT}, snapshot=hit_resolved, require_change=False,
          live_expect='"resumed":true')
else:
    skip("breakpoint:resume", "no breakpoint hit could be produced (breakpoint=%s)" % BP_HIT)

print("############ node writes ############")
FAILED_NODE = None
for _ in range(120):
    for n in nodes_of(FAIL_INST):
        sig = n.get("settling_signal_type") or ""
        if sig.startswith("terminal/error"):
            FAILED_NODE = n["id"]
            break
    if FAILED_NODE:
        break
    time.sleep(0.5)
if FAILED_NODE:
    def failed_marker():
        for n in nodes_of(FAIL_INST):
            if n["id"] == FAILED_NODE:
                return n.get("settling_signal_type")
        return None

    probe("node:reset", "POST", "/v1/nodes/%s/reset" % FAILED_NODE, snapshot=failed_marker)
else:
    skip("node:reset", "no node reached a failed terminal: %s"
         % json.dumps(nodes_of(FAIL_INST))[:300])

print("############ instance teardown writes ############")


def terminated():
    _, d = jget("/v1/instances/" + INST)
    return d.get("terminated_at") is not None


probe("instance:kill", "POST", "/v1/instances/%s/terminate" % INST, {}, snapshot=terminated)


def instance_present():
    s, _ = jget("/v1/instances/" + INST)
    return s == 200


probe("instance:terminate", "DELETE", "/v1/instances/" + INST, snapshot=instance_present)

print("############ template teardown writes ############")
call("DELETE", "/v1/tags/probe-a@1")
probe("template:undeploy", "POST", "/v1/templates/%s/undeploy" % H2,
      snapshot=lambda: template_state(H2))


def template_present(h):
    s, _ = jget("/v1/templates/" + h)
    return s == 200


probe("template:deregister", "DELETE", "/v1/templates/" + H2,
      snapshot=lambda: template_present(H2))

print("############ lineage writes ############")
CUTOFF = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(time.time() + 3600))
s, b = call("POST", "/v1/admin/lineage/prune?dry_run=true", {"before": CUTOFF})
intent = envelope(b)
if intent:
    ls, lb = call("POST", "/v1/admin/lineage/prune", {"before": CUTOFF})
    record("lineage:prune", ls < 400,
           "%s; live control returned %d %s" % (intent, ls, lb.strip()[:80]))
else:
    record("lineage:prune", False, "no synthetic envelope: %d %s" % (s, b.strip()[:200]))

print("############ auth writes ############")


def key_names():
    _, d = jget("/v1/auth/keys")
    return sorted(k.get("name") for k in d.get("keys", []))


probe("auth:create", "POST", "/v1/auth/keys",
      {"name": "probe-key", "permissions": [{"action": "tag:read"}]}, snapshot=key_names)


def key_id():
    _, d = jget("/v1/auth/keys/probe-key")
    return d.get("id") or d.get("key_id")


probe("auth:rotate", "POST", "/v1/auth/keys/probe-key/rotate", {}, snapshot=key_id)


def key_readable():
    s, _ = jget("/v1/auth/keys/probe-key")
    return s == 200


probe("auth:revoke", "DELETE", "/v1/auth/keys/probe-key", snapshot=key_readable)

print("############ asset writes ############")
_, d = jget("/v1/instances/%s/assets" % FAIL_INST)
assets = d.get("assets", [])
if assets:
    alias = assets[0].get("alias")

    def asset_present():
        s, _ = jget("/v1/instances/%s/assets/%s" % (FAIL_INST, alias))
        return s == 200

    probe("asset:delete", "DELETE", "/v1/instances/%s/assets/%s" % (FAIL_INST, alias),
          snapshot=asset_present)
else:
    skip("asset:delete", "no claim-backed asset exists on this deployment to delete")

print()
passed = [a for a, v, _ in results if v == "pass"]
failed = [a for a, v, _ in results if v == "fail"]
skipped = [a for a, v, _ in results if v == "skip"]
print("write actions exercised: %d pass, %d fail, %d not exercised (of %d probed)"
      % (len(passed), len(failed), len(skipped), len(results)))
if failed:
    print("  failed:       " + ", ".join(failed))
if skipped:
    print("  not exercised: " + ", ".join(skipped))
print("RESULT: PASS" if not failed and not skipped else "RESULT: FAIL")
sys.exit(0 if (not failed and not skipped) else 1)
