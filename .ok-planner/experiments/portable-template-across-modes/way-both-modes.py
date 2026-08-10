import hashlib
import json
import os
import subprocess
import sys
import tempfile

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from harness import (RIMSKY, TAG, boot, call, check, docker, finish, free_port,  # noqa: E402
                     new_network, require_image, run_container, teardown, wait_healthy,
                     wait_until)

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.abspath(os.path.join(HERE, "..", "..", ".."))
CLI = os.path.join(ROOT, "bin", "rimsky")
TEMPLATE = os.path.join(HERE, "template.yml")
SUBNET = "172.31.96.0/24"
EXECUTOR_IMAGE = "rimsky-executor-verifier-shape-checks:" + TAG

SPLIT_CONFIG = """persistence:
  driver: postgres
  postgres:
    dsn: postgres://rimsky:rimsky@%s:5432/rimsky?sslmode=disable
claim_producers: {}
named_locks: {}
executors:
  "verifier-shape-checks":
    transport: "grpc"
    endpoint: "%s:9095"
    protocols: ["executor"]
"""

SUPERVISOR_CONFIG = """concurrency: 4
claim_poll_interval_ms: 200
callback:
  host: 0.0.0.0
  port: 9100
  advertise_host: 127.0.0.1
"""

ROLE_ENV = {"RIMSKY_CONTROL_API_HOST": "0.0.0.0",
            "RIMSKY_SUPERVISOR_CALLBACK_ADVERTISE_HOST": "127.0.0.1"}



def write_supervisor_config():
    path = os.path.join(tempfile.mkdtemp(), "supervisor-config.yml")
    with open(path, "w") as fh:
        fh.write(SUPERVISOR_CONFIG)
    return path


SUPERVISOR_MOUNT = None


def role_mounts(config):
    global SUPERVISOR_MOUNT
    if SUPERVISOR_MOUNT is None:
        SUPERVISOR_MOUNT = write_supervisor_config()
    return [(config, "/etc/rimsky/rimsky.yml"),
            (SUPERVISOR_MOUNT, "/etc/rimsky/supervisor-config.yml")]


def cli(*args):
    res = subprocess.run([CLI, *args], capture_output=True, text=True)
    return res.returncode, res.stdout + res.stderr


def digest():
    with open(TEMPLATE, "rb") as fh:
        return hashlib.sha256(fh.read()).hexdigest()


def run_template(base, label):
    rc, out = cli("run", "--endpoint", base, "--instance-key", "portable-" + label, TEMPLATE)
    if rc != 0:
        return None, out
    iid = [ln.split("=", 1)[1].strip() for ln in out.splitlines()
           if ln.startswith("instance_id=")]
    return (iid[0] if iid else None), out


def settle(base, iid):
    rc, out = cli("watch", "--endpoint", base, "--poll-interval", "250ms", iid)
    return rc, out


def template_hash(base, iid):
    return call("GET", "/v1/instances/%s" % iid, base=base)[1]["template_hash"]


def start_split(network, pg, executor):
    config = os.path.join(tempfile.mkdtemp(), "rimsky.yml")
    with open(config, "w") as fh:
        fh.write(SPLIT_CONFIG % (pg, executor))
    port = free_port()
    for command, publish in ((["rimsky-control-api"], (port, 8080)),
                             (["rimsky-scheduler"], None),
                             (["rimsky-supervisor"], None)):
        _, res = run_container(RIMSKY, network=network, env=ROLE_ENV, publish=publish,
                               mounts=role_mounts(config), command=command,
                               extra=["--restart", "unless-stopped"])
        if res.returncode != 0:
            raise SystemExit("HARNESS ERROR: docker run failed: " + res.stderr)
    base = "http://127.0.0.1:%d" % port
    wait_healthy(base)
    return base


def main():
    if not os.path.exists(CLI):
        subprocess.run(["make", "-C", ROOT, "cli"], capture_output=True, text=True)
    require_image(EXECUTOR_IMAGE)
    before = digest()

    _, allinone = boot()
    iid_a, out = run_template(allinone, "allinone")
    check("the template file registers, deploys and instantiates on the all-in-one deployment",
          iid_a is not None, out.strip().splitlines()[-1:] and str(out.strip().splitlines()[-1:]))
    rc, out = settle(allinone, iid_a)
    check("the all-in-one run reaches a terminal state", rc == 0, "exit %d" % rc)
    status_a = call("GET", "/v1/instances/%s/nodes" % iid_a, base=allinone)[1]["nodes"]
    check("its node settled fresh on the all-in-one deployment",
          all(n["run_summary"]["fresh_count"] == 1 for n in status_a),
          json.dumps([n["run_summary"] for n in status_a]))

    network = new_network(SUBNET)
    pg, res = run_container("postgres:16-alpine", network=network, env={
        "POSTGRES_USER": "rimsky", "POSTGRES_PASSWORD": "rimsky", "POSTGRES_DB": "rimsky"})
    if res.returncode != 0:
        raise SystemExit("HARNESS ERROR: postgres failed to start: " + res.stderr)
    wait_until(lambda: docker("exec", pg, "pg_isready", "-U", "rimsky").returncode == 0)
    executor, res = run_container(EXECUTOR_IMAGE, network=network)
    if res.returncode != 0:
        raise SystemExit("HARNESS ERROR: executor failed to start: " + res.stderr)
    split = start_split(network, pg, executor)
    check("the multi-container deployment is up as separate role containers",
          call("GET", "/v1/health", base=split)[1].get("status") == "ok", split)

    iid_b, out = run_template(split, "split")
    check("the same file registers, deploys and instantiates on the multi-container deployment",
          iid_b is not None, str(out.strip().splitlines()[-1:]))
    rc, out = settle(split, iid_b)
    check("the multi-container run reaches a terminal state", rc == 0, "exit %d" % rc)
    status_b = call("GET", "/v1/instances/%s/nodes" % iid_b, base=split)[1]["nodes"]
    check("its node settled fresh on the multi-container deployment",
          all(n["run_summary"]["fresh_count"] == 1 for n in status_b),
          json.dumps([n["run_summary"] for n in status_b]))

    check("nothing edited the template file between the two deployments",
          digest() == before, before)
    check("both deployments content-addressed the file to the same template",
          template_hash(allinone, iid_a) == template_hash(split, iid_b),
          "%s vs %s" % (template_hash(allinone, iid_a), template_hash(split, iid_b)))
    finish()


try:
    main()
finally:
    teardown()
