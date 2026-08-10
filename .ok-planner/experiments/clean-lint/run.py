import json
import os
import pathlib
import shutil
import subprocess
import sys
import tempfile

REPO = pathlib.Path(__file__).resolve().parents[3]
LINT = REPO / ".ok-plumbline" / "bin" / "plumbline"
CONFIG = REPO / ".ok-plumbline" / "config.json"
CHECKS = []


def check(label, ok, detail=""):
    CHECKS.append((label, bool(ok)))
    print(("PASS  " if ok else "FAIL  ") + label + ((" | " + detail) if detail else ""))


def die(msg):
    print("HARNESS ERROR: " + msg)
    sys.exit(2)


def run_lint(target, cwd):
    return subprocess.run([str(LINT), target], cwd=str(cwd), capture_output=True, text=True)


def fixture(name, filename, contents):
    root = pathlib.Path(tempfile.mkdtemp(prefix="exp-clean-lint-"))
    (root / ".ok-plumbline").mkdir()
    shutil.copy(CONFIG, root / ".ok-plumbline" / "config.json")
    (root / ".ok-planner" / "design" / "concepts").mkdir(parents=True)
    (root / ".ok-planner" / "design" / "concepts" / "node.md").write_text("# node\n")
    (root / filename).write_text(contents)
    return root


def main():
    if shutil.which("node") is None:
        die("the plumbline lint is a node script; node is required to run it")
    if not LINT.exists():
        die("the vendored lint binary is missing at %s" % LINT)

    print("  leg 1: the instrument the project ships")
    check("the repository carries the lint binary a maintainer runs", os.access(LINT, os.X_OK), str(LINT))
    config = json.loads(CONFIG.read_text())
    disabled = [name for name, on in (config.get("checks") or {}).items() if not on]
    check("no check is switched off in the project's lint configuration", not disabled, json.dumps(disabled))
    check("the configuration declares the project's citation tags",
          [c["tag"] for c in config.get("citations") or []] != [],
          json.dumps([c["tag"] for c in config.get("citations") or []]))

    print("  leg 2: the whole tree under the shipped configuration")
    res = run_lint(".", REPO)
    check("the lint reports no violation anywhere in the tree", res.returncode == 0,
          "exit %d\n%s" % (res.returncode, (res.stdout + res.stderr)[:800]))

    print("  leg 3: each check is live, not silently inert")
    comment_fixture = fixture("comment", "sample.go", "package main\n\n// a prose comment nobody asked for\nfunc main() {}\n")
    res = run_lint(".", comment_fixture)
    check("a stray comment is reported by comment-hygiene under this project's configuration",
          res.returncode == 2 and "comment-hygiene" in res.stdout,
          "exit %d %s" % (res.returncode, res.stdout[:300]))
    shutil.rmtree(comment_fixture, ignore_errors=True)

    citation_fixture = fixture("citation", "sample.go", "package main\n\n// @concept: no-such-concept\nfunc main() {}\n")
    res = run_lint(".", citation_fixture)
    check("an unresolvable citation is reported by citation-resolution under this project's configuration",
          res.returncode == 2 and "citation-unresolved" in res.stdout,
          "exit %d %s" % (res.returncode, res.stdout[:300]))
    shutil.rmtree(citation_fixture, ignore_errors=True)

    resolved_fixture = fixture("resolved", "sample.go", "package main\n\n// @concept: node\nfunc main() {}\n")
    res = run_lint(".", resolved_fixture)
    check("a citation that resolves is accepted, so the check discriminates rather than always failing",
          res.returncode == 0, "exit %d %s" % (res.returncode, res.stdout[:300]))
    shutil.rmtree(resolved_fixture, ignore_errors=True)

    failed = [c for c in CHECKS if not c[1]]
    print("")
    print("%d checks, %d failed" % (len(CHECKS), len(failed)))
    sys.exit(1 if failed else 0)


main()
