"""Probe: is the CLI a thin client with route parity against the control API?

Usage: probe.py <rimsky-bin> <upstream-base> <home-dir>

The probe stands a recording reverse proxy in front of a live control API,
points the CLI at the proxy, and drives every CLI verb that could reach a
route. Each request is recorded and folded onto the route template it matched,
so the run ends holding two sets:

  * the routes some verb reached, versus the control API's declared routes --
    the routes with no verb are the ones the CLI cannot get to;
  * the verbs that reached nothing at all -- the verbs with no route.

Nothing here is inferred from source: a route counts as reachable only when a
CLI verb was observed asking for it.
"""

import json
import os
import re
import subprocess
import sys
import threading
import urllib.error
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

BIN, UPSTREAM, HOMEDIR = sys.argv[1:4]

seen = []          # (method, path) in order
lock = threading.Lock()


class Recorder(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *a):
        pass

    def _forward(self, method):
        body = None
        n = int(self.headers.get("Content-Length") or 0)
        if n:
            body = self.rfile.read(n)
        with lock:
            seen.append((method, self.path.split("?")[0]))
        req = urllib.request.Request(UPSTREAM + self.path, data=body, method=method)
        for k, v in self.headers.items():
            if k.lower() not in ("host", "content-length", "connection"):
                req.add_header(k, v)
        try:
            with urllib.request.urlopen(req, timeout=30) as r:
                payload, status = r.read(), r.status
        except urllib.error.HTTPError as e:
            payload, status = e.read(), e.code
        except Exception as e:  # upstream unreachable
            payload, status = json.dumps({"error": str(e)}).encode(), 502
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(payload)))
        self.end_headers()
        self.wfile.write(payload)

    def do_GET(self):
        self._forward("GET")

    def do_POST(self):
        self._forward("POST")

    def do_PUT(self):
        self._forward("PUT")

    def do_DELETE(self):
        self._forward("DELETE")


server = ThreadingHTTPServer(("127.0.0.1", 0), Recorder)
PROXY = f"http://127.0.0.1:{server.server_address[1]}"
threading.Thread(target=server.serve_forever, daemon=True).start()

ENV = dict(os.environ)
ENV.update(HOME=HOMEDIR, RIMSKY_CONTROL_API_URL=PROXY)
ENV.pop("RIMSKY_CONTEXT", None)
ENV.pop("RIMSKY_API_KEY", None)

# Every route the control API declares, most specific first.
ROUTES = [
    ("DELETE", r"^/v1/auth/keys/[^/]+$", "DELETE /v1/auth/keys/{nameOrID}"),
    ("DELETE", r"^/v1/instances/[^/]+/breakpoints/[^/]+$", "DELETE /v1/instances/{idOrKey}/breakpoints/{breakpoint_id}"),
    ("DELETE", r"^/v1/instances/[^/]+/assets/[^/]+$", "DELETE /v1/instances/{id}/assets/{alias}"),
    ("DELETE", r"^/v1/instances/[^/]+$", "DELETE /v1/instances/{idOrKey}"),
    ("DELETE", r"^/v1/mcp$", "DELETE /v1/mcp"),
    ("DELETE", r"^/v1/tags/[^/]+$", "DELETE /v1/tags/{tag}"),
    ("DELETE", r"^/v1/templates/[^/]+$", "DELETE /v1/templates/{id}"),
    ("GET", r"^/v1/admin/diagnostics/held-frames$", "GET /v1/admin/diagnostics/held-frames"),
    ("GET", r"^/v1/admin/diagnostics/parked-nodes$", "GET /v1/admin/diagnostics/parked-nodes"),
    ("GET", r"^/v1/admin/diagnostics/producer-outbox$", "GET /v1/admin/diagnostics/producer-outbox"),
    ("GET", r"^/v1/admin/diagnostics/wait-sets$", "GET /v1/admin/diagnostics/wait-sets"),
    ("GET", r"^/v1/audit$", "GET /v1/audit"),
    ("GET", r"^/v1/auth/keys$", "GET /v1/auth/keys"),
    ("GET", r"^/v1/auth/keys/[^/]+$", "GET /v1/auth/keys/{nameOrID}"),
    ("GET", r"^/v1/auth/status$", "GET /v1/auth/status"),
    ("GET", r"^/v1/auth/whoami$", "GET /v1/auth/whoami"),
    ("GET", r"^/v1/ca-root$", "GET /v1/ca-root"),
    ("GET", r"^/v1/claim-handles/[^/]+/holders$", "GET /v1/claim-handles/{claim_handle_id}/holders"),
    ("GET", r"^/v1/events$", "GET /v1/events"),
    ("GET", r"^/v1/health$", "GET /v1/health"),
    ("GET", r"^/v1/instances$", "GET /v1/instances"),
    ("GET", r"^/v1/instances/[^/]+/breakpoint-hits$", "GET /v1/instances/{idOrKey}/breakpoint-hits"),
    ("GET", r"^/v1/instances/[^/]+/breakpoints$", "GET /v1/instances/{idOrKey}/breakpoints"),
    ("GET", r"^/v1/instances/[^/]+/nodes$", "GET /v1/instances/{idOrKey}/nodes"),
    ("GET", r"^/v1/instances/[^/]+/assets$", "GET /v1/instances/{id}/assets"),
    ("GET", r"^/v1/instances/[^/]+/assets/[^/]+/materialization-history$", "GET /v1/instances/{id}/assets/{alias}/materialization-history"),
    ("GET", r"^/v1/instances/[^/]+/assets/[^/]+/versions$", "GET /v1/instances/{id}/assets/{alias}/versions"),
    ("GET", r"^/v1/instances/[^/]+/assets/[^/]+$", "GET /v1/instances/{id}/assets/{alias}"),
    ("GET", r"^/v1/instances/[^/]+/frames$", "GET /v1/instances/{id}/frames"),
    ("GET", r"^/v1/instances/[^/]+/frames/[^/]+$", "GET /v1/instances/{id}/frames/{frame_id}"),
    ("GET", r"^/v1/instances/[^/]+/messages$", "GET /v1/instances/{id}/messages"),
    ("GET", r"^/v1/instances/[^/]+$", "GET /v1/instances/{idOrKey}"),
    ("GET", r"^/v1/lineage/by-producer/[^/]+$", "GET /v1/lineage/by-producer/{executor_name}"),
    ("GET", r"^/v1/lineage/by-source/[^/]+/[^/]+$", "GET /v1/lineage/by-source/{source_type}/{source_id}"),
    ("GET", r"^/v1/lineage/claims/[^/]+/ancestors$", "GET /v1/lineage/claims/{claim_handle_id}/ancestors"),
    ("GET", r"^/v1/lineage/claims/[^/]+/descendants$", "GET /v1/lineage/claims/{claim_handle_id}/descendants"),
    ("GET", r"^/v1/lineage/claims/[^/]+$", "GET /v1/lineage/claims/{claim_handle_id}"),
    ("GET", r"^/v1/lineage/runs/[^/]+/ancestors$", "GET /v1/lineage/runs/{run_id}/ancestors"),
    ("GET", r"^/v1/lineage/runs/[^/]+/descendants$", "GET /v1/lineage/runs/{run_id}/descendants"),
    ("GET", r"^/v1/lineage/runs/[^/]+$", "GET /v1/lineage/runs/{run_id}"),
    ("GET", r"^/v1/mcp$", "GET /v1/mcp"),
    ("GET", r"^/v1/messages/[^/]+$", "GET /v1/messages/{id}"),
    ("GET", r"^/v1/nodes/[^/]+$", "GET /v1/nodes/{id}"),
    ("GET", r"^/v1/observability/", "GET /v1/observability/*"),
    ("GET", r"^/v1/runs/[^/]+$", "GET /v1/runs/{run_id}"),
    ("GET", r"^/v1/tags$", "GET /v1/tags"),
    ("GET", r"^/v1/templates$", "GET /v1/templates"),
    ("GET", r"^/v1/templates/[^/]+$", "GET /v1/templates/{id}"),
    ("POST", r"^/v1/admin/lineage/prune$", "POST /v1/admin/lineage/prune"),
    ("POST", r"^/v1/auth/keys/[^/]+/rotate$", "POST /v1/auth/keys/{nameOrID}/rotate"),
    ("POST", r"^/v1/auth/keys$", "POST /v1/auth/keys"),
    ("POST", r"^/v1/enroll$", "POST /v1/enroll"),
    ("POST", r"^/v1/instances/[^/]+/breakpoints/[^/]+/resume$", "POST /v1/instances/{idOrKey}/breakpoints/{breakpoint_id}/resume"),
    ("POST", r"^/v1/instances/[^/]+/breakpoints$", "POST /v1/instances/{idOrKey}/breakpoints"),
    ("POST", r"^/v1/instances/[^/]+/pause$", "POST /v1/instances/{idOrKey}/pause"),
    ("POST", r"^/v1/instances/[^/]+/resume$", "POST /v1/instances/{idOrKey}/resume"),
    ("POST", r"^/v1/instances/[^/]+/terminate$", "POST /v1/instances/{idOrKey}/terminate"),
    ("POST", r"^/v1/instances/[^/]+/debug/override$", "POST /v1/instances/{id}/debug/override"),
    ("POST", r"^/v1/instances/[^/]+/messages$", "POST /v1/instances/{id}/messages"),
    ("POST", r"^/v1/instances$", "POST /v1/instances"),
    ("POST", r"^/v1/mcp$", "POST /v1/mcp"),
    ("POST", r"^/v1/nodes/[^/]+/reset$", "POST /v1/nodes/{id}/reset"),
    ("POST", r"^/v1/tags$", "POST /v1/tags"),
    ("POST", r"^/v1/templates/validate$", "POST /v1/templates/validate"),
    ("POST", r"^/v1/templates/[^/]+/deploy$", "POST /v1/templates/{id}/deploy"),
    ("POST", r"^/v1/templates/[^/]+/undeploy$", "POST /v1/templates/{id}/undeploy"),
    ("POST", r"^/v1/templates$", "POST /v1/templates"),
    ("PUT", r"^/v1/tags/[^/]+$", "PUT /v1/tags/{tag}"),
]


def fold(method, path):
    for m, rx, name in ROUTES:
        if m == method and re.match(rx, path):
            return name
    return f"UNMATCHED {method} {path}"


def run(*args, cwd=None, cut=None):
    """Run a CLI verb; `cut` bounds a verb that streams and never returns."""
    with lock:
        mark = len(seen)
    try:
        p = subprocess.run([BIN, *args], env=ENV, capture_output=True, text=True,
                           cwd=cwd, timeout=cut)
    except subprocess.TimeoutExpired as e:
        p = subprocess.CompletedProcess(args, -1, e.stdout or "", e.stderr or "")
    with lock:
        hit = [fold(m, q) for m, q in seen[mark:]]
    return p, sorted(set(hit))


def jout(p):
    try:
        return json.loads(p.stdout or "null")
    except ValueError:
        return None


tpl = os.path.join(HOMEDIR, "t.yml")
with open(tpl, "w") as fh:
    fh.write('name: route-parity-probe\nversion: "1"\nnodes:\n  - type: verify\n'
             '    executor: verifier-shape-checks\n')

print("== seeding through the proxy ==")
H = jout(run("template", "register", tpl, "-o", "json")[0])["template_id"]
run("template", "deploy", H)
INST = jout(run("instance", "create", H, "-o", "json")[0])["instance_id"]
NODE = jout(run("instance", "nodes", INST, "-o", "json")[0])[0]["id"]
DEAD = jout(run("instance", "create", H, "-o", "json")[0])["instance_id"]
run("instance", "kill", DEAD, "--force")
ADMIN = ""
for line in run("auth", "init")[0].stdout.splitlines():
    if line.strip().startswith("rk_"):
        ADMIN = line.strip()
ENV["RIMSKY_API_KEY"] = ADMIN
run("auth", "create-key", "--name=spare", "--role=read-only")
print(f"  template {H[:18]}…  instance {INST[:8]}  node {NODE[:8]}  key spare")

# `logs` and `watch` stream: they are cut off once they have shown which route
# they poll, since the question here is which routes a verb reaches.
STREAMING = {"logs", "watch", "messages tail"}

VERBS = [
    ("health", ["health"]),
    ("register", ["register", tpl]),
    ("deploy", ["deploy", H]),
    ("instantiate", ["instantiate", H]),
    ("ls templates", ["ls", "templates"]),
    ("ls instances", ["ls", "instances"]),
    ("ls tags", ["ls", "tags"]),
    ("logs", ["logs", DEAD]),
    ("watch", ["watch", INST, "--until", "terminated"]),
    ("template lint", ["template", "lint", tpl]),
    ("template list", ["template", "list"]),
    ("template get", ["template", "get", H]),
    ("tag create", ["tag", "create", "probe-tag", "--template", H]),
    ("tag list", ["tag", "list"]),
    ("tag get", ["tag", "get", "probe-tag"]),
    ("tag mv", ["tag", "mv", "probe-tag", "--template", H]),
    ("tag rm", ["tag", "rm", "probe-tag"]),
    ("instance list", ["instance", "list"]),
    ("instance get", ["instance", "get", INST]),
    ("instance status", ["instance", "status", INST]),
    ("instance nodes", ["instance", "nodes", INST]),
    ("instance events", ["instance", "events", INST]),
    ("instance kill", ["instance", "kill", INST, "--force"]),
    ("instance delete", ["instance", "delete", DEAD]),
    ("rm-instance", ["rm-instance", INST]),
    ("node get", ["node", "get", NODE]),
    ("admin reset", ["admin", "reset", NODE]),
    ("parked list", ["parked", "list"]),
    ("messages tail", ["messages", "tail", "--instance", INST]),
    ("messages show", ["messages", "show", "00000000-0000-0000-0000-000000000001"]),
    ("asset list", ["asset", "list", "--instance", INST]),
    ("asset show", ["asset", "show", "--instance", INST, "verify.a"]),
    ("asset versions", ["asset", "versions", "--instance", INST, "verify.a"]),
    ("asset delete", ["asset", "delete", "--instance", INST, "verify.a"]),
    ("asset lineage", ["asset", "lineage", "--instance", INST, "verify.a"]),
    ("lineage prune", ["lineage", "prune", "--older-than", "1s"]),
    ("auth create-key", ["auth", "create-key", "--name=probe-key", "--role=read-only"]),
    ("auth list", ["auth", "list"]),
    ("auth show", ["auth", "show", "spare"]),
    ("auth status", ["auth", "status"]),
    ("auth rotate", ["auth", "rotate", "spare"]),
    ("auth revoke", ["auth", "revoke", "spare"]),
    ("undeploy", ["undeploy", H]),
    ("template undeploy", ["template", "undeploy", H]),
    ("template rm", ["template", "rm", H]),
    ("ctx list", ["ctx", "list"]),
    ("ctx add", ["ctx", "add", "probe", "--endpoint", PROXY]),
    ("ctx use", ["ctx", "use", "probe"]),
    ("ctx current", ["ctx", "current"]),
    ("ctx rm", ["ctx", "rm", "probe"]),
    ("agent status", ["agent", "status"]),
    ("agent stop", ["agent", "stop"]),
    ("version", ["version"]),
    ("compose status", ["compose", "status"]),
    ("compose plan", ["compose", "plan"]),
]
# The compose verbs need a manifest in the working directory before they will
# dial anything, so they are driven from a directory that has one.
with open(os.path.join(HOMEDIR, "rimsky-compose.yml"), "w") as fh:
    fh.write("project: route-parity\ntemplates:\n  - path: t.yml\n    tag: probe\n    state: deployed\n")
COMPOSE_CWD = {"compose status", "compose plan"}

print()
print("== driving every CLI verb through the proxy ==")
reached, silent = {}, []
for label, argv in VERBS:
    _, hits = run(*argv, cut=15 if label in STREAMING else None,
                  cwd=HOMEDIR if label in COMPOSE_CWD else None)
    if hits:
        for h in hits:
            reached.setdefault(h, []).append(label)
    else:
        silent.append(label)
    print(f"  {label:20s} {', '.join(hits) if hits else '(reached no route)'}")

print()
print("== names an operator would reach for that are not verbs ==")
for guess in (["instance", "pause"], ["instance", "resume"], ["audit"], ["whoami"],
              ["breakpoint", "list"], ["observability", "get"], ["run", "get"]):
    p2, _ = run(*guess)
    line = (p2.stdout or p2.stderr).splitlines()[0] if (p2.stdout or p2.stderr) else ""
    print(f"  rimsky {' '.join(guess):20s} → {line[:60]}")

declared = [name for _, _, name in ROUTES]
unreached = [r for r in declared if r not in reached]
unmatched = [r for r in reached if r.startswith("UNMATCHED")]

print()
print(f"== {len(declared)} declared control-API routes; {len(reached) - len(unmatched)} were reached ==")
print(f"{len(unreached)} routes no CLI verb reached:")
for r in unreached:
    print(f"     {r}")
print()
print(f"{len(silent)} CLI verbs reached no route at all:")
print("     " + ", ".join(silent))
if unmatched:
    print()
    print("requests that matched no declared route:")
    for r in unmatched:
        print(f"     {r}")

print()
if not unreached and not silent:
    print("RESULT: PASS")
    sys.exit(0)
print("RESULT: FAIL")
sys.exit(1)
