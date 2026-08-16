import json
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
ROOT = os.path.abspath(os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", ".."))
CLI = os.path.join(ROOT, "bin", "rimsky")
ALL_IN_ONE = "rimsky-all-in-one:" + TAG
PROXY = "rimsky-host-agent-proxy:" + TAG
NET = "exp-assumption-log-level-net"
PG = "exp-assumption-log-level-pg"
CHECKS = []
CONTAINERS = []
NETWORKS = []

RIMSKY_YML = ("persistence:\n"
              "  driver: sqlite\n"
              "  sqlite:\n"
              "    path: /var/lib/rimsky/state.db\n"
              "claim_producers: {}\n"
              "named_locks: {}\n"
              "executors:\n"
              "  foo:\n"
              "    transport: grpc\n"
              "    endpoint: 127.0.0.1:9999\n")

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


def write(path, name, body):
    full = os.path.join(path, name)
    with open(full, "w") as fh:
        fh.write(body)
    return full


def logs(name):
    res = docker("logs", name)
    return res.stdout + res.stderr


def level_lines(text, level):
    return [l for l in text.splitlines() if ('"level":"%s"' % level) in l]


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


def running(name):
    return docker("inspect", "-f", "{{.State.Running}}", name).stdout.strip() == "true"


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


def get(base, path):
    try:
        with urllib.request.urlopen(base + path) as resp:
            raw = resp.read().decode()
            return resp.status, (json.loads(raw) if raw else None)
    except urllib.error.HTTPError as exc:
        return exc.code, exc.read().decode()
    except Exception as exc:
        return 0, str(exc)


def probe_stamp(base):
    status, payload = get(base, "/v1/observability/executors")
    if status != 200:
        return None
    for entry in payload["executors"]:
        if entry["name"] == "foo":
            return entry["last_probed_at"]
    return None


def boot_core(name, level, cfg):
    env = {"RIMSKY_OBSERVABILITY_REFRESH_INTERVAL": "1s"}
    if level is not None:
        env["RIMSKY_LOG_LEVEL"] = level
    port = free_port()
    run_detached(name, ALL_IN_ONE, env=env, publish=[(port, 8080)],
                 mounts=[(cfg, "/etc/rimsky/rimsky.yml")], network=NET)
    base = "http://127.0.0.1:%d" % port
    while True:
        if not running(name):
            die("container %s exited during boot:\n%s" % (name, logs(name)[-1500:]))
        if get(base, "/v1/health")[0] == 200:
            break
        time.sleep(0.3)
    first = probe_stamp(base)
    while probe_stamp(base) == first:
        if not running(name):
            die("container %s exited while probing:\n%s" % (name, logs(name)[-1500:]))
        time.sleep(0.2)
    return base


if docker("image", "inspect", ALL_IN_ONE).returncode != 0:
    die("image %s is not present locally; build it with: make core-images service-images" % ALL_IN_ONE)
docker("network", "rm", NET)
if docker("network", "create", NET).returncode != 0:
    die("docker network create failed")
NETWORKS.append(NET)
cfgdir = tempdir()
cfg = write(cfgdir, "rimsky.yml", RIMSKY_YML)

print("== the core process, at each level ==")
base = boot_core("exp-assumption-log-level-core-default", None, cfg)
default_log = logs("exp-assumption-log-level-core-default")
subjects = {
    "the entrypoint": '"binary":"entrypoint"',
    "the migrate step": '"binary":"migrate"',
    "the scheduler role": '"role":"scheduler"',
    "the supervisor role": '"role":"supervisor"',
    "the control-api role": '"role":"control-api"',
}
missing = [name for name, marker in subjects.items()
           if not [l for l in level_lines(default_log, "INFO") if marker in l]]
check("at the default level all five core subjects log at INFO",
      not missing, "silent: %s" % missing)
check("and none of them logs at DEBUG",
      not level_lines(default_log, "DEBUG"), "%d DEBUG lines" % len(level_lines(default_log, "DEBUG")))

boot_core("exp-assumption-log-level-core-error", "error", cfg)
error_log = logs("exp-assumption-log-level-core-error")
check("RIMSKY_LOG_LEVEL=error silences every INFO and WARN line of the core process",
      not level_lines(error_log, "INFO") and not level_lines(error_log, "WARN"),
      "%d INFO, %d WARN" % (len(level_lines(error_log, "INFO")), len(level_lines(error_log, "WARN"))))

boot_core("exp-assumption-log-level-core-debug", "debug", cfg)
debug_log = logs("exp-assumption-log-level-core-debug")
check("RIMSKY_LOG_LEVEL=debug adds DEBUG lines the default level withholds",
      level_lines(debug_log, "DEBUG"), "%d DEBUG lines" % len(level_lines(debug_log, "DEBUG")))

for value in ("DEBUG", "trace"):
    name = "exp-assumption-log-level-core-" + value.lower() + "-word"
    boot_core(name, value, cfg)
    text = logs(name)
    check("RIMSKY_LOG_LEVEL=%s falls back to the default level without saying so" % value,
          level_lines(text, "INFO") and not level_lines(text, "DEBUG") and value not in text,
          "%d INFO, %d DEBUG, value named: %s"
          % (len(level_lines(text, "INFO")), len(level_lines(text, "DEBUG")), value in text))

print("")
print("== the host-agent proxy image ==")
proxy_port = free_port()
run_detached("exp-assumption-log-level-proxy", PROXY, network=NET,
             publish=[(proxy_port, 9090)],
             env={"RIMSKY_LOG_LEVEL": "error",
                  "RIMSKY_CONTROL_API_URL": "http://exp-assumption-log-level-core-default:8080"})
await_tcp("exp-assumption-log-level-proxy", proxy_port)
proxy_log = logs("exp-assumption-log-level-proxy")
check("the proxy honors RIMSKY_LOG_LEVEL=error once it is listening",
      not level_lines(proxy_log, "INFO"),
      "%d INFO lines" % len(level_lines(proxy_log, "INFO")))

print("")
print("== the host agent, started through the CLI verb ==")
if not os.path.exists(CLI):
    die("bin/rimsky is not built; build it with: make cli")
agent_port = free_port()
agent_env = dict(os.environ)
agent_env.update({"RIMSKY_LOG_LEVEL": "error",
                  "RIMSKY_AGENT_LISTEN": "127.0.0.1:%d" % agent_port,
                  "RIMSKY_HOST_AGENT_PROXY_URL": "127.0.0.1:%d" % free_port()})
agent_out = os.path.join(tempdir(), "agent.log")
with open(agent_out, "w") as fh:
    agent = subprocess.Popen([CLI, "agent", "start", "--foreground"],
                             stdout=fh, stderr=subprocess.STDOUT, env=agent_env)
while True:
    sock = socket.socket()
    try:
        sock.connect(("127.0.0.1", agent_port))
        sock.close()
        break
    except Exception:
        if agent.poll() is not None:
            die("rimsky agent start exited before listening:\n" + open(agent_out).read())
        time.sleep(0.15)
agent.terminate()
agent.wait()
agent_log = open(agent_out).read()
check("the agent started by `rimsky agent start` still logs INFO at RIMSKY_LOG_LEVEL=error",
      " INFO " in agent_log,
      next((l.strip()[:110] for l in agent_log.splitlines() if " INFO " in l), ""))
check("and its lines are plain text, not the JSON the core roles emit",
      '"level":"INFO"' not in agent_log, "no JSON records in the agent's output")

print("")
print("== the eleven bundled services, all at RIMSKY_LOG_LEVEL=error ==")
run_detached(PG, "postgres:16-alpine", network=NET,
             env={"POSTGRES_USER": "rimsky", "POSTGRES_PASSWORD": "rimsky",
                  "POSTGRES_DB": "rimsky"})
while docker("exec", PG, "pg_isready", "-h", PG, "-U", "rimsky").returncode != 0:
    if not running(PG):
        die("postgres exited:\n" + logs(PG)[-1000:])
    time.sleep(0.3)

svcdir = tempdir()
datadir = tempdir()
os.makedirs(os.path.join(datadir, "work"), exist_ok=True)
os.chmod(os.path.join(datadir, "work"), 0o777)
fs_cfg = write(svcdir, "fs.yml", FS_YML)
pg_cfg = write(svcdir, "pg.yml", PG_YML)

listeners = [
    ("sensor-cron", "rimsky-sensor-cron", 9081, {}, []),
    ("sensor-http", "rimsky-sensor-http", 9082, {}, []),
    ("sensor-object-store", "rimsky-sensor-object-store", 9083, {}, []),
    ("sensor-webhook", "rimsky-sensor-webhook", 9084, {}, []),
    ("http-node", "rimsky-executor-http-node", 9091, {}, []),
    ("verifier-http", "rimsky-executor-verifier-http", 9096, {}, []),
    ("verifier-shape-checks", "rimsky-executor-verifier-shape-checks", 9095, {}, []),
    ("claude-agent", "rimsky-executor-claude-agent", 9090,
     {"ANTHROPIC_API_KEY": "unused-by-this-run", "RIMSKY_EXECUTOR_STUB_MODE": "1"}, []),
    ("claim-producer-filesystem", "rimsky-claim-producer-filesystem", 9100,
     {"RIMSKY_CLAIM_PRODUCER_FILESYSTEM_CONFIG": "/cfg/fs.yml"},
     [(svcdir, "/cfg"), (datadir, "/data")]),
    ("claim-producer-postgres", "rimsky-claim-producer-postgres", 9101,
     {"RIMSKY_CLAIM_PRODUCER_POSTGRES_CONFIG": "/cfg/pg.yml"}, [(svcdir, "/cfg")]),
]

started = []
for service, image, guest_port, env, mounts in listeners:
    full = image + ":" + TAG
    if docker("image", "inspect", full).returncode != 0:
        die("image %s is not present locally; build it with: make service-images" % full)
    host_port = free_port()
    env = dict(env)
    env["RIMSKY_LOG_LEVEL"] = "error"
    name = "exp-assumption-log-level-" + service
    run_detached(name, full, env=env, publish=[(host_port, guest_port)],
                 mounts=mounts, network=NET)
    started.append((service, name, host_port))

ignoring = []
for service, name, host_port in started:
    await_tcp(name, host_port)
    text = logs(name)
    if level_lines(text, "INFO"):
        ignoring.append(service)
check("ten of the eleven services log at INFO after being told error",
      len(ignoring) == 10, "ignoring the level: %s" % sorted(ignoring))

ol_image = "rimsky-subscriber-openlineage:" + TAG
if docker("image", "inspect", ol_image).returncode != 0:
    die("image %s is not present locally; build it with: make service-images" % ol_image)
ol = run_detached("exp-assumption-log-level-openlineage", ol_image, network=NET,
                  env={"RIMSKY_LOG_LEVEL": "error",
                       "RIMSKY_OPENLINEAGE_RIMSKY_DSN":
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
docker("stop", ol)
ol_log = logs(ol)
check("the eleventh service logs at INFO too",
      level_lines(ol_log, "INFO"),
      next((l[:110] for l in level_lines(ol_log, "INFO")), ""))

finish()
