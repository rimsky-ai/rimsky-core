import os
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request

TAG = os.environ.get("RIMSKY_IMAGE_TAG")
if not TAG:
    print("HARNESS ERROR: export RIMSKY_IMAGE_TAG=src-<tree hash> first")
    sys.exit(2)
ALL_IN_ONE = "rimsky-all-in-one:" + TAG
PROXY = "rimsky-host-agent-proxy:" + TAG
NET = "exp-assumption-health-net"
PG = "exp-assumption-health-pg"
CHECKS = []
CONTAINERS = []
NETWORKS = []

FS_YML = ("root: /data\n"
          "pick_policies:\n"
          "  work:\n"
          "    root: work\n"
          "    folder_pattern: \".*\"\n"
          "    on_commit: pop\n"
          "    on_give_up: recycle\n"
          "    visibility_timeout_seconds: 60\n"
          "    sync_strategy: on_drain\n")

PG_YML = ("connection: postgres://rimsky:rimsky@" + PG + ":5432/rimsky?sslmode=disable\n"
          "write_semantics: read_only\n")

SERVICES = [
    ("sensor-cron", "rimsky-sensor-cron", [9081], {}, []),
    ("sensor-http", "rimsky-sensor-http", [9082], {}, []),
    ("sensor-object-store", "rimsky-sensor-object-store", [9083], {}, []),
    ("sensor-webhook", "rimsky-sensor-webhook", [9084, 9184], {}, []),
    ("http-node", "rimsky-executor-http-node", [9091, 9092], {}, []),
    ("verifier-http", "rimsky-executor-verifier-http", [9096], {}, []),
    ("verifier-shape-checks", "rimsky-executor-verifier-shape-checks", [9095], {}, []),
    ("claude-agent", "rimsky-executor-claude-agent", [9090, 9190],
     {"ANTHROPIC_API_KEY": "unused-by-this-run", "RIMSKY_EXECUTOR_STUB_MODE": "1"}, []),
    ("claim-producer-filesystem", "rimsky-claim-producer-filesystem", [9100, 9110],
     {"RIMSKY_CLAIM_PRODUCER_FILESYSTEM_CONFIG": "/cfg/fs.yml"}, [("CFG", "/cfg"), ("DATA", "/data")]),
    ("claim-producer-postgres", "rimsky-claim-producer-postgres", [9101, 9111],
     {"RIMSKY_CLAIM_PRODUCER_POSTGRES_CONFIG": "/cfg/pg.yml"}, [("CFG", "/cfg")]),
]


def docker(*args):
    return subprocess.run(["docker", *args], capture_output=True, text=True)


def die(msg):
    print("HARNESS ERROR: " + msg)
    teardown()
    sys.exit(2)


def teardown():
    for name in CONTAINERS:
        docker("rm", "-f", name)
    del CONTAINERS[:]
    for net in NETWORKS:
        docker("network", "rm", net)
    del NETWORKS[:]


def free_port():
    sock = socket.socket()
    sock.bind(("127.0.0.1", 0))
    port = sock.getsockname()[1]
    sock.close()
    return port


def check(label, ok, detail=""):
    CHECKS.append((label, bool(ok)))
    print(("PASS  " if ok else "FAIL  ") + label + ((" | " + detail) if detail else ""))


def finish():
    teardown()
    failed = [c for c in CHECKS if not c[1]]
    print("")
    print("%d checks, %d failed" % (len(CHECKS), len(failed)))
    print("RESULT: " + ("FAIL" if failed else "PASS"))
    sys.exit(1 if failed else 0)


def tempdir():
    path = tempfile.mkdtemp()
    os.chmod(path, 0o777)
    return path


def running(name):
    return docker("inspect", "-f", "{{.State.Running}}", name).stdout.strip() == "true"


def logs(name):
    res = docker("logs", name)
    return res.stdout + res.stderr


def run_detached(name, image, env=None, publish=None, mounts=None, network=None):
    docker("rm", "-f", name)
    args = ["run", "-d", "--name", name]
    if network:
        args += ["--network", network]
    for host_port, guest_port in (publish or []):
        args += ["-p", "127.0.0.1:%d:%d" % (host_port, guest_port)]
    for host, guest in (mounts or []):
        args += ["-v", "%s:%s" % (host, guest)]
    for key, value in (env or {}).items():
        args += ["-e", "%s=%s" % (key, value)]
    args.append(image)
    res = docker(*args)
    CONTAINERS.append(name)
    if res.returncode != 0:
        die("docker run %s failed: %s" % (name, res.stderr.strip()))
    return name


def await_tcp(name, port):
    while True:
        if not running(name):
            die("container %s exited before listening:\n%s" % (name, logs(name)[-1500:]))
        sock = socket.socket()
        try:
            sock.connect(("127.0.0.1", port))
            sock.close()
            return
        except Exception:
            time.sleep(0.15)


def fetch(port, path):
    url = "http://127.0.0.1:%d%s" % (port, path)
    try:
        with urllib.request.urlopen(url, timeout=30) as resp:
            return resp.status, resp.read().decode("utf-8", "replace")
    except urllib.error.HTTPError as exc:
        return exc.code, exc.read().decode("utf-8", "replace")
    except Exception as exc:
        return 0, str(exc)


if docker("image", "inspect", ALL_IN_ONE).returncode != 0:
    die("image %s is not present locally; build it with: make core-images service-images" % ALL_IN_ONE)
docker("network", "rm", NET)
if docker("network", "create", NET).returncode != 0:
    die("docker network create failed")
NETWORKS.append(NET)

print("== the core container's two HTTP listeners ==")
api_port = free_port()
callback_port = free_port()
core = run_detached("exp-assumption-health-core", ALL_IN_ONE, network=NET,
                    publish=[(api_port, 8080), (callback_port, 9100)])
while fetch(api_port, "/v1/health")[0] != 200:
    if not running(core):
        die("all-in-one exited during boot:\n" + logs(core)[-1500:])
    time.sleep(0.3)
await_tcp(core, callback_port)
status, body = fetch(callback_port, "/health")
check("the supervisor's callback listener answers GET /health",
      status == 200, "status %s, body %r" % (status, body.strip()[:40]))
status, _ = fetch(api_port, "/health")
check("the control API does not: its probe is GET /v1/health", status == 404,
      "GET /health -> %s, GET /v1/health -> %s" % (status, fetch(api_port, "/v1/health")[0]))

print("")
print("== the eleven bundled services ==")
run_detached(PG, "postgres:16-alpine", network=NET,
             env={"POSTGRES_USER": "rimsky", "POSTGRES_PASSWORD": "rimsky", "POSTGRES_DB": "rimsky"})
while docker("exec", PG, "pg_isready", "-h", PG, "-U", "rimsky").returncode != 0:
    if not running(PG):
        die("postgres exited:\n" + logs(PG)[-1000:])
    time.sleep(0.3)

cfgdir = tempdir()
datadir = tempdir()
os.makedirs(os.path.join(datadir, "work"), exist_ok=True)
os.chmod(os.path.join(datadir, "work"), 0o777)
with open(os.path.join(cfgdir, "fs.yml"), "w") as fh:
    fh.write(FS_YML)
with open(os.path.join(cfgdir, "pg.yml"), "w") as fh:
    fh.write(PG_YML)
places = {"CFG": cfgdir, "DATA": datadir}

started = []
for service, image, guest_ports, env, mounts in SERVICES:
    full = image + ":" + TAG
    if docker("image", "inspect", full).returncode != 0:
        die("image %s is not present locally; build it with: make service-images" % full)
    ports = {}
    publish = []
    for guest in guest_ports:
        host = free_port()
        ports[guest] = host
        publish.append((host, guest))
    name = "exp-assumption-health-" + service
    run_detached(name, full, env=dict(env), publish=publish,
                 mounts=[(places[a], b) for a, b in mounts], network=NET)
    started.append((service, name, guest_ports, ports))

answering = []
for service, name, guest_ports, ports in started:
    await_tcp(name, ports[guest_ports[0]])
    for guest, host in sorted(ports.items()):
        if fetch(host, "/health")[0] == 200:
            answering.append("%s:%d" % (service, guest))
check("exactly one of the ten services with listeners answers GET /health",
      answering == ["sensor-webhook:9184"], "answering: %s" % answering)

claude_bridge = dict(started[7][3])[9190]
status_health, _ = fetch(claude_bridge, "/health")
status_healthz, _ = fetch(claude_bridge, "/healthz")
check("the claude-agent bridge spells its probe /healthz, and /health is a 404",
      status_health == 404 and status_healthz == 200,
      "/health -> %s, /healthz -> %s" % (status_health, status_healthz))

ol_image = "rimsky-subscriber-openlineage:" + TAG
if docker("image", "inspect", ol_image).returncode != 0:
    die("image %s is not present locally; build it with: make service-images" % ol_image)
ol_probe = free_port()
ol = run_detached("exp-assumption-health-openlineage", ol_image, network=NET,
                  publish=[(ol_probe, 8080)],
                  env={"RIMSKY_OPENLINEAGE_RIMSKY_DSN":
                           "postgres://rimsky:rimsky@%s:5432/rimsky?sslmode=disable" % PG,
                       "RIMSKY_OPENLINEAGE_BACKEND_URL": "http://127.0.0.1:9999"})
while True:
    res = docker("exec", PG, "psql", "-U", "rimsky", "-d", "rimsky", "-tAc",
                 "select to_regclass('rimsky_openlineage_cursor') is not null")
    if res.stdout.strip() == "t":
        break
    if not running(ol):
        die("openlineage exited before reaching its store:\n" + logs(ol)[-1500:])
    time.sleep(0.3)
check("the openlineage subscriber serves no HTTP at all", fetch(ol_probe, "/health")[0] == 0,
      "the probe was refused; the image declares no port")

print("")
print("== the host-agent proxy image ==")
proxy_port = free_port()
proxy = run_detached("exp-assumption-health-proxy", PROXY, network=NET,
                     publish=[(proxy_port, 9090)],
                     env={"RIMSKY_CONTROL_API_URL": "http://exp-assumption-health-core:8080"})
await_tcp(proxy, proxy_port)
check("the proxy's agent-facing port answers no GET /health",
      fetch(proxy_port, "/health")[0] != 200,
      "status %s on a gRPC-only listener" % fetch(proxy_port, "/health")[0])

finish()
