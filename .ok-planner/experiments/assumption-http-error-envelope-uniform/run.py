import json
import os
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request

SLUG = "assumption-http-error-envelope-uniform"
TAG = os.environ.get("RIMSKY_IMAGE_TAG") or sys.exit("export RIMSKY_IMAGE_TAG=src-<tree hash> first")
IMAGE = "rimsky-all-in-one:" + TAG
NAME = "exp-" + SLUG
HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.abspath(os.path.join(HERE, "..", "..", ".."))
CLI = os.path.join(ROOT, "bin", "rimsky")
STATE = {"base": None, "checks": []}

TPL = {"name": "envelope-probe", "version": "1",
       "nodes": [{"type": "w", "kind": "attribute_passthrough"}]}


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


def call(method, path, body=None, token=None, raw_body=None):
    headers = {"Content-Type": "application/json"}
    if token:
        headers["Authorization"] = "Bearer " + token
    data = raw_body.encode() if raw_body is not None else (
        None if body is None else json.dumps(body).encode())
    req = urllib.request.Request(STATE["base"] + path, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req) as resp:
            return resp.status, resp.read().decode(), resp.headers.get("Content-Type", "")
    except urllib.error.HTTPError as exc:
        return exc.code, exc.read().decode(), exc.headers.get("Content-Type", "")


def shape(method, path, body=None, token=None, raw_body=None):
    status, text, ctype = call(method, path, body, token, raw_body)
    try:
        doc = json.loads(text)
    except ValueError:
        return status, "non-json", text.strip()[:60], ctype
    if not isinstance(doc, dict):
        return status, "non-object", text[:60], ctype
    if "jsonrpc" in doc:
        return status, "jsonrpc", json.dumps(doc.get("error"))[:80], ctype
    err = doc.get("error")
    if isinstance(err, str):
        return status, "error-string", err[:60], ctype
    if isinstance(err, dict):
        return status, "error-object", json.dumps(sorted(err.keys())), ctype
    return status, "no-error-key", json.dumps(sorted(doc.keys()))[:60], ctype


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


def bootstrap_auth():
    env = dict(os.environ, HOME=tempfile.mkdtemp(prefix="rimsky-exp-"))
    env.pop("RIMSKY_API_KEY", None)
    env.pop("RIMSKY_CONTROL_API_URL", None)

    def plaintext(out):
        for line in out.splitlines():
            if "RIMSKY_API_KEY" in line and "for subsequent" in line:
                return line.split("RIMSKY_API_KEY=")[1].split(" ")[0].strip('"')
        die("could not read a key plaintext out of:\n" + out)

    admin = plaintext(subprocess.run(
        [CLI, "auth", "init", "--endpoint", STATE["base"]],
        capture_output=True, text=True, env=env).stdout)
    reader = plaintext(subprocess.run(
        [CLI, "auth", "create-key", "--endpoint", STATE["base"], "--key", admin,
         "--name", "reader", "--role", "read-only"],
        capture_output=True, text=True, env=env).stdout)
    return admin, reader


def finish():
    docker("rm", "-f", NAME)
    failed = [c for c in STATE["checks"] if not c[1]]
    print("")
    print("%d checks, %d failed" % (len(STATE["checks"]), len(failed)))
    print("EXPERIMENT PASS" if not failed else "EXPERIMENT FAIL")
    sys.exit(1 if failed else 0)


def main():
    boot()
    unknown = "11111111-1111-1111-1111-111111111111"

    print("== the core CRUD families: one flat envelope, one human-readable string ==")
    for method, path, body in [
        ("GET", "/v1/instances/" + unknown, None),
        ("GET", "/v1/templates/nosuch", None),
        ("GET", "/v1/nodes/" + unknown, None),
        ("GET", "/v1/runs/" + unknown, None),
        ("GET", "/v1/messages/" + unknown, None),
        ("DELETE", "/v1/tags/nosuch", None),
        ("DELETE", "/v1/templates/nosuch", None),
    ]:
        status, kind, detail, _ = shape(method, path, body)
        check("404 %-6s %-42s carries {\"error\": <string>}" % (method, path),
              status == 404 and kind == "error-string", detail)

    status, kind, detail, _ = shape("POST", "/v1/templates", raw_body='{"bogus":1}')
    check("400 unknown JSON field carries {\"error\": <string>}",
          status == 400 and kind == "error-string", detail)
    status, kind, detail, _ = shape("POST", "/v1/templates", raw_body="not json")
    check("400 malformed JSON carries {\"error\": <string>}",
          status == 400 and kind == "error-string", detail)

    admin, reader = bootstrap_auth()

    status, text, _ = call("POST", "/v1/templates", {"spec": TPL}, admin)
    template_id = json.loads(text)["template_id"]
    call("POST", "/v1/templates/%s/deploy" % template_id, {}, admin)
    call("POST", "/v1/tags", {"tag": "envelope:v1", "template": template_id}, admin)
    call("POST", "/v1/instances", {"template": template_id, "instance_key": "envelope-a",
                                   "target_agent": "audit-agent"}, admin)

    status, kind, detail, _ = shape("POST", "/v1/tags", {"tag": "envelope:v1", "template": template_id}, admin)
    check("409 duplicate tag carries {\"error\": <string>}",
          status == 409 and kind == "error-string", detail)
    status, text, _ = call("POST", "/v1/templates/%s/undeploy" % template_id, {}, admin)
    check("409 undeploy-with-instances keeps \"error\" a string and adds its own field",
          status == 409 and isinstance(json.loads(text).get("error"), str)
          and "active_count" in json.loads(text), text[:90])

    print("")
    print("== the same envelope's fields are not stable across statuses ==")
    status, text, _ = call("GET", "/v1/instances")
    body = json.loads(text)
    check("401 no-token body is {\"error\":\"unauthorized\"} plus denial_reason",
          status == 401 and body.get("error") == "unauthorized" and body.get("denial_reason") == "no_token",
          text[:90])
    status, text, _ = call("GET", "/v1/instances", token="rk_bogus")
    check("401 invalid-token reports a different denial_reason",
          status == 401 and json.loads(text).get("denial_reason") == "invalid_token", text[:90])
    status, text, _ = call("POST", "/v1/templates", {"spec": TPL}, reader)
    body = json.loads(text)
    check("403 body carries no denial_reason and no code — only the string",
          status == 403 and body == {"error": "permission denied"}, text[:90])

    print("")
    print("== PRIOR CONTRADICTED: the observability family nests error as an object ==")
    for path in ["/v1/observability/instances/" + unknown,
                 "/v1/observability/frames/" + unknown,
                 "/v1/observability/node-runs/" + unknown,
                 "/v1/observability/claim-handles/" + unknown,
                 "/v1/observability/executors/nosuch",
                 "/v1/observability/claim-producers/nosuch",
                 "/v1/observability/templates/nosuch"]:
        status, kind, detail, _ = shape("GET", path, token=admin)
        check("404 %-46s nests {\"error\":{code,message}}" % path,
              status == 404 and kind == "error-object" and detail == '["code", "message"]', detail)

    status, kind, detail, _ = shape("GET", "/v1/observability/events?limit=abc", token=admin)
    check("400 observability nests {\"error\":{code,message}}",
          status == 400 and kind == "error-object", detail)
    status, kind, detail, _ = shape("GET", "/v1/observability/instances?cursor=zzz", token=admin)
    check("500 observability nests {\"error\":{code,message}}",
          status == 500 and kind == "error-object", detail)
    status, kind, detail, _ = shape("GET", "/v1/instances?cursor=zzz", token=admin)
    check("500 core keeps {\"error\": <string>} for the identical failure",
          status == 500 and kind == "error-string", detail)

    print("")
    print("== PRIOR CONTRADICTED: one observability route answers in two envelopes ==")
    status, kind, detail, _ = shape("GET", "/v1/observability/instances/" + unknown)
    check("401 on an observability route uses the FLAT string envelope",
          status == 401 and kind == "error-string", detail)
    status, kind, detail, _ = shape("GET", "/v1/observability/instances/" + unknown, token=admin)
    check("404 on the SAME route uses the NESTED object envelope",
          status == 404 and kind == "error-object", detail)

    print("")
    print("== PRIOR CONTRADICTED: two more envelopes on the same versioned surface ==")
    status, kind, detail, _ = shape("POST", "/v1/mcp",
                                    {"jsonrpc": "2.0", "id": 1, "method": "tools/list"}, admin)
    check("400 POST /v1/mcp answers in a JSON-RPC envelope, not {\"error\":...}",
          kind == "jsonrpc", detail)
    status, kind, detail, ctype = shape("GET", "/v1/no-such-route", token=admin)
    check("404 on an unmatched /v1 path is plain text, not JSON at all",
          status == 404 and kind == "non-json" and "text/plain" in ctype,
          "%s %s" % (ctype, detail))
    status, text, _ = call("PATCH", "/v1/instances", {}, admin)
    check("405 carries an empty body", status == 405 and text == "", repr(text))

    print("")
    print("== PRIOR CONTRADICTED: no machine-readable code on the core envelope ==")
    coded = []
    for method, path, body in [("GET", "/v1/instances/" + unknown, None),
                               ("GET", "/v1/templates/nosuch", None),
                               ("POST", "/v1/tags", {"tag": "envelope:v1", "template": template_id}),
                               ("POST", "/v1/templates", {"spec": {}})]:
        _, text, _ = call(method, path, body, admin)
        doc = json.loads(text)
        if any(k in doc for k in ("code", "error_code", "type")):
            coded.append(path)
    check("no core error body carries code / error_code / type", not coded, ", ".join(coded) or "none found")

    finish()


main()
