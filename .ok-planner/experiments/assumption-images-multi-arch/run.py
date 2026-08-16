import json
import os
import re
import subprocess
import sys

TAG = os.environ.get("RIMSKY_IMAGE_TAG")
if not TAG:
    print("HARNESS ERROR: export RIMSKY_IMAGE_TAG=src-<tree hash> first")
    sys.exit(2)
REGISTRY = "docker.io/rimskyai"
IMAGES = [
    "rimsky", "rimsky-all-in-one", "rimsky-host-agent-proxy", "rimsky-conformance",
    "rimsky-claim-producer-filesystem", "rimsky-claim-producer-postgres",
    "rimsky-sensor-cron", "rimsky-sensor-http", "rimsky-sensor-object-store",
    "rimsky-sensor-webhook", "rimsky-subscriber-openlineage",
    "rimsky-executor-http-node", "rimsky-executor-verifier-http",
    "rimsky-executor-verifier-shape-checks", "rimsky-executor-claude-agent",
]
CHECKS = []


def die(msg):
    print("HARNESS ERROR: " + msg)
    sys.exit(2)


def check(label, ok, detail=""):
    CHECKS.append((label, bool(ok)))
    print(("PASS  " if ok else "FAIL  ") + label + ((" | " + str(detail)[:320]) if detail else ""))


def finish():
    failed = [c for c in CHECKS if not c[1]]
    print("")
    print("%d checks, %d failed" % (len(CHECKS), len(failed)))
    print("RESULT: " + ("FAIL" if failed else "PASS"))
    sys.exit(1 if failed else 0)


def docker(*args):
    return subprocess.run(["docker", *args], capture_output=True, text=True)


def published_platforms(image, tag):
    res = docker("buildx", "imagetools", "inspect", "%s/%s:%s" % (REGISTRY, image, tag))
    if res.returncode != 0:
        return None, (res.stdout + res.stderr).strip()
    found = re.findall(r"Platform:\s+(\S+)", res.stdout)
    return sorted(set(p for p in found if not p.startswith("unknown"))), ""


def local_platform(image):
    res = docker("image", "inspect", "%s:%s" % (image, TAG),
                 "--format", "{{.Os}}/{{.Architecture}}")
    if res.returncode != 0:
        return None
    return res.stdout.strip()


print("== the published images on the registry the release pushes to ==")
platforms = {}
for image in IMAGES:
    found, err = published_platforms(image, "latest")
    if found is None:
        die("cannot read the published manifest for %s: %s" % (image, err[:200]))
    platforms[image] = found
    print("    %-42s %s" % (image, ",".join(found)))

both = sorted(i for i, p in platforms.items() if "linux/amd64" in p and "linux/arm64" in p)
arm_only = sorted(i for i, p in platforms.items() if p == ["linux/arm64"])
check("all fifteen published images were readable", len(platforms) == 15,
      "%d manifests inspected" % len(platforms))
check("not one of the fifteen carries both architectures", not both,
      "multi-architecture images: %s" % both)
check("every one of the fifteen is arm64 and only arm64", len(arm_only) == 15,
      "arm64-only: %d of %d" % (len(arm_only), len(IMAGES)))

print("")
print("== the images this tree builds ==")
locals_ = {image: local_platform(image) for image in IMAGES}
missing = [i for i, p in locals_.items() if p is None]
if missing:
    die("images not present locally: %s; build them with: make core-images service-images"
        % missing)
single = sorted(set(locals_.values()))
check("a local build produces one architecture per image, the builder's own",
      len(single) == 1, "architectures built: %s" % single)

finish()
