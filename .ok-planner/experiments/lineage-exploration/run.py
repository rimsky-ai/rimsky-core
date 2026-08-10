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

SLUG = "lineage-exploration"
HERE = os.path.dirname(os.path.abspath(__file__))
CHECKS = {"type": "array", "default": [{"kind": "row_count_absolute", "config": {"min": 0}}]}

SPEC = {
    "name": "exp-lineage-exploration",
    "version": "1",
    "messages": [{"type": "ops/kick"}],
    "nodes": [
        {"type": "producer", "executor": "verifier-shape-checks",
         "subscribes": [{"node": "ops/kick", "type": "terminal/success", "force_upstream_refresh": False}],
         "claim_producers": [{"name": "claim-producer-filesystem", "selector": "data",
                              "intent": "rw", "alias": "parent"}],
         "error_types": {"acquire/unavailable": {"action": "give_up"}},
         "fan_out": {"claim": "parent",
                     "partition_request": '{"list":[{"key":"p1"},{"key":"p2"}]}',
                     "parallelism": 1, "error_policy": {"kind": "strict"}},
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


def records(path):
    status, out = call("GET", path)
    if status != 200:
        die("GET %s -> %s %s" % (path, status, out))
    return out


def main():
    root = tempfile.mkdtemp(prefix="rimsky-exp-lineage-")
    STATE["tmp"].append(root)
    os.makedirs(os.path.join(root, "data"))
    with open(os.path.join(root, "cp.yml"), "w") as fh:
        fh.write("root: /workspace\nhost: 127.0.0.1\ngrpc_port: 9200\n"
                 "http_port: 9210\nsweep_interval_seconds: 60\n")
    boot_rimsky(SLUG, env={"RIMSKY_CLAIM_PRODUCER_FILESYSTEM_CONFIG": "/etc/rimsky/cp.yml"},
                volumes=["%s/cp.yml:/etc/rimsky/cp.yml:ro" % root, "%s:/workspace:rw" % root])
    iid = new_instance(deploy(SPEC))
    send_message(iid, {"type": "ops/kick"})
    wait_until(lambda: node_runs(iid).get("consumer", 0) >= 1 or None)

    by_attr = wait_until(lambda: records("/v1/lineage/by-source/attribute/verifier_rows")["records"] or None)
    check("lineage can be queried by the source a run drew its input from",
          len(by_attr) == 1 and by_attr[0]["record"]["node_alias"] == "consumer",
          json.dumps([r["record"]["node_alias"] for r in by_attr]))
    consumer_rec = by_attr[0]["record"]
    consumer_run = consumer_rec["run_id"]
    upstream = [r for r in consumer_rec["substitution_refs"] if r["source_kind"] == "run"]
    check("the run's record names the upstream run its input came from",
          len(upstream) == 1 and upstream[0]["source_node_alias"] == "producer",
          json.dumps(consumer_rec["substitution_refs"]))
    producer_run = upstream[0]["source_version_or_id"]

    one = records("/v1/lineage/runs/%s" % consumer_run)
    check("a single run's lineage record can be read by its run id",
          one["record"]["run_id"] == consumer_run and one["record_kind"] == "leaf_run",
          json.dumps(one["record"]["node_alias"]))

    back = records("/v1/lineage/runs/%s/ancestors" % consumer_run)["ancestors"]
    check("walking a run backward reaches the run that produced its input",
          any(a["record"]["run_id"] == producer_run for a in back),
          json.dumps([a["record"]["node_alias"] for a in back]))

    fwd = records("/v1/lineage/runs/%s/descendants" % producer_run)["descendants"]
    check("walking that run forward reaches the run that consumed its output",
          any(d["record"]["run_id"] == consumer_run for d in fwd),
          json.dumps([d["record"]["node_alias"] for d in fwd]))

    by_run = records("/v1/lineage/by-source/run/%s" % producer_run)["records"]
    check("lineage can be queried by an upstream run as the source",
          any(r["record"]["run_id"] == consumer_run for r in by_run),
          json.dumps([r["record"]["node_alias"] for r in by_run]))

    depth1 = records("/v1/lineage/runs/%s/ancestors?depth=1" % consumer_run)
    check("the walk depth is the caller's to choose", depth1["depth"] == 1, json.dumps(depth1["depth"]))

    by_prod = records("/v1/lineage/by-producer/claim-producer-filesystem")["records"]
    check("lineage can be queried by the named producer that committed the work",
          len(by_prod) == 3 and all(r["record_kind"] == "claim_terminal" for r in by_prod),
          json.dumps([r["record"]["outcome"] for r in by_prod]))
    check("a producer that committed nothing answers with no records",
          records("/v1/lineage/by-producer/claim-producer-postgres")["records"] == [],
          json.dumps(records("/v1/lineage/by-producer/claim-producer-postgres")["records"]))

    parents = [r["record"] for r in by_prod if r["record"].get("sub_claim_handle_ids")]
    check("the committed claims include one that was split into sub-claims",
          len(parents) == 1 and len(parents[0]["sub_claim_handle_ids"]) == 2,
          json.dumps([p["claim_handle_id"] for p in parents]))
    parent = parents[0]["claim_handle_id"]
    subs = parents[0]["sub_claim_handle_ids"]

    claim = records("/v1/lineage/claims/%s" % parent)
    check("lineage can be queried by claim handle",
          claim["record"]["claim_handle_id"] == parent
          and claim["record"]["producer_name"] == "claim-producer-filesystem",
          json.dumps(claim["record"]["outcome"]))

    down = records("/v1/lineage/claims/%s/descendants" % parent)["descendants"]
    got = set(d["record"]["claim_handle_id"] for d in down)
    check("walking a claim handle forward reaches the sub-claims it was split into",
          set(subs).issubset(got), json.dumps(sorted(got)))

    up = records("/v1/lineage/claims/%s/ancestors" % subs[0])["ancestors"]
    check("walking a sub-claim backward reaches the claim it was split from",
          parent in set(a["record"]["claim_handle_id"] for a in up),
          json.dumps([a["record"]["claim_handle_id"] for a in up]))

    status, _ = call("GET", "/v1/lineage/runs/%s" % uuid.uuid4())
    check("a run with no lineage answers not-found rather than an empty walk", status == 404, status)
    finish()


try:
    main()
finally:
    teardown()
    for d in STATE["tmp"]:
        shutil.rmtree(d, ignore_errors=True)
