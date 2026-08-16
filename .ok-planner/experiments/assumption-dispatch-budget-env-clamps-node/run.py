import json
import os
import shutil
import socket
import subprocess
import sys
import time
import urllib.error
import urllib.request
import uuid

HERE = os.path.dirname(os.path.abspath(__file__))
TAG = os.environ.get("RIMSKY_IMAGE_TAG")
if not TAG:
    print("HARNESS ERROR: export RIMSKY_IMAGE_TAG=src-<tree hash> first")
    sys.exit(2)
IMAGE = "rimsky-all-in-one:" + TAG
PROBE_BINARY = "/probe/probe-agent"
OPERATOR_CEILING = "1.00"
STATE = {"base": None}
CHECKS = []
CONTAINERS = []
SETTLED = ("completed", "failed", "terminated")


def docker(*args):
    return subprocess.run(["docker", *args], capture_output=True, text=True)


def die(msg):
    print("HARNESS ERROR: " + msg)
    teardown()
    sys.exit(2)


def teardown():
    for name in list(CONTAINERS):
        docker("rm", "-f", name)
    del CONTAINERS[:]


def purge_build():
    shutil.rmtree(os.path.join(HERE, ".probe-build"), ignore_errors=True)


def free_port():
    sock = socket.socket()
    sock.bind(("127.0.0.1", 0))
    port = sock.getsockname()[1]
    sock.close()
    return port


def check(label, ok, detail=""):
    CHECKS.append((label, bool(ok)))
    print(("PASS  " if ok else "FAIL  ") + label + ((" | " + str(detail)[:300]) if detail else ""))


def finish():
    teardown()
    purge_build()
    failed = [c for c in CHECKS if not c[1]]
    print("")
    print("%d checks, %d failed" % (len(CHECKS), len(failed)))
    print("RESULT: " + ("FAIL" if failed else "PASS"))
    sys.exit(1 if failed else 0)


def call(method, path, body=None, headers=None):
    data = None if body is None else json.dumps(body).encode()
    hdrs = {"Content-Type": "application/json"}
    if headers:
        hdrs.update(headers)
    req = urllib.request.Request(STATE["base"] + path, data=data, headers=hdrs, method=method)
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            raw = resp.read().decode()
            return resp.status, (json.loads(raw) if raw else None)
    except urllib.error.HTTPError as exc:
        raw = exc.read().decode()
        try:
            return exc.code, json.loads(raw)
        except ValueError:
            return exc.code, raw
    except Exception as exc:
        return 0, str(exc)


def running(name):
    return docker("inspect", "-f", "{{.State.Running}}", name).stdout.strip() == "true"


def logs(name):
    res = docker("logs", name)
    return res.stdout + res.stderr


def docker_arch():
    arch = docker("version", "--format", "{{.Server.Arch}}").stdout.strip()
    if not arch:
        die("cannot read docker server architecture")
    return arch


def build_probe_agent():
    workdir = os.path.join(HERE, ".probe-build")
    os.makedirs(workdir, exist_ok=True)
    with open(os.path.join(workdir, "main.go"), "w") as fh:
        fh.write(open(os.path.join(HERE, "probe-agent.go.txt")).read())
    with open(os.path.join(workdir, "go.mod"), "w") as fh:
        fh.write("module probeagent\n\ngo 1.25\n")
    binary = os.path.join(workdir, "probe-agent")
    res = subprocess.run(["go", "build", "-o", binary, "."], cwd=workdir,
                         env=dict(os.environ, GOOS="linux", GOARCH=docker_arch(),
                                  CGO_ENABLED="0", GOWORK="off"),
                         capture_output=True, text=True)
    if res.returncode != 0:
        die("go build (container target) failed: " + res.stderr.strip())
    return binary


def boot(probe_binary, env):
    if docker("image", "inspect", IMAGE).returncode != 0:
        die("image %s is not present locally; build it with: make core-images" % IMAGE)
    name = "exp-assumption-dispatch-budget-" + uuid.uuid4().hex[:6]
    port = free_port()
    args = ["run", "-d", "--name", name, "-p", "127.0.0.1:%d:8080" % port,
            "-v", "%s:%s:ro" % (probe_binary, PROBE_BINARY),
            "-e", "RIMSKY_SUPERVISOR_CALLBACK_ADVERTISE_HOST=127.0.0.1"]
    for key, value in env.items():
        args += ["-e", "%s=%s" % (key, value)]
    args.append(IMAGE)
    res = docker(*args)
    CONTAINERS.append(name)
    if res.returncode != 0:
        die("docker run failed: " + res.stderr.strip())
    STATE["base"] = "http://127.0.0.1:%d" % port
    while True:
        if not running(name):
            die("container exited during boot:\n" + logs(name)[-1500:])
        if call("GET", "/v1/health")[0] == 200:
            return name
        time.sleep(0.3)


def deploy(spec):
    status, out = call("POST", "/v1/templates", {"spec": spec})
    if status not in (200, 201):
        die("template register rejected: %s %s" % (status, out))
    tid = out["template_id"]
    status, out = call("POST", "/v1/templates/%s/deploy" % tid, {})
    if status not in (200, 201):
        die("template deploy rejected: %s %s" % (status, out))
    return tid


def new_instance(tid):
    status, out = call("POST", "/v1/instances", {
        "template": tid, "instance_key": "exp-" + uuid.uuid4().hex[:12],
        "target_agent": "audit-agent"})
    if status not in (200, 201):
        die("instance create rejected: %s %s" % (status, out))
    return out["instance_id"]


def send_message(iid):
    return call("POST", "/v1/instances/%s/messages" % iid, {},
                {"Idempotency-Key": uuid.uuid4().hex})


def timeline(iid):
    types = {n["id"]: n["node_type"]
             for n in call("GET", "/v1/instances/%s/nodes" % iid)[1]["nodes"]}
    rows = call("GET", "/v1/observability/events?instance_id=%s&limit=500" % iid)[1]["events"] or []
    return [{"seq": e["id"], "node": types.get(e.get("node_id"), ""), "kind": e["kind"],
             "payload": e["payload"] or {}} for e in sorted(rows, key=lambda r: r["id"])]


def quiet(iid):
    while True:
        frames = call("GET", "/v1/instances/%s/frames" % iid)[1]["frames"]
        live = call("GET", "/v1/observability/node-runs?instance_id=%s" % iid)[1]["node_runs"]
        if frames and all(f["state"] in SETTLED for f in frames) and not live:
            return timeline(iid)
        time.sleep(0.25)


def agent_node(node_type, cli):
    return {"type": node_type, "executor": "claude-agent",
            "attributes": {"schema": {"type": "object", "properties": {
                "model": {"type": "string", "default": "probe-model"},
                "system_prompt": {"type": "string", "default": "budget probe"},
                "user_prompt": {"type": "string", "default": "probe.mode=observe"},
                "cli": {"type": "object", "default": cli}}}}}


SPEC = {
    "name": "exp-assumption-dispatch-budget",
    "version": "1",
    "nodes": [
        agent_node("declares-more", {"max_budget_usd": "50.00"}),
        agent_node("declares-less", {"max_budget_usd": "0.25"}),
        agent_node("declares-nothing", {}),
    ],
}


def budget_of(tl, node_type):
    rows = [r for r in tl if r["node"] == node_type and r["kind"].startswith("terminal/")]
    if not rows:
        return None
    obs = (rows[-1]["payload"].get("attributes_delta") or {}).get("cli_observation")
    if not isinstance(obs, dict):
        return rows[-1]["payload"]
    argv = obs.get("argv") or []
    for i, arg in enumerate(argv):
        if arg == "--max-budget-usd" and i + 1 < len(argv):
            return argv[i + 1]
    return ""


probe = build_probe_agent()
print("== a deployment ceiling of $%s, three nodes declaring different budgets ==" % OPERATOR_CEILING)
boot(probe, {"CLAUDE_CODE_OAUTH_TOKEN": "probe-stand-in",
             "RIMSKY_EXECUTOR_CLAUDE_BINARY": PROBE_BINARY,
             "RIMSKY_DISPATCH_MAX_USD": OPERATOR_CEILING})
iid = new_instance(deploy(SPEC))
send_message(iid)
tl = quiet(iid)
for row in tl:
    if row["kind"].startswith("terminal/"):
        print("    %-5s %-18s %-20s %s" % (row["seq"], row["node"], row["kind"],
                                           json.dumps(row["payload"])[:120]))

more = budget_of(tl, "declares-more")
less = budget_of(tl, "declares-less")
nothing = budget_of(tl, "declares-nothing")
check("a node declaring more than the deployment ceiling runs at its own figure",
      more == "50.00", "the agent was spawned with --max-budget-usd %s" % more)
check("a node declaring less than the ceiling runs at its own figure too",
      less == "0.25", "the agent was spawned with --max-budget-usd %s" % less)
check("the ceiling applies only where the node declares nothing",
      nothing == OPERATOR_CEILING, "the agent was spawned with --max-budget-usd %s" % nothing)
check("so the deployment figure is a default, not a clamp",
      more != OPERATOR_CEILING and float(more) > float(OPERATOR_CEILING),
      "node asked for %s under a %s ceiling and got %s" % ("50.00", OPERATOR_CEILING, more))

print("")
print("== the same template with no deployment ceiling set ==")
teardown()
boot(probe, {"CLAUDE_CODE_OAUTH_TOKEN": "probe-stand-in",
             "RIMSKY_EXECUTOR_CLAUDE_BINARY": PROBE_BINARY})
iid = new_instance(deploy(SPEC))
send_message(iid)
tl = quiet(iid)
check("without the variable a node's own budget still reaches the agent",
      budget_of(tl, "declares-more") == "50.00",
      "--max-budget-usd %s" % budget_of(tl, "declares-more"))
check("and a node declaring nothing is spawned with no budget flag at all",
      budget_of(tl, "declares-nothing") == "",
      "--max-budget-usd %r" % budget_of(tl, "declares-nothing"))

finish()
