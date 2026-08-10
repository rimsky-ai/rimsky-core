import json
import os
import socket
import subprocess
import sys
import time
import urllib.error
import urllib.request
import uuid

IMAGE = "rimsky-all-in-one:" + os.environ.get("RIMSKY_IMAGE_TAG", "latest")
STATE = {"base": None, "container": None, "checks": []}
SETTLED = ("completed", "failed", "terminated")


def die(msg):
    print("HARNESS ERROR: " + msg)
    teardown()
    sys.exit(2)


def docker(*args):
    return subprocess.run(["docker", *args], capture_output=True, text=True)


def free_port():
    sock = socket.socket()
    sock.bind(("127.0.0.1", 0))
    port = sock.getsockname()[1]
    sock.close()
    return port


def boot():
    if docker("image", "inspect", IMAGE).returncode != 0:
        die("image %s is not present locally; build it with: make core-images service-images test-images" % IMAGE)
    port = free_port()
    name = "rimsky-exp-" + uuid.uuid4().hex[:8]
    res = docker("run", "-d", "--name", name, "-p", "127.0.0.1:%d:8080" % port, IMAGE)
    if res.returncode != 0:
        die("docker run failed: " + res.stderr.strip())
    STATE["container"] = name
    STATE["base"] = "http://127.0.0.1:%d" % port
    while True:
        try:
            if call("GET", "/v1/health")[0] == 200:
                return
        except Exception:
            pass
        time.sleep(0.3)


def teardown():
    if STATE["container"]:
        docker("rm", "-f", STATE["container"])
        STATE["container"] = None


def call(method, path, body=None, headers=None):
    data = None if body is None else json.dumps(body).encode()
    hdrs = {"Content-Type": "application/json"}
    if headers:
        hdrs.update(headers)
    req = urllib.request.Request(STATE["base"] + path, data=data, headers=hdrs, method=method)
    try:
        with urllib.request.urlopen(req) as resp:
            raw = resp.read().decode()
            return resp.status, (json.loads(raw) if raw else None)
    except urllib.error.HTTPError as exc:
        raw = exc.read().decode()
        try:
            return exc.code, json.loads(raw)
        except ValueError:
            return exc.code, raw


def deploy(spec):
    status, out = call("POST", "/v1/templates", {"spec": spec})
    if status not in (200, 201):
        die("template register rejected: %s %s" % (status, out))
    tid = out["template_id"]
    status, out = call("POST", "/v1/templates/%s/deploy" % tid, {})
    if status not in (200, 201):
        die("template deploy rejected: %s %s" % (status, out))
    return tid


def instantiate(tid):
    status, out = call("POST", "/v1/instances", {
        "template": tid,
        "instance_key": "exp-" + uuid.uuid4().hex[:12],
        "target_agent": "audit-agent"})
    if status not in (200, 201):
        die("instance create rejected: %s %s" % (status, out))
    return out["instance_id"]


def quiet(iid):
    while True:
        frames = call("GET", "/v1/instances/%s/frames" % iid)[1]["frames"]
        runs = call("GET", "/v1/observability/node-runs?instance_id=%s" % iid)[1]["node_runs"]
        if frames and all(f["state"] in SETTLED for f in frames) and not runs:
            return
        time.sleep(0.25)


def node_view(iid, node_type):
    return call("GET", "/v1/observability/nodes/%s/%s" % (iid, node_type))[1]


def check(label, ok, detail=""):
    STATE["checks"].append((label, bool(ok)))
    print(("PASS  " if ok else "FAIL  ") + label + ((" | " + detail) if detail else ""))


def finish():
    teardown()
    failed = [c for c in STATE["checks"] if not c[1]]
    print("")
    print("%d checks, %d failed" % (len(STATE["checks"]), len(failed)))
    sys.exit(1 if failed else 0)

import pathlib

CLI = str(pathlib.Path(__file__).resolve().parents[3] / "bin" / "rimsky")

ADVISORY_FRAGMENT = "is not in any declared vocabulary"


def spec_for(name):
    return {
        "name": name,
        "version": "1",
        "nodes": [{
            "type": "verifier",
            "executor": "verifier-shape-checks",
            "error_types": {"totally/made-up": {"action": "retry", "reason": "probe"}},
            "attributes": {"schema": {"type": "object", "properties": {
                "checks": {"type": "array", "default": [
                    {"kind": "row_count_absolute", "config": {"min": 0}}]}}}},
        }],
    }


def carries_advisory(body):
    for entry in (body or {}).get("validation_warnings") or []:
        if ADVISORY_FRAGMENT in (entry.get("msg") or entry.get("message") or ""):
            return True
    return False


def template_count():
    return len(call("GET", "/v1/templates")[1]["templates"])


def cli(*args):
    return subprocess.run([CLI, *args, "--endpoint", STATE["base"]], capture_output=True, text=True)


def write_spec(path, name):
    path.write_text(json.dumps(spec_for(name)))


def main():
    if not os.path.exists(CLI):
        die("the rimsky CLI is not built at %s; build it with: make cli" % CLI)
    boot()

    print("  leg 1: the validation response")
    status, body = call("POST", "/v1/templates/validate", {"spec": spec_for("exp-warn-validate")})
    check("a template tripping only the advisory validates ok", status == 200 and body.get("ok") is True,
          json.dumps(body)[:200])
    check("the validation response carries the advisory the validator computed", carries_advisory(body),
          json.dumps(body)[:300])

    print("  leg 2: the validation response with the promotion flag")
    status, body = call("POST", "/v1/templates/validate?warnings_as_errors=true",
                        {"spec": spec_for("exp-warn-validate")})
    check("the flag flips the validate verdict on an advisory-only template",
          status == 200 and body.get("ok") is False, json.dumps(body)[:200])
    check("the flipped response still names the advisory that flipped it", carries_advisory(body),
          json.dumps(body)[:300])

    print("  leg 3: the registration response")
    status, body = call("POST", "/v1/templates", {"spec": spec_for("exp-warn-register")})
    check("a template tripping only the advisory registers", status in (200, 201), json.dumps(body)[:200])
    check("the registration response carries the advisory", carries_advisory(body), json.dumps(body)[:300])

    print("  leg 4: the registration response with the promotion flag")
    before = template_count()
    status, body = call("POST", "/v1/templates?warnings_as_errors=true",
                        {"spec": spec_for("exp-warn-register-strict")})
    check("the flag turns the advisory into a rejected registration", status == 400, json.dumps(body)[:200])
    check("the rejection echoes the flag", body.get("warnings_as_errors") is True, json.dumps(body)[:200])
    check("the rejection names the advisory that tripped it", carries_advisory(body), json.dumps(body)[:300])
    check("the rejected registration persisted nothing", template_count() == before,
          "%d then %d" % (before, template_count()))

    print("  leg 5: the same two responses through the CLI")
    path = pathlib.Path(os.environ.get("TMPDIR", "/tmp")) / ("exp-warn-%s.json" % uuid.uuid4().hex[:8])
    write_spec(path, "exp-warn-cli")
    res = cli("template", "lint", str(path), "-o", "json")
    check("rimsky template lint prints the advisory", ADVISORY_FRAGMENT in res.stdout, res.stdout[:300])
    strict = cli("template", "lint", str(path), "-o", "json", "--warnings-as-errors")
    check("rimsky template lint --warnings-as-errors reports the template not ok",
          '"ok": false' in strict.stdout, strict.stdout[:200])
    rejected = cli("template", "register", str(path), "-o", "json", "--warnings-as-errors")
    check("rimsky template register --warnings-as-errors refuses and prints the advisory",
          ADVISORY_FRAGMENT in (rejected.stdout + rejected.stderr), (rejected.stdout + rejected.stderr)[:300])
    accepted = cli("template", "register", str(path), "-o", "json")
    print("  observation: rimsky template register (no flag) prints: " +
          json.dumps(accepted.stdout.strip())[:200])
    path.unlink()

    finish()


try:
    main()
finally:
    teardown()
