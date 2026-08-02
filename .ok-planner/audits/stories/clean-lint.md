---
audit: clean-lint
artifact: story:clean-lint
determination: supported
commit: b767a27d
audited: 2026-08-02T09:38:50Z
---

# Maintainer can verify the codebase passes Plumbline's full enforcement

Supported. Running the vendored `.ok-plumbline/bin/plumbline` checker over the whole repository exits 0 (clean), and `.ok-plumbline/config.json` carries no `checks` key, which per `test/plumbline/clean_test.go`'s `assertAllChecksActive` means both of Plumbline's checks (comment-hygiene and citation-resolution — the only two the binary runs) are active by default rather than selectively disabled. `TestPlumblineClean` in that same file gives a maintainer a standing, repeatable way to run this same check and get a pass/fail verdict, and `.claude/settings.json`'s `PostToolUse` hook runs the same checker on every Edit/Write, so the capability is available both on demand and continuously, not just as a one-off manual check.
