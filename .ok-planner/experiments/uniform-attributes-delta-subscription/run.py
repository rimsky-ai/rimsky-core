import json
import os
import socket
import subprocess
import sys
import time
import urllib.error
import urllib.request
import uuid

TAG = os.environ.get("RIMSKY_IMAGE_TAG", "latest")
IMAGE = "rimsky-all-in-one:" + TAG
STATE = {"base": None, "containers": [], "networks": [], "checks": []}
SETTLED_FRAME_STATES = ("completed", "failed", "terminated")


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


def require_image(image):
    if docker("image", "inspect", image).returncode != 0:
        die("image %s is not present locally; build it with: make core-images service-images test-images" % image)


def network():
    name = "exp-net-" + uuid.uuid4().hex[:8]
    if docker("network", "create", name).returncode != 0:
        die("docker network create failed")
    STATE["networks"].append(name)
    return name


def run_container(image, args, name=None):
    require_image(image)
    name = name or ("rimsky-exp-" + uuid.uuid4().hex[:8])
    res = docker("run", "-d", "--name", name, *args, image)
    if res.returncode != 0:
        die("docker run %s failed: %s" % (image, res.stderr.strip()))
    STATE["containers"].append(name)
    return name


def boot(volumes=(), env=None, net=None, alias=None, extra=()):
    url = os.environ.get("RIMSKY_CONTROL_API_URL")
    if url and not volumes and not env and not net:
        STATE["base"] = url
        return
    port = free_port()
    args = ["-p", "127.0.0.1:%d:8080" % port]
    for v in volumes:
        args += ["-v", v]
    for key, value in (env or {}).items():
        args += ["-e", "%s=%s" % (key, value)]
    if net:
        args += ["--network", net]
        if alias:
            args += ["--network-alias", alias]
    args += list(extra)
    run_container(IMAGE, args)
    STATE["base"] = "http://127.0.0.1:%d" % port
    while True:
        try:
            if call("GET", "/v1/health")[0] == 200:
                return
        except Exception:
            pass
        time.sleep(0.3)


def teardown():
    for name in STATE["containers"]:
        docker("rm", "-f", name)
    STATE["containers"] = []
    for name in STATE["networks"]:
        docker("network", "rm", name)
    STATE["networks"] = []


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


def register(spec):
    return call("POST", "/v1/templates", {"spec": spec})


def deploy(spec):
    status, out = register(spec)
    if status not in (200, 201):
        die("template register rejected: %s %s" % (status, json.dumps(out)))
    template_id = out["template_id"]
    status, out = call("POST", "/v1/templates/%s/deploy" % template_id, {})
    if status not in (200, 201):
        die("template deploy rejected: %s %s" % (status, out))
    return template_id


def new_instance(template_id, **extra):
    body = {"template": template_id,
            "instance_key": "exp-" + uuid.uuid4().hex[:12],
            "target_agent": "audit-probe-agent"}
    body.update(extra)
    status, out = call("POST", "/v1/instances", body)
    if status not in (200, 201):
        die("instance create rejected: %s %s" % (status, out))
    return out["instance_id"]


def send_message(iid, body=None, key=None):
    return call("POST", "/v1/instances/%s/messages" % iid,
                {} if body is None else body,
                {"Idempotency-Key": key or uuid.uuid4().hex})


def messages(iid):
    return call("GET", "/v1/instances/%s/messages" % iid)[1]["messages"]


def frames(iid):
    return call("GET", "/v1/instances/%s/frames" % iid)[1]["frames"]


def node_types(iid):
    return {n["id"]: n["node_type"] for n in call("GET", "/v1/instances/%s/nodes" % iid)[1]["nodes"]}


def live_runs(iid):
    return call("GET", "/v1/observability/node-runs?instance_id=%s" % iid)[1]["node_runs"]


def timeline(iid):
    types = node_types(iid)
    rows = call("GET", "/v1/observability/events?instance_id=%s&limit=800" % iid)[1]["events"]
    out = []
    for e in sorted(rows, key=lambda r: r["id"]):
        out.append({
            "seq": e["id"],
            "node": types.get(e.get("node_id"), ""),
            "kind": e["kind"],
            "payload": e["payload"] or {},
        })
    return out


def pending(iid):
    return [m for m in messages(iid) if not m.get("delivered_at") and not m.get("cancelled")]


def quiet(iid):
    while True:
        fr = frames(iid)
        if fr and not pending(iid) and all(f["state"] in SETTLED_FRAME_STATES for f in fr) and not live_runs(iid):
            return timeline(iid)
        time.sleep(0.25)


def wait_hits(iid, count):
    while True:
        hits = (call("GET", "/v1/instances/%s/breakpoint-hits" % iid)[1] or {}).get("hits") or []
        if len(hits) >= count:
            return hits
        time.sleep(0.2)


def pause_breakpoint(iid, node_type):
    status, out = call("POST", "/v1/instances/%s/breakpoints" % iid,
                       {"matcher": {"node_type": node_type},
                        "checkpoint": "before_dispatch",
                        "mode": "pause"})
    if status not in (200, 201):
        die("breakpoint create rejected: %s %s" % (status, out))
    return out["breakpoint_id"]


def drain_breakpoint(iid, bpid):
    resumed = set()
    while True:
        hits = (call("GET", "/v1/instances/%s/breakpoint-hits" % iid)[1] or {}).get("hits") or []
        fresh = [h for h in hits if h["hit_id"] not in resumed]
        for h in fresh:
            resumed.add(h["hit_id"])
            call("POST", "/v1/instances/%s/breakpoints/%s/resume" % (iid, bpid), {"hit_id": h["hit_id"]})
        fr = frames(iid)
        if not fresh and not pending(iid) and fr and all(f["state"] in SETTLED_FRAME_STATES for f in fr):
            return timeline(iid)
        time.sleep(0.25)


def terminals(tl, node):
    return [r for r in tl if r["node"] == node and r["kind"].startswith("terminal/")]


def deltas(tl, node):
    return [r["payload"].get("attributes_delta") for r in terminals(tl, node)]


def starts(tl, node):
    return [r for r in tl if r["node"] == node and r["kind"] == "work_started"]


def show(tl):
    for r in tl:
        if r["kind"] == "work_started" or r["kind"].startswith("terminal/"):
            print("    %-5s %-22s %-34s %s" % (r["seq"], r["node"], r["kind"], json.dumps(r["payload"])[:110]))


def check(label, ok, detail=""):
    STATE["checks"].append((label, bool(ok)))
    print(("PASS  " if ok else "FAIL  ") + label + ((" | " + detail) if detail else ""))


def finish():
    teardown()
    failed = [c for c in STATE["checks"] if not c[1]]
    print("")
    print("%d checks, %d failed" % (len(STATE["checks"]), len(failed)))
    sys.exit(1 if failed else 0)


def passthrough(node_type, subscribes=None, properties=None):
    node = {
        "type": node_type,
        "kind": "attribute_passthrough",
        "attributes": {"schema": {"type": "object",
                                  "properties": properties or {"v": {"type": "integer", "default": 1}}}},
    }
    if subscribes:
        node["subscribes"] = subscribes
    return node


def sub(source, signal, when=None, force=False):
    entry = {"node": source, "type": signal, "force_upstream_refresh": force}
    if when:
        entry["when"] = when
    return entry


import shutil
import tempfile

WORKDIRS = []
BRIDGE = []

HERE = os.path.dirname(os.path.abspath(__file__))

RIMSKY_YML = """persistence:
  driver: sqlite
  sqlite:
    path: /var/lib/rimsky/state.db
claim_producers: {}
named_locks: {}
executors:
  "verdict":
    transport: http
    endpoint: "http://host.docker.internal:%d"
    protocols: ["executor"]
"""

WHEN = 'payload.attributes_delta.verdict == "red"'


def producer(node_type):
    return {"type": node_type, "executor": "verdict", "max_retries": 0}


def watcher(node_type, source):
    return passthrough(node_type, [sub(source, "terminal/*", when=WHEN)],
                       {"saw": {"type": "string", "default": source}})


SPEC = {
    "name": "exp-uniform-attributes-delta",
    "version": "1",
    "nodes": [
        producer("ok_red"), producer("err_red"), producer("ok_green"), producer("err_green"),
        watcher("w_ok_red", "ok_red"), watcher("w_err_red", "err_red"),
        watcher("w_ok_green", "ok_green"), watcher("w_err_green", "err_green"),
    ],
}


def start_bridge():
    port = free_port()
    proc = subprocess.Popen([sys.executable, os.path.join(HERE, "executor_bridge.py"), str(port)])
    BRIDGE.append(proc)
    while True:
        try:
            with socket.create_connection(("127.0.0.1", port), timeout=1):
                return port
        except OSError:
            time.sleep(0.1)


def main():
    port = start_bridge()
    work = tempfile.mkdtemp(prefix="exp-uniform-attributes-delta-")
    WORKDIRS.append(work)
    cfg = os.path.join(work, "rimsky.yml")
    with open(cfg, "w") as fh:
        fh.write(RIMSKY_YML % port)
    boot(volumes=[cfg + ":/etc/rimsky/rimsky.yml:ro"],
         extra=["--add-host", "host.docker.internal:host-gateway"])

    iid = new_instance(deploy(SPEC))
    status, _ = send_message(iid)
    if status != 201:
        die("wake rejected: %s" % status)
    tl = quiet(iid)
    show(tl)

    def verdict_of(node):
        return [t["payload"].get("attributes_delta") for t in terminals(tl, node)]

    check("the executor wrote a verdict attribute alongside a success and alongside an error",
          verdict_of("ok_red") == [{"verdict": "red"}] and verdict_of("err_red") == [{"verdict": "red"}],
          json.dumps([verdict_of("ok_red"), verdict_of("err_red")]))
    check("the two producers settled on different terminal kinds",
          [t["kind"] for t in terminals(tl, "ok_red")] == ["terminal/success"]
          and [t["kind"] for t in terminals(tl, "err_red")] == ["terminal/error/probe/refused"],
          json.dumps([t["kind"] for t in terminals(tl, "ok_red")] +
                     [t["kind"] for t in terminals(tl, "err_red")]))

    check("one subscription form, predicated only on the verdict's attribute value, fired on the "
          "success", len(starts(tl, "w_ok_red")) == 1, str(len(starts(tl, "w_ok_red"))))
    check("the identical subscription fired on the error, with no per-kind entry written for it",
          len(starts(tl, "w_err_red")) == 1, str(len(starts(tl, "w_err_red"))))
    check("the same form stayed silent where the verdict carried the other value, on both kinds",
          not starts(tl, "w_ok_green") and not starts(tl, "w_err_green"),
          "%d %d" % (len(starts(tl, "w_ok_green")), len(starts(tl, "w_err_green"))))
    check("the erroring producer's attribute was not lost to its error: the watcher saw it",
          deltas(tl, "w_err_red") == [{"saw": "err_red"}], json.dumps(deltas(tl, "w_err_red")))
    check("all four producers ran, so the two silent watchers were silent by predicate, not by "
          "a missing signal",
          all(len(starts(tl, n)) == 1 for n in ("ok_red", "err_red", "ok_green", "err_green")),
          json.dumps({n: len(starts(tl, n)) for n in ("ok_red", "err_red", "ok_green", "err_green")}))

    finish()


try:
    main()
finally:
    teardown()
    for proc in BRIDGE:
        proc.terminate()
    for d in WORKDIRS:
        shutil.rmtree(d, ignore_errors=True)
