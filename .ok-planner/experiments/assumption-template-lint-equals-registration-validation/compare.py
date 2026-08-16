"""Compare what `template lint` says against what `template register` rejects.

Usage: compare.py <rimsky-bin> <work-dir>

Each case is a template with one deliberate defect. Both verbs run against the
same live deployment; the probe extracts the set of finding paths each one
reports and requires them to match. A case where registration rejects and lint
does not is the failure the prior is about; the reverse is recorded too.
"""

import json
import os
import re
import subprocess
import sys

BIN, WORK = sys.argv[1], sys.argv[2]

BASE = 'version: "1"\n'
CASES = {
    "undeclared executor": 'nodes:\n  - type: a\n    executor: no-such-executor\n',
    "undeclared claim producer": (
        'nodes:\n  - type: a\n    executor: verifier-shape-checks\n'
        '    claim_producers:\n      - name: no-such-producer\n        selector: x\n'
        '        intent: read\n        alias: c\n'),
    "duplicate node type": (
        'nodes:\n  - type: a\n    executor: verifier-shape-checks\n'
        '  - type: a\n    executor: verifier-shape-checks\n'),
    "dangling subscribe": (
        'nodes:\n  - type: a\n    executor: verifier-shape-checks\n'
        '    subscribes:\n      - node: nonexistent\n        type: fresh\n'),
    "uncompilable params_schema": (
        'params_schema:\n  type: not-a-json-schema-type\n'
        'nodes:\n  - type: a\n    executor: verifier-shape-checks\n'),
    "undeclared sent message": (
        'nodes:\n  - type: a\n    executor: verifier-shape-checks\n'
        '    sends_message: never-declared\n'),
    "out-of-grammar duration": (
        'nodes:\n  - type: a\n    executor: verifier-shape-checks\n    max_runtime: 30d\n'),
    "unknown top-level key": (
        'totally_unknown_key: 3\nnodes:\n  - type: a\n    executor: verifier-shape-checks\n'),
    "dangling graph reference": (
        'nodes:\n  - type: a\n    executor: verifier-shape-checks\n'
        'graphs:\n  - name: g\n    entry: nope\n    exit: nope\n    nodes: [nope]\n'),
}


def run(*args):
    p = subprocess.run([BIN, *args], capture_output=True, text=True)
    return p.returncode, (p.stdout or "") + (p.stderr or "")


def lint_paths(text):
    return set(re.findall(r"^(?:error|warning): ([^:]+):", text, re.M))


def register_paths(text):
    out = set()
    for m in re.finditer(r'"path":\s*"([^"]+)"', text):
        out.add(m.group(1))
    return out


def parse_failure(text):
    return "unmarshal errors" in text or "parse config" in text


agree, disagree = 0, []
print()
print(f"== {len(CASES)} defective templates through both verbs ==")
for label, body in CASES.items():
    slug = re.sub(r"\W+", "-", label)
    path = os.path.join(WORK, slug + ".yml")
    with open(path, "w") as fh:
        fh.write("name: lint-probe-" + slug + "\n" + BASE + body)
    lrc, lout = run("template", "lint", path)
    rrc, rout = run("template", "register", path)
    if parse_failure(lout) and parse_failure(rout):
        same, detail = True, "both refused to parse the file"
    else:
        lp, rp = lint_paths(lout), register_paths(rout)
        same = (lp == rp) and ((lrc != 0) == (rrc != 0))
        detail = f"lint {sorted(lp) or 'clean'} exit {lrc} / register {sorted(rp) or 'clean'} exit {rrc}"
    if same:
        agree += 1
        print(f"  agree     {label:28s} {detail}")
    else:
        disagree.append(label)
        print(f"  DISAGREE  {label:28s} {detail}")

print()
print(f"{agree} of {len(CASES)} cases: lint and register reported the same findings")
if disagree:
    print("FAIL  " + ", ".join(disagree))
    sys.exit(1)
print("PASS  lint reports exactly what registration rejects, against the same deployment")
sys.exit(0)
