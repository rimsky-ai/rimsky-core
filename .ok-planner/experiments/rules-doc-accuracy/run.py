import json
import pathlib
import re
import subprocess
import sys

REPO = pathlib.Path(__file__).resolve().parents[3]
RULES = REPO / ".claude" / "rules" / "rules.md"
CHECKS = []

CURATED_DEAD_REFERENCES = [
    "deploy/build-images.sh",
    "deploy/docker-compose.yml",
    "`executors/claude-agent",
    "docs/2026-04-25-stores-redesign.md",
]

PATH_EXTENSIONS = (".sh", ".yml", ".yaml", ".md", ".go", ".proto", ".ts", ".json", ".jsonl", ".toml")


def check(label, ok, detail=""):
    CHECKS.append((label, bool(ok)))
    print(("PASS  " if ok else "FAIL  ") + label + ((" | " + detail) if detail else ""))


def die(msg):
    print("HARNESS ERROR: " + msg)
    sys.exit(2)


def backtick_spans(text):
    return re.findall(r"`([^`\n]+)`", text)


def normalise(token):
    return token[2:] if token.startswith("./") else token


def looks_like_root_level_filename(name):
    base = name.rsplit(".", 1)[0]
    return base.startswith(".") or base == base.upper()


def looks_like_repo_path(token):
    token = normalise(token)
    if not token or token.startswith(("http://", "https://", "make")):
        return False
    if "*" in token or "{" in token:
        return False
    if token.endswith("/"):
        return True
    if not token.endswith(PATH_EXTENSIONS):
        return False
    if "/" in token:
        return True
    return looks_like_root_level_filename(token)


def scannable(text):
    return "\n".join(line for line in text.splitlines()
                     if not line.strip().startswith("Exclude from file searches:"))


def make_targets():
    out = set()
    for line in (REPO / "Makefile").read_text().splitlines():
        if line.startswith(".PHONY:"):
            out.update(line.split(":", 1)[1].split())
        match = re.match(r"^([A-Za-z0-9._-]+):", line)
        if match:
            out.add(match.group(1))
    return out


def main():
    if not RULES.exists():
        die("the contributor-facing rules file is missing at %s" % RULES)
    text = RULES.read_text()

    print("  leg 1: every path the rules cite in a filesystem-path shape")
    cited = []
    for span in backtick_spans(scannable(text)):
        for token in span.split():
            if looks_like_repo_path(token) and token not in cited:
                cited.append(token)
    missing = [c for c in cited if not (REPO / normalise(c)).exists()]
    print("  cited paths: " + json.dumps(cited))
    check("every path the rules cite resolves to a real repository artifact (%d cited)" % len(cited),
          not missing, json.dumps(missing))
    check("the rules cite at least one path, so the check has a population",
          len(cited) > 0, str(len(cited)))

    print("  leg 2: the curated known-dead references")
    present = [ref for ref in CURATED_DEAD_REFERENCES if ref in text]
    check("none of the %d curated dead references appears in the rules" % len(CURATED_DEAD_REFERENCES),
          not present, json.dumps(present))

    print("  leg 3: the commands the verification steps tell a contributor to run")
    targets = make_targets()
    cited_makes = sorted({span.split()[1] for span in backtick_spans(text)
                          if span.split() and span.split()[0] == "make" and len(span.split()) > 1})
    unknown = [t for t in cited_makes if t not in targets]
    print("  cited make targets: " + json.dumps(cited_makes))
    check("every make target the rules name exists in the Makefile (%d named)" % len(cited_makes),
          not unknown, json.dumps(unknown))
    check("the rules name the image-rebuild step the verification section depends on",
          "make core-images" in text)

    print("  leg 4: the rules file a contributor actually reads is the one measured")
    res = subprocess.run(["git", "ls-files", "--error-unmatch", ".claude/rules/rules.md"],
                         cwd=str(REPO), capture_output=True, text=True)
    check("the rules file is committed, so every contributor's checkout carries it",
          res.returncode == 0, (res.stdout + res.stderr).strip()[:200])

    failed = [c for c in CHECKS if not c[1]]
    print("")
    print("%d checks, %d failed" % (len(CHECKS), len(failed)))
    sys.exit(1 if failed else 0)


main()
