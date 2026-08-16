SLUG = "assumption-http-version-prefix-negotiable"

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

def main():
    boot()
    admin = bootstrap_admin()

    print("== exactly one version prefix is mounted, and a wrong one is a typo ==")
    status, text, headers = raw("GET", "/v1/health")
    check("GET /v1/health answers 200", status == 200, str(status))
    for path in ["/v2/health", "/v0/health", "/health", "/api/v1/health", "/v2/instances"]:
        status, text, headers = raw("GET", path, token=admin)
        check("%-18s answers the same plain-text 404 a typo gets" % path,
              status == 404 and text.strip() == "404 page not found"
              and "text/plain" in headers.get("Content-Type", ""),
              "%s %r" % (status, text.strip()[:30]))
    for path in ["/", "/v1", "/version", "/v1/version", "/v1/versions", "/.well-known/versions"]:
        status, text, headers = raw("GET", path, token=admin)
        check("%-22s offers no version discovery" % path, status == 404, str(status))

    print("")
    print("== PRIOR CONTRADICTED: nothing on the wire names a version ==")
    status, text, headers = raw("GET", "/v1/health", token=admin)
    version_headers = [k for k in headers if "version" in k.lower() or k.lower() == "api-version"]
    check("no response header names an API version", not version_headers, str(sorted(headers.keys())))
    body = json.loads(text)
    check("GET /v1/health reports no version field",
          not any("version" in k.lower() for k in body), str(sorted(body.keys())))
    status, body = call("GET", "/v1/auth/status", token=admin)
    check("GET /v1/auth/status reports no version field",
          not any("version" in k.lower() for k in body), str(sorted(body.keys())))

    print("")
    print("== PRIOR CONTRADICTED: the one place the server names a version, it names the prefix ==")
    status, body = call("POST", "/v1/mcp", {"jsonrpc": "2.0", "id": 1, "method": "initialize",
                                            "params": {"protocolVersion": "2025-06-18",
                                                       "capabilities": {},
                                                       "clientInfo": {"name": "probe", "version": "1"}}},
                        admin)
    server_info = body["result"]["serverInfo"]
    check("MCP initialize reports serverInfo.version as the literal \"v1\"",
          server_info == {"name": "rimsky-control-api", "version": "v1"}, json.dumps(server_info))
    client_version = subprocess.run([CLI, "version"], capture_output=True, text=True,
                                    env=cli_env()).stdout.strip()
    check("the CLI reports its own build and never asks the server",
          client_version.startswith("rimsky v") and client_version != "rimsky v1", client_version)

    print("")
    print("== the MCP skin has a protocolVersion field and still does not negotiate ==")
    answers = {}
    for asked in ["2024-11-05", "2025-06-18", "1999-01-01"]:
        _, body = call("POST", "/v1/mcp", {"jsonrpc": "2.0", "id": 1, "method": "initialize",
                                           "params": {"protocolVersion": asked, "capabilities": {},
                                                      "clientInfo": {"name": "probe", "version": "1"}}},
                       admin)
        answers[asked] = body["result"]["protocolVersion"]
    check("every requested protocolVersion is answered with the same fixed value",
          len(set(answers.values())) == 1, json.dumps(answers))

    print("")
    print("== PRIOR CONTRADICTED: the client does not negotiate, it concatenates ==")
    out = subprocess.run([CLI, "ls", "instances", "--endpoint", STATE["base"] + "/v2",
                          "--key", admin], capture_output=True, text=True, env=cli_env())
    combined = out.stdout + out.stderr
    check("--endpoint <base>/v2 requests <base>/v2/v1/instances — /v1 is a literal suffix",
          "/v2/v1/instances" in combined, combined.strip()[:110])

    print("")
    print("== what a run at one tree cannot observe ==")
    check("no /v2 exists at this tree, so coexistence with a future /v2 is unobservable",
          raw("GET", "/v2/instances", token=admin)[0] == 404, "the prefix is the only version fact on the wire")

    finish()


main()
