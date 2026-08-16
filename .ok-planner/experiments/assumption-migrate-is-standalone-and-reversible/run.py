import os
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
import uuid

ROOT = os.path.abspath(os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", ".."))
CLI = os.path.join(ROOT, "bin", "rimsky")
TAG = os.environ.get("RIMSKY_IMAGE_TAG")
if not TAG:
    print("HARNESS ERROR: export RIMSKY_IMAGE_TAG=src-<tree hash> first")
    sys.exit(2)
IMAGE = "rimsky-all-in-one:" + TAG
SUFFIX = uuid.uuid4().hex[:6]
CHECKS = []
CONTAINERS = []


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
    print(("PASS  " if ok else "FAIL  ") + label + ((" | " + str(detail)[:300]) if detail else ""))


def finish():
    teardown()
    failed = [c for c in CHECKS if not c[1]]
    print("")
    print("%d checks, %d failed" % (len(CHECKS), len(failed)))
    print("RESULT: " + ("FAIL" if failed else "PASS"))
    sys.exit(1 if failed else 0)


def logs(name):
    res = docker("logs", name)
    return res.stdout + res.stderr


def running(name):
    return docker("inspect", "-f", "{{.State.Running}}", name).stdout.strip() == "true"


def health(port):
    try:
        with urllib.request.urlopen("http://127.0.0.1:%d/v1/health" % port, timeout=30) as resp:
            return resp.status
    except urllib.error.HTTPError as exc:
        return exc.code
    except Exception:
        return 0


def run_to_exit(name, env=None, command=None, mounts=None):
    docker("rm", "-f", name)
    args = ["run", "--name", name]
    for host, guest in (mounts or []):
        args += ["-v", "%s:%s" % (host, guest)]
    for key, value in (env or {}).items():
        args += ["-e", "%s=%s" % (key, value)]
    args.append(IMAGE)
    args += command or []
    res = docker(*args)
    CONTAINERS.append(name)
    return res.returncode, res.stdout + res.stderr


def boot(name, state_dir, env=None, command=None):
    docker("rm", "-f", name)
    port = free_port()
    args = ["run", "-d", "--name", name, "-p", "127.0.0.1:%d:8080" % port,
            "-v", "%s:/var/lib/rimsky" % state_dir]
    for key, value in (env or {}).items():
        args += ["-e", "%s=%s" % (key, value)]
    args.append(IMAGE)
    args += command or []
    res = docker(*args)
    CONTAINERS.append(name)
    if res.returncode != 0:
        die("docker run failed: " + res.stderr.strip())
    while True:
        if not running(name):
            die("container %s exited during boot:\n%s" % (name, logs(name)[-1500:]))
        if health(port) == 200:
            return port
        time.sleep(0.3)


if docker("image", "inspect", IMAGE).returncode != 0:
    die("image %s is not present locally; build it with: make core-images" % IMAGE)

print("== asking the image to run the migrate binary on its own ==")
code, text = run_to_exit("exp-assumption-migrate-standalone-" + SUFFIX,
                         command=["rimsky-migrate"])
check("the image refuses to run rimsky-migrate as its command", code != 0, "exit %d" % code)
check("and the refusal names the commands it does accept",
      "rimsky-migrate" in text and "rimsky-scheduler" in text and "rimsky-control-api" in text,
      next((l.strip()[:200] for l in text.splitlines() if "rimsky-migrate" in l), text[:200]))

print("")
print("== the only way to run it: as a step inside a role container ==")
state = tempfile.mkdtemp()
os.chmod(state, 0o777)
first = "exp-assumption-migrate-first-" + SUFFIX
boot(first, state, env={"RIMSKY_ENTRYPOINT_MIGRATE": "1"}, command=["rimsky-control-api"])
applied_first = logs(first).count('"msg":"migration applied"')
check("forcing the migrate step still leaves a long-running role behind, not a finished job",
      running(first) and applied_first > 0,
      "%d migrations applied, container still running" % applied_first)

print("")
print("== running it again over the same database ==")
docker("stop", first)
second = "exp-assumption-migrate-second-" + SUFFIX
port = boot(second, state, env={"RIMSKY_ENTRYPOINT_MIGRATE": "1"}, command=["rimsky-control-api"])
applied_second = logs(second).count('"msg":"migration applied"')
check("a second forced run over the same database applies nothing and comes up healthy",
      applied_second == 0 and health(port) == 200,
      "first run applied %d, second applied %d" % (applied_first, applied_second))

print("")
print("== asking for a way back down ==")
code, text = run_to_exit("exp-assumption-migrate-down-" + SUFFIX,
                         env={"RIMSKY_ENTRYPOINT_MIGRATE": "down"},
                         command=["rimsky-control-api"])
check("the migrate switch takes no down value", code != 0 and "down" in text,
      next((l.strip()[-180:] for l in text.splitlines() if "ENTRYPOINT_MIGRATE" in l), text[:180]))
if not os.path.exists(CLI):
    die("bin/rimsky is not built; build it with: make cli")
res = subprocess.run([CLI, "migrate"], capture_output=True, text=True)
check("the CLI has no migrate verb to roll back with", res.returncode != 0,
      (res.stdout + res.stderr).strip().splitlines()[0][:180] if (res.stdout + res.stderr).strip() else "")
res = subprocess.run([CLI, "--help"], capture_output=True, text=True)
check("and its help offers no migration command at all",
      "migrate" not in (res.stdout + res.stderr),
      "the word does not appear in the CLI's own help")

finish()
