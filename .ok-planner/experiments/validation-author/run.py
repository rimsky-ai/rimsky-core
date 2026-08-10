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

TAG = os.environ.get("RIMSKY_IMAGE_TAG", "latest")
VALIDATOR_IMAGE = "rimsky-executor-verifier-shape-checks:" + TAG
CLI = str(pathlib.Path(__file__).resolve().parents[3] / "bin" / "rimsky")
PEER_NAME = "author-validator"


def start_validator():
    if docker("image", "inspect", VALIDATOR_IMAGE).returncode != 0:
        die("image %s is not present locally; build it with: make service-images" % VALIDATOR_IMAGE)
    port = free_port()
    name = "rimsky-exp-" + uuid.uuid4().hex[:8]
    res = docker("run", "-d", "--name", name, "-p", "127.0.0.1:%d:9095" % port, VALIDATOR_IMAGE)
    if res.returncode != 0:
        die("docker run failed: " + res.stderr.strip())
    STATE["validator"] = name
    while True:
        probe = socket.socket()
        try:
            probe.connect(("127.0.0.1", port))
            probe.close()
            return port
        except OSError:
            probe.close()
            time.sleep(0.2)


def boot_with_validator(validator_port, protocols=("executor", "validation")):
    config = pathlib.Path(os.environ.get("TMPDIR", "/tmp")) / ("exp-author-%s.yml" % uuid.uuid4().hex[:8])
    config.write_text(
        "persistence:\n"
        "  driver: sqlite\n"
        "  sqlite:\n"
        "    path: /var/lib/rimsky/state.db\n"
        "claim_producers: {}\n"
        "named_locks: {}\n"
        "executors:\n"
        "  %s:\n"
        "    transport: grpc\n"
        "    endpoint: grpc://host.docker.internal:%d\n"
        "    protocols:\n%s"
        % (PEER_NAME, validator_port, "".join("      - %s\n" % p for p in protocols)))
    STATE["config"] = config
    port = free_port()
    name = "rimsky-exp-" + uuid.uuid4().hex[:8]
    res = docker("run", "-d", "--name", name,
                 "-p", "127.0.0.1:%d:8080" % port,
                 "-v", "%s:/etc/rimsky/rimsky.yml:ro" % config,
                 IMAGE)
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


def teardown_all():
    teardown()
    if STATE.get("validator"):
        docker("rm", "-f", STATE["validator"])
        STATE["validator"] = None
    if STATE.get("config"):
        try:
            STATE["config"].unlink()
        except OSError:
            pass
        STATE["config"] = None


def node(name, checks):
    node = {"type": "verifier", "executor": PEER_NAME}
    if checks is not None:
        node["attributes"] = {"schema": {"type": "object", "properties": {
            "checks": {"type": "array", "default": checks}}}}
    return {"name": name, "version": "1", "nodes": [node]}


def findings(body, key):
    return (body or {}).get(key) or []


def has_class(entries, want):
    return any(e.get("class") == want for e in entries)


def main():
    if not os.path.exists(CLI):
        die("the rimsky CLI is not built at %s; build it with: make cli" % CLI)
    validator_port = start_validator()

    print("  leg 1: the conformance kit against the validator's Validate RPC")
    res = subprocess.run([CLI, "conformance", "validation",
                          "--endpoint", "grpc://127.0.0.1:%d" % validator_port,
                          "--role", "executor"], capture_output=True, text=True)
    check("rimsky conformance validation passes against the service's validation RPC",
          res.returncode == 0, (res.stdout + res.stderr)[:300])
    check("the conformance run exercised the unsupported-role case too",
          "UnknownRole" in res.stdout, res.stdout[:300])

    boot_with_validator(validator_port)

    print("  leg 2: a template the validator rejects")
    status, body = call("POST", "/v1/templates", {"spec": node("exp-author-missing-checks", None)})
    errors = findings(body, "validation_errors")
    check("the validator's error blocks the registration", status == 400, json.dumps(body)[:200])
    check("the blocked registration names the validator's finding class",
          has_class(errors, "missing_checks"), json.dumps(errors)[:300])
    check("the finding is reported against the executor role context the validator was given",
          any((e.get("path") or "").startswith("/executor") for e in errors), json.dumps(errors)[:300])

    print("  leg 3: a template the validator only warns about")
    status, body = call("POST", "/v1/templates",
                        {"spec": node("exp-author-unknown-kind", [{"kind": "no_such_check", "config": {}}])})
    warnings = findings(body, "validation_warnings")
    check("the validator's warning does not block the registration", status in (200, 201),
          json.dumps(body)[:200])
    check("the registration response carries the validator's warning class",
          has_class(warnings, "unknown_check_kind"), json.dumps(warnings)[:300])

    print("  leg 4: a template the validator accepts outright")
    status, body = call("POST", "/v1/templates",
                        {"spec": node("exp-author-clean", [{"kind": "row_count_absolute", "config": {"min": 0}}])})
    check("a template the validator accepts registers with no findings",
          status in (200, 201) and not findings(body, "validation_errors")
          and not findings(body, "validation_warnings"), json.dumps(body)[:300])

    print("  leg 5: the same service wired without the validation protocol")
    teardown()
    boot_with_validator(validator_port, protocols=("executor",))
    status, body = call("POST", "/v1/templates", {"spec": node("exp-author-missing-checks", None)})
    check("with the mix-in not advertised in the peer's protocols, the same template is not rejected — "
          "the finding came from the service's validator",
          status in (200, 201), json.dumps(body)[:200])

    teardown_all()
    failed = [c for c in STATE["checks"] if not c[1]]
    print("")
    print("%d checks, %d failed" % (len(STATE["checks"]), len(failed)))
    sys.exit(1 if failed else 0)


try:
    main()
finally:
    teardown_all()
