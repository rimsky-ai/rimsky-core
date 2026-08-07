---
issue: cli-help-text-gaps
kind: audit
category: doc-drift
artifacts: []
status: repaired
opened: 2026-08-06T06:49:02Z
---

# Three CLI help-text gaps mis-project into every generated CLI reference

Question: does `rimsky`'s top-level `--help` output accurately reflect the
CLI's actual auth subcommands, conformance subcommands, and `watch` exit
behavior?

Re-verification against the current tree found two of the three filed gaps
still live and one already fixed: (1) `auth login` (`cmd/rimsky/cli/auth_login.go`)
was still missing from the "Auth:" group in `cmd/rimsky/main.go`'s top-level
help; (2) the top-level conformance line already lists all eight subcommands
including `lifecycle-subscriber` (`cmd/rimsky/main.go`, matching
`cmd/rimsky/conformance.go`'s own usage) — this part of the filed gap had
already been fixed and needed no action; (3) the `watch` line still said
"until terminal" while `cli/watch.go`'s `--until` flag takes `idle` (default)
or `terminated` — there is no "terminal" value at all.

This is a mechanical doc-accuracy fix with no design-corpus commitment
involved (CLI help text isn't part of the concept/story/decision corpus) —
the compliant end state is simply "the help text matches the code," which
was never in question.

Repaired in `cmd/rimsky/main.go`:
- Added `auth login` to the "Auth:" help group.
- Changed the `watch` line from "Live feed: events + breakpoint hits until
  terminal" to "Live feed: events + breakpoint hits until idle (--until)".

Verified: `go build ./...`, `go vet ./...`, `golangci-lint run
./cmd/rimsky/...`, and `./.ok-plumbline/bin/plumbline cmd/rimsky/main.go` all
clean.
