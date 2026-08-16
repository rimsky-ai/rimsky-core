SLUG = "assumption-http-observability-mirrors-primary"

import json
import os
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid

TAG = os.environ.get("RIMSKY_IMAGE_TAG") or sys.exit("export RIMSKY_IMAGE_TAG=src-<tree hash> first")
IMAGE = "rimsky-all-in-one:" + TAG
NAME = "exp-" + SLUG
HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.abspath(os.path.join(HERE, "..", "..", ".."))
CLI = os.path.join(ROOT, "bin", "rimsky")
STATE = {"base": None, "checks": []}


def docker(*args):
    return subprocess.run(["docker", *args], capture_output=True, text=True)


def free_port():
    s = socket.socket()
    s.bind(("127.0.0.1", 0))
    port = s.getsockname()[1]
    s.close()
    return port


def die(msg):
    print("HARNESS ERROR: " + msg)
    docker("rm", "-f", NAME)
    sys.exit(2)


def raw(method, path, body=None, token=None, headers=None):
    hdrs = {"Content-Type": "application/json", "Idempotency-Key": uuid.uuid4().hex}
    if token:
        hdrs["Authorization"] = "Bearer " + token
    if headers:
        hdrs.update(headers)
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(STATE["base"] + path, data=data, headers=hdrs, method=method)
    try:
        with urllib.request.urlopen(req) as resp:
            return resp.status, resp.read().decode(), dict(resp.headers)
    except urllib.error.HTTPError as exc:
        return exc.code, exc.read().decode(), dict(exc.headers)


def call(method, path, body=None, token=None, headers=None):
    status, text, _ = raw(method, path, body, token, headers)
    try:
        return status, json.loads(text) if text else None
    except ValueError:
        return status, text


def check(label, ok, detail=""):
    STATE["checks"].append((label, bool(ok)))
    print(("PASS  " if ok else "FAIL  ") + label + ((" | " + detail) if detail else ""))


def boot():
    if docker("image", "inspect", IMAGE).returncode != 0:
        die("image %s is not present locally; build it with: make core-images service-images test-images" % IMAGE)
    docker("rm", "-f", NAME)
    port = free_port()
    res = docker("run", "-d", "--name", NAME, "-p", "127.0.0.1:%d:8080" % port, IMAGE)
    if res.returncode != 0:
        die("docker run failed: " + res.stderr.strip())
    STATE["base"] = "http://127.0.0.1:%d" % port
    while True:
        try:
            if call("GET", "/v1/health")[0] == 200:
                return
        except Exception:
            pass
        if docker("inspect", "-f", "{{.State.Running}}", NAME).stdout.strip() != "true":
            die("container exited during boot:\n" + docker("logs", NAME).stdout + docker("logs", NAME).stderr)
        time.sleep(0.3)


def cli_env():
    env = dict(os.environ, HOME=tempfile.mkdtemp(prefix="rimsky-exp-"))
    env.pop("RIMSKY_API_KEY", None)
    env.pop("RIMSKY_CONTROL_API_URL", None)
    return env


def plaintext_of(out):
    for line in out.splitlines():
        if "RIMSKY_API_KEY" in line and "for subsequent" in line:
            return line.split("RIMSKY_API_KEY=")[1].split(" ")[0].strip('"')
    die("could not read a key plaintext out of:\n" + out)


def bootstrap_admin():
    return plaintext_of(subprocess.run([CLI, "auth", "init", "--endpoint", STATE["base"]],
                                       capture_output=True, text=True, env=cli_env()).stdout)


def finish():
    docker("rm", "-f", NAME)
    failed = [c for c in STATE["checks"] if not c[1]]
    print("")
    print("%d checks, %d failed" % (len(STATE["checks"]), len(failed)))
    print("EXPERIMENT PASS" if not failed else "EXPERIMENT FAIL")
    sys.exit(1 if failed else 0)

def item_keys(page, key):
    rows = page.get(key) or []
    return sorted(rows[0].keys()) if rows else None


def main():
    boot()
    admin = bootstrap_admin()
    _, out = call("POST", "/v1/templates", {"spec": {
        "name": "mirror-probe", "version": "1",
        "nodes": [{"type": "w", "kind": "attribute_passthrough"}]}}, admin)
    template_id = out["template_id"]
    call("POST", "/v1/templates/%s/deploy" % template_id, {}, admin)
    _, out = call("POST", "/v1/instances", {"template": template_id, "instance_key": "mirror-a",
                                            "target_agent": "audit-agent"}, admin)
    instance_id = out["instance_id"]

    print("== PRIOR CONTRADICTED: the single-instance read is not the same resource ==")
    _, primary = call("GET", "/v1/instances/" + instance_id, None, admin)
    _, mirror = call("GET", "/v1/observability/instances/" + instance_id, None, admin)
    check("GET /v1/instances/{id} returns the instance object at the top level",
          "id" in primary and primary["id"] == instance_id, str(sorted(primary.keys())))
    check("GET /v1/observability/instances/{id} wraps it and adds a cascade_graph",
          sorted(mirror.keys()) == ["cascade_graph", "instance"], str(sorted(mirror.keys())))
    check("a dashboard reading body[\"id\"] from both gets the id from one and nothing from the other",
          primary.get("id") == instance_id and mirror.get("id") is None,
          "primary id=%s mirror id=%s" % (primary.get("id"), mirror.get("id")))

    print("")
    print("== PRIOR CONTRADICTED: the mirror carries fields the primary route hides ==")
    extra = sorted(set(mirror["instance"]) - set(primary))
    check("the wrapped instance adds attribute_overrides and terminated_at",
          extra == ["attribute_overrides", "terminated_at"], str(extra))
    check("nothing on the primary instance object is missing from the mirror",
          not set(primary) - set(mirror["instance"]), str(sorted(set(primary) - set(mirror["instance"]))))
    status, refused = call("DELETE", "/v1/instances/" + instance_id, None, admin)
    check("DELETE names terminated_at in its 409, and the primary instance route does not return it",
          status == 409 and "terminated_at" in refused["error"] and "terminated_at" not in primary,
          "%s %s" % (status, json.dumps(refused)))

    print("")
    print("== the list routes agree on the envelope and disagree on the row ==")
    _, primary = call("GET", "/v1/instances?limit=1", None, admin)
    _, mirror = call("GET", "/v1/observability/instances?limit=1", None, admin)
    check("both instance lists use {instances, next_cursor}",
          sorted(primary.keys()) == sorted(mirror.keys()) == ["instances", "next_cursor"],
          "%s vs %s" % (sorted(primary.keys()), sorted(mirror.keys())))
    extra = sorted(set(item_keys(mirror, "instances")) - set(item_keys(primary, "instances")))
    check("the mirror's rows carry attribute_overrides and terminated_at, the primary's do not",
          extra == ["attribute_overrides", "terminated_at"], str(extra))
    _, primary = call("GET", "/v1/templates?limit=1", None, admin)
    _, mirror = call("GET", "/v1/observability/templates?limit=1", None, admin)
    extra = sorted(set(item_keys(mirror, "templates")) - set(item_keys(primary, "templates")))
    check("the mirror's template rows carry spec, the primary's do not", extra == ["spec"], str(extra))

    print("")
    print("== one pair really does mirror, which is what makes the rest surprising ==")
    _, primary = call("GET", "/v1/events?limit=1", None, admin)
    _, mirror = call("GET", "/v1/observability/events?limit=1", None, admin)
    check("events: same envelope and the same row keys either way",
          sorted(primary.keys()) == sorted(mirror.keys())
          and item_keys(primary, "events") == item_keys(mirror, "events"),
          str(item_keys(primary, "events")))

    print("")
    print("== PRIOR CONTRADICTED: nodes and frames are not addressed the same way at all ==")
    _, nodes = call("GET", "/v1/instances/%s/nodes?limit=1" % instance_id, None, admin)
    node_id = nodes["nodes"][0]["id"]
    _, primary = call("GET", "/v1/nodes/" + node_id, None, admin)
    status, mirror = call("GET", "/v1/observability/nodes/%s/w" % instance_id, None, admin)
    check("GET /v1/nodes/{node_id} takes a node id; the mirror takes {instance_id}/{node_type}",
          status == 200 and sorted(mirror.keys()) == ["events", "holdings", "latest_attributes",
                                                      "node", "run_summary"],
          "primary=%s mirror=%s" % (sorted(primary.keys()), sorted(mirror.keys())))
    status, mirror = call("GET", "/v1/observability/nodes/" + node_id, None, admin)
    check("the mirror has no by-node-id read at all", status == 404, str(status))
    _, primary = call("GET", "/v1/instances/%s/frames" % instance_id, None, admin)
    _, mirror = call("GET", "/v1/observability/frames?instance_id=%s" % instance_id, None, admin)
    check("frames: the primary is instance-scoped with no cursor, the mirror is a filtered collection",
          sorted(primary.keys()) == ["frames"] and sorted(mirror.keys()) == ["frames", "next_cursor"],
          "%s vs %s" % (sorted(primary.keys()), sorted(mirror.keys())))

    print("")
    print("== PRIOR CONTRADICTED: it is a different permission, not a read-only variant ==")
    narrow = plaintext_of(subprocess.run(
        [CLI, "auth", "create-key", "--endpoint", STATE["base"], "--key", admin,
         "--name", "instance-reader", "--role", "read-only",
         "--remove", "*:read", "--add", "instance:read"],
        capture_output=True, text=True, env=cli_env()).stdout)
    status, _ = call("GET", "/v1/instances", None, narrow)
    check("a key granted only instance:read reads the primary route", status == 200, str(status))
    for path in ["/v1/observability/instances", "/v1/observability/instances/" + instance_id,
                 "/v1/observability/system/health"]:
        status, body = call("GET", path, None, narrow)
        check("%-46s refuses it with 403" % path,
              status == 403 and body == {"error": "permission denied"}, "%s %s" % (status, json.dumps(body)))

    print("")
    print("== and the two halves do not even report failure the same way ==")
    unknown = "11111111-1111-1111-1111-111111111111"
    _, primary = call("GET", "/v1/instances/" + unknown, None, admin)
    _, mirror = call("GET", "/v1/observability/instances/" + unknown, None, admin)
    check("404: the primary sends error as a string, the mirror sends it as an object",
          isinstance(primary["error"], str) and isinstance(mirror["error"], dict),
          "%s vs %s" % (json.dumps(primary), json.dumps(mirror)))

    finish()


main()
