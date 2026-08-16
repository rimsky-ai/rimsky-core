SLUG = "assumption-http-events-streamable"

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

def cli_event_reads(admin):
    _, page = call("GET", "/v1/audit?limit=1000", None, admin)
    return sum(1 for e in page["audit"]
               if e["kind"].startswith("auth.access")
               and e["payload"].get("action") == "event:read"
               and e["payload"].get("user_agent") == "rimsky")


def main():
    boot()
    admin = bootstrap_admin()
    _, out = call("POST", "/v1/templates", {"spec": {
        "name": "stream-probe", "version": "1",
        "nodes": [{"type": "w", "kind": "attribute_passthrough"}]}}, admin)
    template_id = out["template_id"]
    call("POST", "/v1/templates/%s/deploy" % template_id, {}, admin)
    _, out = call("POST", "/v1/instances", {"template": template_id, "instance_key": "stream-a",
                                            "target_agent": "audit-agent"}, admin)
    instance_id = out["instance_id"]

    print("== PRIOR CONTRADICTED: Accept: text/event-stream gets an ordinary JSON body ==")
    for path in ["/v1/events?limit=2", "/v1/observability/events?limit=2"]:
        status, text, headers = raw("GET", path, token=admin,
                                    headers={"Accept": "text/event-stream"})
        check("%-32s answers application/json, not text/event-stream" % path,
              status == 200 and headers.get("Content-Type") == "application/json",
              "%s %s" % (status, headers.get("Content-Type")))
        check("%-32s sends Content-Length, so the body is complete and the connection closes" % path,
              headers.get("Content-Length") is not None and len(text) == int(headers["Content-Length"]),
              "Content-Length=%s" % headers.get("Content-Length"))

    print("")
    print("== PRIOR CONTRADICTED: a follow parameter is accepted and ignored ==")
    _, plain = call("GET", "/v1/events?limit=2", None, admin)
    for param in ["follow=true", "follow=1", "stream=true", "watch=true", "tail=true"]:
        status, body = call("GET", "/v1/events?limit=2&" + param, None, admin)
        check("?%-12s answers 200 with the same finite envelope" % param,
              status == 200 and sorted(body.keys()) == sorted(plain.keys())
              and len(body["events"]) == len(plain["events"]),
              "%s keys=%s n=%d" % (status, sorted(body.keys()), len(body["events"])))

    print("")
    print("== no route on the surface holds a connection open ==")
    status, body = call("GET", "/v1/instances/%s/breakpoint-hits" % instance_id, None, admin)
    check("GET /v1/instances/{id}/breakpoint-hits returns immediately with next_since",
          status == 200 and body == {"hits": [], "next_since": 0, "truncated": False},
          json.dumps(body))
    status, body = call("GET", "/v1/events?since=0", None, admin)
    check("?since= takes an RFC3339 timestamp, not a resume offset",
          status == 400 and "RFC3339" in json.dumps(body), json.dumps(body))

    print("")
    print("== the CLI's follow really is client-side polling ==")
    for verb, flag in [("watch", "-poll-interval"), ("instance events", "-poll-interval")]:
        args = [CLI] + verb.split() + ["--help"]
        out = subprocess.run(args, capture_output=True, text=True, env=cli_env())
        text = out.stdout + out.stderr
        check("rimsky %-16s advertises %s in its own help" % (verb, flag), flag in text,
              "; ".join(ln.strip() for ln in text.splitlines() if flag in ln)[:90])

    before = cli_event_reads(admin)
    proc = subprocess.Popen([CLI, "logs", instance_id, "--follow", "--poll-interval", "200ms",
                             "--endpoint", STATE["base"], "--key", admin],
                            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, env=cli_env())
    try:
        while cli_event_reads(admin) - before < 5:
            time.sleep(0.2)
    finally:
        proc.terminate()
        proc.wait()
    polls = cli_event_reads(admin) - before
    check("rimsky logs --follow issued repeated GET /v1/events requests, not one open stream",
          polls >= 5, "%d separate event:read requests from one --follow run" % polls)

    finish()


main()
