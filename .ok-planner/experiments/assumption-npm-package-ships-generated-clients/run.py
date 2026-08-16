import json
import os
import subprocess
import sys
import tarfile
import tempfile

ROOT = os.path.abspath(os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", ".."))
PACKAGE_DIR = os.path.join(ROOT, "lib", "protocols")
PACKAGE = "@rimsky-ai/protocols"
CHECKS = []
STUB_MARKERS = ("_pb.ts", "_pb.js", "_pb.d.ts", "_grpc_pb", ".client.ts", "connect", "generated")


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


def npm(*args, **kwargs):
    return subprocess.run(["npm", *args], capture_output=True, text=True, **kwargs)


def entries_of(tarball):
    with tarfile.open(tarball) as tar:
        return sorted(name[len("package/"):] for name in tar.getnames()
                      if name.startswith("package/") and name != "package/")


if npm("--version").returncode != 0:
    die("npm is not available on PATH")

print("== the package this tree would publish ==")
workdir = tempfile.mkdtemp()
res = npm("pack", PACKAGE_DIR, "--pack-destination", workdir)
if res.returncode != 0:
    die("npm pack of the tree's package failed: " + (res.stdout + res.stderr)[-300:])
tarball = os.path.join(workdir, sorted(os.listdir(workdir))[0])
local_entries = entries_of(tarball)
print("    " + "\n    ".join(local_entries))
protos = [e for e in local_entries if e.endswith(".proto")]
code = [e for e in local_entries if e.endswith((".js", ".ts", ".mjs", ".cjs"))]
check("the package carries the ten wire-protocol definitions", len(protos) == 10,
      "%d .proto files" % len(protos))
check("and exactly two code files, the path helpers", sorted(code) == ["index.d.ts", "index.js"],
      "code files: %s" % sorted(code))
stubs = [e for e in local_entries if any(marker in e for marker in STUB_MARKERS)]
check("no generated stub of any kind is shipped", not stubs, "stub-looking entries: %s" % stubs)

print("")
print("== what the two code files export ==")
declared = open(os.path.join(PACKAGE_DIR, "index.d.ts")).read()
check("the whole typed surface is a directory and a path helper",
      "protoDir" in declared and "protoPath" in declared
      and declared.count("export") == 2,
      "%d exports declared" % declared.count("export"))

print("")
print("== the package as published ==")
pubdir = tempfile.mkdtemp()
res = npm("pack", PACKAGE + "@latest", "--pack-destination", pubdir)
if res.returncode != 0:
    die("npm pack of the published package failed: " + (res.stdout + res.stderr)[-300:])
published_tarball = os.path.join(pubdir, sorted(os.listdir(pubdir))[0])
published_entries = entries_of(published_tarball)
check("the published package carries the same shape",
      sorted(e for e in published_entries if e.endswith((".js", ".ts")))
      == ["index.d.ts", "index.js"],
      "published code files: %s" % sorted(e for e in published_entries if e.endswith((".js", ".ts"))))
check("and no generated stub either",
      not [e for e in published_entries if any(m in e for m in STUB_MARKERS)],
      "%d entries, %d of them .proto"
      % (len(published_entries), len([e for e in published_entries if e.endswith(".proto")])))

print("")
print("== what a consumer would have to do instead ==")
res = subprocess.run(["node", "-e",
                      "import('%s/index.js').then(m => console.log(JSON.stringify(Object.keys(m))))"
                      % PACKAGE_DIR.replace("'", "")],
                     capture_output=True, text=True)
if res.returncode != 0:
    die("could not import the package's entry point: " + (res.stdout + res.stderr)[-300:])
exported = json.loads(res.stdout.strip())
check("importing the package hands back a proto directory to feed a loader, and nothing callable",
      sorted(exported) == ["protoDir", "protoPath"], "exports: %s" % sorted(exported))

finish()
