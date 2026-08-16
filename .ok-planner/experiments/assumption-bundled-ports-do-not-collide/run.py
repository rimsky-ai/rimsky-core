import json
import os
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
import uuid

TAG = os.environ.get("RIMSKY_IMAGE_TAG")
if not TAG:
    print("HARNESS ERROR: export RIMSKY_IMAGE_TAG=src-<tree hash> first")
    sys.exit(2)
SUFFIX = uuid.uuid4().hex[:6]
HOLDER = "exp-assumption-ports-core-" + SUFFIX
PG = "exp-assumption-ports-pg-" + SUFFIX
CHECKS = []
CONTAINERS = []
PORTS = [8080, 9081, 9082, 9083, 9084, 9090, 9091, 9092, 9095, 9096,
         9100, 9101, 9110, 9111, 9121, 9184, 9190]
MAPPED = {}

FS_YML = ("root: /data\nhost: 0.0.0.0\npick_policies:\n  work:\n    root: work\n"
          "    folder_pattern: \".*\"\n    on_commit: pop\n    on_give_up: recycle\n"
          "    visibility_timeout_seconds: 60\n    sync_strategy: on_drain\n")
FS_MOVED_YML = FS_YML + "grpc_port: 9200\nhttp_port: 9210\n"
PG_YML = ("connection: postgres://u:p@127.0.0.1:5432/rimsky?sslmode=disable\n"
          "write_semantics: read_only\n")

SERVICES = [
    ("sensor-cron", "rimsky-sensor-cron", [9081], 9081, None, {}, []),
    ("sensor-http", "rimsky-sensor-http", [9082], 9082, None, {}, []),
    ("sensor-object-store", "rimsky-sensor-object-store", [9083], 9083, None, {}, []),
    ("sensor-webhook", "rimsky-sensor-webhook", [9084, 9184], 9084, None, {}, []),
    ("claude-agent", "rimsky-executor-claude-agent", [9090, 9190], 9090, None,
     {"ANTHROPIC_API_KEY": "unused-by-this-run", "RIMSKY_EXECUTOR_STUB_MODE": "1"}, []),
    ("http-node", "rimsky-executor-http-node", [9091, 9092], 9091, None, {}, []),
    ("verifier-shape-checks", "rimsky-executor-verifier-shape-checks", [9095], 9095, None, {}, []),
    ("verifier-http", "rimsky-executor-verifier-http", [9096], 9096, None, {}, []),
    ("claim-producer-filesystem", "rimsky-claim-producer-filesystem", [9100, 9110], 9110, None,
     {"RIMSKY_CLAIM_PRODUCER_FILESYSTEM_CONFIG": "/cfg/fs.yml"}, [("CFG", "/cfg"), ("DATA", "/data")]),
    ("claim-producer-postgres", "rimsky-claim-producer-postgres", [9101, 9111], 9111, None,
     {"RIMSKY_CLAIM_PRODUCER_POSTGRES_CONFIG": "/cfg/pg.yml"}, [("CFG", "/cfg")]),
    ("subscriber-openlineage", "rimsky-subscriber-openlineage", [], None, "openlineage.starting",
     {"RIMSKY_OPENLINEAGE_RIMSKY_DSN": "postgres://u:p@127.0.0.1:5432/rimsky?sslmode=disable",
      "RIMSKY_OPENLINEAGE_BACKEND_URL": "http://127.0.0.1:9999"}, []),
]


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


def free_port():
    sock = socket.socket()
    sock.bind(("127.0.0.1", 0))
    port = sock.getsockname()[1]
    sock.close()
    return port


def check(label, ok, detail=""):
    CHECKS.append((label, bool(ok)))
    print(("PASS  " if ok else "FAIL  ") + label + ((" | " + str(detail)[:340]) if detail else ""))


def finish():
    teardown()
    failed = [c for c in CHECKS if not c[1]]
    print("")
    print("%d checks, %d failed" % (len(CHECKS), len(failed)))
    print("RESULT: " + ("FAIL" if failed else "PASS"))
    sys.exit(1 if failed else 0)


def image(name):
    ref = "%s:%s" % (name, TAG)
    if docker("image", "inspect", ref).returncode != 0:
        die("image %s is not present locally; build it with: make core-images service-images" % ref)
    return ref


def running(name):
    return docker("inspect", "-f", "{{.State.Running}}", name).stdout.strip() == "true"


def logs(name):
    res = docker("logs", name)
    return res.stdout + res.stderr


def tcp_open(port):
    sock = socket.socket()
    sock.settimeout(2)
    try:
        sock.connect(("127.0.0.1", MAPPED[port]))
        sock.close()
        return True
    except Exception:
        return False


def http_get(port, path):
    try:
        with urllib.request.urlopen("http://127.0.0.1:%d%s" % (MAPPED[port], path), timeout=30) as r:
            return r.status, r.read().decode()
    except urllib.error.HTTPError as exc:
        return exc.code, ""
    except Exception:
        return 0, ""


def join_namespace(name, ref, env=None, mounts=None):
    docker("rm", "-f", name)
    args = ["run", "-d", "--name", name, "--network", "container:" + HOLDER]
    for host, guest in (mounts or []):
        args += ["-v", "%s:%s" % (host, guest)]
    for key, value in (env or {}).items():
        args += ["-e", "%s=%s" % (key, value)]
    args.append(ref)
    res = docker(*args)
    CONTAINERS.append(name)
    if res.returncode != 0:
        die("docker run %s failed: %s" % (name, res.stderr.strip()))
    return name


cfgdir = tempfile.mkdtemp()
os.chmod(cfgdir, 0o777)
datadir = tempfile.mkdtemp()
os.chmod(datadir, 0o777)
os.makedirs(os.path.join(datadir, "work"), exist_ok=True)
os.chmod(os.path.join(datadir, "work"), 0o777)
for name, body in (("fs.yml", FS_YML), ("fs-moved.yml", FS_MOVED_YML), ("pg.yml", PG_YML)):
    with open(os.path.join(cfgdir, name), "w") as fh:
        fh.write(body)
places = {"CFG": cfgdir, "DATA": datadir}

print("== the core stack, holding the namespace every service will join ==")
args = ["run", "-d", "--name", HOLDER]
for port in PORTS:
    MAPPED[port] = free_port()
    args += ["-p", "127.0.0.1:%d:%d" % (MAPPED[port], port)]
MAPPED[5432] = free_port()
args += ["-p", "127.0.0.1:%d:5432" % MAPPED[5432], "-p", "127.0.0.1:%d:9200" % free_port()]
args.append(image("rimsky-all-in-one"))
res = docker(*args)
CONTAINERS.append(HOLDER)
if res.returncode != 0:
    die("docker run %s failed: %s" % (HOLDER, res.stderr.strip()))
while http_get(8080, "/v1/health")[0] != 200:
    if not running(HOLDER):
        die("core stack exited during boot:\n" + logs(HOLDER)[-1500:])
    time.sleep(0.3)
check("the core stack takes 8080 for its API and 9100 for the supervisor's callback listener",
      http_get(8080, "/v1/health")[0] == 200 and http_get(9100, "/health")[0] == 200,
      "both listeners answer inside the namespace")

join_namespace(PG, "postgres:16-alpine",
               env={"POSTGRES_USER": "u", "POSTGRES_PASSWORD": "p", "POSTGRES_DB": "rimsky"})
while docker("exec", PG, "pg_isready", "-h", "127.0.0.1", "-U", "u", "-d", "rimsky").returncode != 0:
    if not running(PG):
        die("postgres exited:\n" + logs(PG)[-1000:])
    time.sleep(0.3)

print("")
print("== the eleven bundled services, default ports, same namespace ==")
outcome = {}
for service, img, ports, probe, marker, env, mounts in SERVICES:
    name = "exp-assumption-ports-%s-%s" % (service, SUFFIX)
    join_namespace(name, image(img), env=env,
                   mounts=[(places[a], b) for a, b in mounts])
    while True:
        if not running(name):
            outcome[service] = ("exited", logs(name).strip().splitlines()[-1] if logs(name).strip() else "")
            break
        if (probe is not None and tcp_open(probe)) or (marker is not None and marker in logs(name)):
            outcome[service] = ("listening", "")
            break
        time.sleep(0.2)
    print("    %-28s %s" % (service, outcome[service][0]))

up = sorted(s for s, (state, _) in outcome.items() if state == "listening")
down = sorted(s for s, (state, _) in outcome.items() if state == "exited")
check("the eleven services do not all come up on their default ports",
      len(down) >= 1, "came up: %d, failed: %s" % (len(up), down))
fs_state, fs_error = outcome["claim-producer-filesystem"]
check("the filesystem claim producer wants the port the supervisor's callback listener already holds",
      fs_state == "exited" and "9100" in fs_error and "address already in use" in fs_error,
      fs_error[-200:])
check("every other bundled service found its default port free",
      down == ["claim-producer-filesystem"], "failed: %s" % down)

print("")
print("== the host-agent proxy, on the same host ==")
proxy = "exp-assumption-ports-proxy-" + SUFFIX
join_namespace(proxy, image("rimsky-host-agent-proxy"),
               env={"RIMSKY_CONTROL_API_URL": "http://127.0.0.1:8080"})
while True:
    if not running(proxy):
        proxy_state, proxy_error = "exited", logs(proxy).strip().splitlines()[-1]
        break
    if "listening" in logs(proxy):
        proxy_state, proxy_error = "listening", ""
        break
    time.sleep(0.2)
check("the proxy's default ports are the two an executor already holds",
      proxy_state == "exited" and ("9090" in proxy_error or "9091" in proxy_error),
      proxy_error[-200:])

print("")
print("== moving the colliding port by configuration ==")
moved = "exp-assumption-ports-fs-moved-" + SUFFIX
join_namespace(moved, image("rimsky-claim-producer-filesystem"),
               env={"RIMSKY_CLAIM_PRODUCER_FILESYSTEM_CONFIG": "/cfg/fs-moved.yml"},
               mounts=[(cfgdir, "/cfg"), (datadir, "/data")])
while True:
    if not running(moved):
        die("the moved producer exited:\n" + logs(moved)[-800:])
    if "claim-producer-filesystem started" in logs(moved):
        break
    time.sleep(0.2)
check("the same producer comes up once its ports are named in its config",
      "9200" in logs(moved) and running(moved),
      next((l[-140:] for l in logs(moved).splitlines() if "started" in l), ""))

finish()
