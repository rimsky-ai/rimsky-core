SLUG = "assumption-enroll-route-always-mounted"

import base64
import json
import os
import socket
import ssl
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
import uuid

TAG = os.environ.get("RIMSKY_IMAGE_TAG") or sys.exit("export RIMSKY_IMAGE_TAG=src-<tree hash> first")
IMAGE = "rimsky-all-in-one:" + TAG
BASENAME = "exp-" + SLUG
HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.abspath(os.path.join(HERE, "..", "..", ".."))
CLI = os.path.join(ROOT, "bin", "rimsky")
HOMEDIR = tempfile.mkdtemp(prefix="rimsky-exp-")
WORK = tempfile.mkdtemp(prefix="rimsky-exp-cfg-")
STATE = {"checks": [], "containers": []}

UNVERIFIED = ssl.create_default_context()
UNVERIFIED.check_hostname = False
UNVERIFIED.verify_mode = ssl.CERT_NONE

BASE_CONFIG = """persistence:
  driver: sqlite
  sqlite:
    path: /var/lib/rimsky/state.db
claim_producers: {}
named_locks: {}
executors: {}
"""


def docker(*args):
    return subprocess.run(["docker", *args], capture_output=True, text=True)


def free_port():
    s = socket.socket()
    s.bind(("127.0.0.1", 0))
    port = s.getsockname()[1]
    s.close()
    return port


def cleanup():
    for name in STATE["containers"]:
        docker("rm", "-f", name)


def die(msg):
    print("HARNESS ERROR: " + msg)
    cleanup()
    sys.exit(2)


def call(base, method, path, body=None, token=None):
    hdrs = {"Content-Type": "application/json", "Idempotency-Key": uuid.uuid4().hex}
    if token:
        hdrs["Authorization"] = "Bearer " + token
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(base + path, data=data, headers=hdrs, method=method)
    ctx = UNVERIFIED if base.startswith("https") else None
    try:
        with urllib.request.urlopen(req, context=ctx) as resp:
            return resp.status, resp.read().decode()
    except urllib.error.HTTPError as exc:
        return exc.code, exc.read().decode()


def check(label, ok, detail=""):
    STATE["checks"].append((label, bool(ok)))
    print(("PASS  " if ok else "FAIL  ") + label + ((" | " + detail) if detail else ""))


def run_stack(suffix, config_text, env=None, scheme="http"):
    name = BASENAME + "-" + suffix
    docker("rm", "-f", name)
    port = free_port()
    path = os.path.join(WORK, "rimsky-%s.yml" % suffix)
    with open(path, "w") as fh:
        fh.write(config_text)
    args = ["run", "-d", "--name", name, "-p", "127.0.0.1:%d:8080" % port,
            "-v", "%s:/etc/rimsky/rimsky.yml:ro" % path]
    for k, v in (env or {}).items():
        args += ["-e", "%s=%s" % (k, v)]
    args.append(IMAGE)
    if docker(*args).returncode != 0:
        die("docker run failed for " + name)
    STATE["containers"].append(name)
    base = "%s://127.0.0.1:%d" % (scheme, port)
    while True:
        try:
            if call(base, "GET", "/v1/health")[0] == 200:
                return name, base
        except Exception:
            pass
        if docker("inspect", "-f", "{{.State.Running}}", name).stdout.strip() != "true":
            die("container %s exited during boot:\n%s%s" % (name, docker("logs", name).stdout,
                                                            docker("logs", name).stderr))
        time.sleep(0.3)


def cli(base, *args):
    env = dict(os.environ, HOME=HOMEDIR)
    env.pop("RIMSKY_API_KEY", None)
    env.pop("RIMSKY_CONTROL_API_URL", None)
    return subprocess.run([CLI, *args, "--endpoint", base], capture_output=True, text=True, env=env)


def plaintext_of(out):
    for line in out.splitlines():
        if "RIMSKY_API_KEY" in line and "for subsequent" in line:
            return line.split("RIMSKY_API_KEY=")[1].split(" ")[0].strip('"')
    die("could not read a key plaintext out of:\n" + out)


def finish():
    cleanup()
    failed = [c for c in STATE["checks"] if not c[1]]
    print("")
    print("%d checks, %d failed" % (len(STATE["checks"]), len(failed)))
    print("EXPERIMENT PASS" if not failed else "EXPERIMENT FAIL")
    sys.exit(1 if failed else 0)


def main():
    if docker("image", "inspect", IMAGE).returncode != 0:
        die("image %s is not present locally; build it with: make core-images service-images test-images" % IMAGE)

    print("== PRIOR CONTRADICTED: with no peer_auth, neither route is mounted ==")
    name, base = run_stack("default", BASE_CONFIG)
    admin = plaintext_of(cli(base, "auth", "init").stdout)
    for label, method, path, body in [("POST /v1/enroll ", "POST", "/v1/enroll", {"label": "svc"}),
                                      ("GET  /v1/ca-root", "GET", "/v1/ca-root", None)]:
        status, text = call(base, method, path, body, admin)
        check("%s with an admin key answers 404" % label, status == 404,
              "%s %s" % (status, text.strip()[:40]))
    status, _ = call(base, "POST", "/v1/enroll", {"label": "svc"})
    check("POST /v1/enroll unauthenticated answers 404 too", status == 404, str(status))
    body = call(base, "GET", "/v1/ca-root", None, admin)[1]
    check("the 404 is a route miss, not a handler saying no",
          "404 page not found" in body, body.strip()[:40])
    check("the same stack serves its other routes normally",
          call(base, "GET", "/v1/health", None, admin)[0] == 200)
    status, text = call(base, "POST", "/v1/auth/keys",
                        {"name": "svc", "permissions": [{"action": "service:enroll"}]}, admin)
    check("a key carrying service:enroll still mints on this stack", status == 201, str(status))
    enroll_key = json.loads(text)["plaintext"]
    status, _ = call(base, "POST", "/v1/enroll", {"label": "svc"}, enroll_key)
    check("and even that key gets 404 — the grant exists, the route does not", status == 404, str(status))
    docker("rm", "-f", name)
    STATE["containers"].remove(name)

    print("")
    print("== an explicit peer_auth: none is the same ==")
    name, base = run_stack("none", BASE_CONFIG + "peer_auth: none\n")
    for label, method, path in [("POST /v1/enroll ", "POST", "/v1/enroll"),
                                ("GET  /v1/ca-root", "GET", "/v1/ca-root")]:
        status, _ = call(base, method, path, {} if method == "POST" else None)
        check("%s under an explicit peer_auth: none answers 404" % label, status == 404, str(status))
    docker("rm", "-f", name)
    STATE["containers"].remove(name)

    print("")
    print("== with peer_auth: mtls, both routes appear (and the API turns HTTPS) ==")
    name, base = run_stack("mtls", BASE_CONFIG + "peer_auth: mtls\n",
                           {"RIMSKY_CA_ENCRYPTION_KEY": base64.b64encode(os.urandom(32)).decode()},
                           scheme="https")
    check("the control API now speaks HTTPS, not HTTP", base.startswith("https"), base)
    status, text = call(base, "GET", "/v1/ca-root")
    check("GET /v1/ca-root answers 200 unauthenticated and returns a PEM",
          status == 200 and text.startswith("-----BEGIN CERTIFICATE-----"),
          "%s %s" % (status, text.splitlines()[0] if text else ""))
    status, text = call(base, "POST", "/v1/enroll", {"label": "svc"})
    check("POST /v1/enroll unauthenticated is refused by the handler, not the router",
          status == 403 and "404 page not found" not in text, "%s %s" % (status, text.strip()[:90]))
    status, text = call(base, "POST", "/v1/auth/keys",
                        {"name": "svc", "permissions": [{"action": "service:enroll"}]})
    enroll_key = json.loads(text)["plaintext"]
    status, text = call(base, "POST", "/v1/enroll", {"label": "svc"}, enroll_key)
    payload = json.loads(text) if status == 200 else {}
    check("POST /v1/enroll with a service:enroll key issues a leaf certificate",
          status == 200 and payload.get("cert_pem", "").startswith("-----BEGIN CERTIFICATE-----"),
          "%s %s" % (status, ",".join(sorted(payload.keys())) or text.strip()[:80]))
    docker("rm", "-f", name)
    STATE["containers"].remove(name)

    finish()


main()
