import json
import os
import socket
import subprocess
import sys
import time
import urllib.error
import urllib.request
import uuid

TAG = os.environ.get("RIMSKY_IMAGE_TAG")
if not TAG:
    print("HARNESS ERROR: set RIMSKY_IMAGE_TAG to the tag built from this tree")
    sys.exit(2)

STATE = {"base": None, "containers": [], "networks": [], "checks": [], "tmp": []}
SETTLED_FRAME_STATES = ("completed", "failed", "terminated")


def die(msg):
    print("HARNESS ERROR: " + msg)
    teardown()
    sys.exit(2)


def docker(*args):
    return subprocess.run(["docker", *args], capture_output=True, text=True)


def image(name):
    ref = "%s:%s" % (name, TAG)
    if docker("image", "inspect", ref).returncode != 0:
        die("image %s is not present locally; build it with: make core-images service-images test-images" % ref)
    return ref


def free_port():
    sock = socket.socket()
    sock.bind(("127.0.0.1", 0))
    port = sock.getsockname()[1]
    sock.close()
    return port


def network(slug):
    name = "rimsky-exp-%s-%s" % (slug, uuid.uuid4().hex[:6])
    res = docker("network", "create", name)
    if res.returncode != 0:
        die("docker network create failed: " + res.stderr.strip())
    STATE["networks"].append(name)
    return name


def run_container(name, ref, net=None, alias=None, env=None, ports=None, volumes=None, cmd=None):
    args = ["run", "-d", "--name", name]
    if net:
        args += ["--network", net]
        args += ["--network-alias", alias or name]
    for host, cport in (ports or []):
        args += ["-p", "127.0.0.1:%d:%d" % (host, cport)]
    for key, value in (env or {}).items():
        args += ["-e", "%s=%s" % (key, value)]
    for spec in (volumes or []):
        args += ["-v", spec]
    args.append(ref)
    if cmd:
        args += list(cmd)
    res = docker(*args)
    if res.returncode != 0:
        die("docker run %s failed: %s" % (name, res.stderr.strip()))
    if name not in STATE["containers"]:
        STATE["containers"].append(name)
    return name


def logs(name):
    res = docker("logs", name)
    return res.stdout + res.stderr


def teardown():
    for name in STATE["containers"]:
        docker("rm", "-f", name)
    STATE["containers"] = []
    for net in STATE["networks"]:
        docker("network", "rm", net)
    STATE["networks"] = []


def call(method, path, body=None, headers=None, base=None):
    data = None if body is None else json.dumps(body).encode()
    hdrs = {"Content-Type": "application/json"}
    if headers:
        hdrs.update(headers)
    req = urllib.request.Request((base or STATE["base"]) + path, data=data, headers=hdrs, method=method)
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


def raw_post(url, body, headers=None):
    req = urllib.request.Request(url, data=body, headers=headers or {}, method="POST")
    try:
        with urllib.request.urlopen(req) as resp:
            return resp.status, resp.read().decode()
    except urllib.error.HTTPError as exc:
        return exc.code, exc.read().decode()
    except urllib.error.URLError as exc:
        return 0, str(exc)


def boot_rimsky(slug, net=None, alias=None, config_path=None, env=None, volumes=None):
    port = free_port()
    name = "rimsky-exp-%s-api-%s" % (slug, uuid.uuid4().hex[:6])
    vols = list(volumes or [])
    if config_path:
        vols.append("%s:/etc/rimsky/rimsky.yml:ro" % config_path)
    run_container(name, image("rimsky-all-in-one"), net=net, alias=alias or "rimsky",
                  env=env, ports=[(port, 8080)], volumes=vols)
    STATE["base"] = "http://127.0.0.1:%d" % port
    while True:
        try:
            if call("GET", "/v1/health")[0] == 200:
                return name
        except Exception:
            pass
        time.sleep(0.3)


def deploy(spec):
    status, out = call("POST", "/v1/templates", {"spec": spec})
    if status not in (200, 201):
        die("template register rejected: %s %s" % (status, out))
    template_id = out["template_id"]
    status, out = call("POST", "/v1/templates/%s/deploy" % template_id, {})
    if status not in (200, 201):
        die("template deploy rejected: %s %s" % (status, out))
    return template_id


def new_instance(template_id):
    status, out = call("POST", "/v1/instances", {
        "template": template_id,
        "instance_key": "exp-" + uuid.uuid4().hex[:12],
        "target_agent": "audit-agent"})
    if status not in (200, 201):
        die("instance create rejected: %s %s" % (status, out))
    return out["instance_id"]


def send_message(iid, body=None):
    return call("POST", "/v1/instances/%s/messages" % iid,
                {} if body is None else body,
                {"Idempotency-Key": uuid.uuid4().hex})


def instance(iid):
    return call("GET", "/v1/instances/%s" % iid)[1]


def wait_subscriptions_active(iid):
    while True:
        d = instance(iid)
        subs = d.get("subscriptions") or []
        for s in subs:
            if s["state"] == "failed":
                die("publisher subscription %s failed: %s" % (s["publisher_name"], s.get("failure_reason")))
        if subs and all(s["state"] == "active" for s in subs):
            return subs
        time.sleep(0.25)


def messages(iid, query=""):
    return call("GET", "/v1/instances/%s/messages%s" % (iid, query))[1]["messages"]


def frames(iid):
    return call("GET", "/v1/instances/%s/frames" % iid)[1]["frames"]


def nodes(iid):
    out = call("GET", "/v1/instances/%s/nodes" % iid)[1]
    return out["nodes"] if isinstance(out, dict) else out


def node_runs(iid):
    return {n["node_type"]: n["run_summary"]["fresh_count"] + n["run_summary"]["failed_count"]
            for n in nodes(iid) if n.get("node_type")}


def wait_until(fn, note=""):
    while True:
        value = fn()
        if value:
            return value
        time.sleep(0.25)


def wait_node_ran(iid, node_type, count=1):
    return wait_until(lambda: node_runs(iid).get(node_type, 0) >= count)


def check(label, ok, detail=""):
    STATE["checks"].append((label, bool(ok)))
    print(("PASS  " if ok else "FAIL  ") + label + ((" | " + str(detail)[:300]) if detail else ""))


def finish():
    teardown()
    failed = [c for c in STATE["checks"] if not c[1]]
    print("")
    print("%d checks, %d failed" % (len(STATE["checks"]), len(failed)))
    print("EXPERIMENT PASS" if not failed else "EXPERIMENT FAIL")
    sys.exit(1 if failed else 0)


import shutil
import tempfile

SLUG = "lineage-admin"
HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.abspath(os.path.join(HERE, "..", "..", ".."))
CLI = os.path.join(ROOT, "bin", "rimsky")
CHECKS = {"type": "array", "default": [{"kind": "row_count_absolute", "config": {"min": 0}}]}

SPEC = {
    "name": "exp-lineage-admin",
    "version": "1",
    "messages": [{"type": "ops/kick"}],
    "nodes": [
        {"type": "producer", "executor": "verifier-shape-checks",
         "subscribes": [{"node": "ops/kick", "type": "terminal/success", "force_upstream_refresh": False}],
         "claim_producers": [{"name": "claim-producer-filesystem", "selector": "data",
                              "intent": "rw", "alias": "held"}],
         "error_types": {"acquire/unavailable": {"action": "give_up"}},
         "attributes": {"schema": {"type": "object", "properties": {
             "checks": CHECKS, "verifier_rows": {"type": "integer", "default": 0}}}}},
        {"type": "consumer", "kind": "attribute_passthrough",
         "subscribes": [{"node": "producer", "type": "terminal/success", "force_upstream_refresh": False},
                        {"node": "producer", "type": "attribute/verifier_rows/changed",
                         "force_upstream_refresh": False}],
         "attributes": {"schema": {"type": "object", "properties": {
             "seen": {"type": "integer", "source": "{{nodes.producer.attribute.verifier_rows}}"}}}}},
    ],
}


def cli(*args):
    res = subprocess.run([CLI, *args, "--endpoint", STATE["base"], "-o", "json"],
                         capture_output=True, text=True)
    return res.returncode, res.stdout.strip(), res.stderr.strip()


def records(path):
    status, out = call("GET", path)
    if status != 200:
        die("GET %s -> %s %s" % (path, status, out))
    return out


def rfc(ts):
    return time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(ts))


def leaf_runs():
    return records("/v1/lineage/by-source/attribute/verifier_rows")["records"]


def claim_records():
    return records("/v1/lineage/by-producer/claim-producer-filesystem")["records"]


def run_a_workflow(tid):
    iid = new_instance(tid)
    send_message(iid, {"type": "ops/kick"})
    wait_until(lambda: node_runs(iid).get("consumer", 0) >= 1 or None)
    return iid


def main():
    if not os.path.exists(CLI):
        die("build the CLI first: make cli")
    root = tempfile.mkdtemp(prefix="rimsky-exp-lineage-admin-")
    STATE["tmp"].append(root)
    os.makedirs(os.path.join(root, "data"))
    with open(os.path.join(root, "cp.yml"), "w") as fh:
        fh.write("root: /workspace\nhost: 127.0.0.1\ngrpc_port: 9200\n"
                 "http_port: 9210\nsweep_interval_seconds: 60\n")
    boot_rimsky(SLUG, config_path=os.path.join(HERE, "rimsky.yml"),
                env={"RIMSKY_CLAIM_PRODUCER_FILESYSTEM_CONFIG": "/etc/rimsky/cp.yml"},
                volumes=["%s/cp.yml:/etc/rimsky/cp.yml:ro" % root, "%s:/workspace:rw" % root])

    tid = deploy(SPEC)
    iid = run_a_workflow(tid)
    wait_until(lambda: leaf_runs() or None)
    wait_until(lambda: claim_records() or None)
    consumer_run = leaf_runs()[0]["record"]["run_id"]
    check("a deployment that has run work is holding lineage records",
          records("/v1/lineage/runs/%s" % consumer_run)["record"]["run_id"] == consumer_run
          and len(claim_records()) >= 1,
          "%d claim records, %d leaf-run records" % (len(claim_records()), len(leaf_runs())))

    wait_until(lambda: frames(iid) == [] or None)
    check("the run tree has aged out while the lineage records remain",
          frames(iid) == [] and len(leaf_runs()) >= 1 and len(claim_records()) >= 1,
          "%d frames, %d leaf-run records, %d claim records"
          % (len(frames(iid)), len(leaf_runs()), len(claim_records())))

    before_all = rfc(time.time() - 3600)
    code, out, err = cli("lineage", "prune", "--before", before_all)
    check("pruning with a cutoff older than every record deletes nothing",
          code == 0 and json.loads(out)["deleted"] == 0, out or err)
    check("the records the cutoff excluded are still readable",
          records("/v1/lineage/runs/%s" % consumer_run)["record"]["run_id"] == consumer_run,
          "%d leaf-run records, %d claim records" % (len(leaf_runs()), len(claim_records())))

    held = len(leaf_runs()) + len(claim_records())
    code, out, err = cli("lineage", "prune", "--before", rfc(time.time() + 3600))
    deleted = json.loads(out)["deleted"] if code == 0 else -1
    check("pruning with a cutoff newer than the records deletes them",
          code == 0 and deleted >= held, "deleted %s, at least %d were readable" % (deleted, held))
    check("the pruned records are gone from every lineage read",
          call("GET", "/v1/lineage/runs/%s" % consumer_run)[0] == 404
          and claim_records() == [] and leaf_runs() == [],
          "run record now %d" % call("GET", "/v1/lineage/runs/%s" % consumer_run)[0])

    iid2 = run_a_workflow(tid)
    wait_until(lambda: leaf_runs() or None)
    check("work run after a prune records lineage again", len(leaf_runs()) >= 1,
          json.dumps([r["record"]["node_alias"] for r in leaf_runs()]))
    wait_until(lambda: frames(iid2) == [] or None)
    code, out, err = cli("lineage", "prune", "--older-than", "1s")
    check("the cutoff can be given as an age instead of a timestamp",
          code == 0 and json.loads(out)["deleted"] >= 1, out or err)

    status, body = call("POST", "/v1/admin/lineage/prune", {"before": "not-a-timestamp"})
    check("a malformed cutoff is refused rather than guessed at", status == 400, json.dumps(body))
    status, body = call("POST", "/v1/admin/lineage/prune", {})
    check("a prune with no cutoff is refused", status == 400, json.dumps(body))
    finish()


try:
    main()
finally:
    teardown()
    for d in STATE["tmp"]:
        shutil.rmtree(d, ignore_errors=True)
