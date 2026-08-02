---
issue: stories-doc-accuracy-gates-decision
kind: sprint
category: stories-prescriptive
artifacts:
  - story:rules-doc-accuracy
  - story:substitution-doc-accuracy
status: verified
opened: 2026-08-01T22:32:30Z
---

# Two doc-accuracy fitness gates prescribe their mechanisms in story prose

Two stories promise that specific documentation cannot drift from the code: one that the repo's rules document only cites paths that exist, one that the substitution package's documented source-kind list matches the runtime resolver's actual dispatch set. Both are real, code-verified build-time gates (`code:tools/rulesdoc/rulesdoc_test.go`, `code:lib/graph/attribute/substitution_doc_accuracy_test.go`), and both stories describe the gate mechanism in prose the format rules will force out. No decision records the pattern; the corpus is silent.

The two gates parse genuinely different things (Markdown prose against the file tree; a Go doc comment against AST dispatch arms) for the same reason: prose that enumerates code facts must be mechanically diffed against those facts or it rots. That shared reason is the durable content — it is also this repo's house methodology (lint is authoritative over prose) applied to documentation.

## Options

- One decision recording the doc-accuracy-gate pattern, both gates as instances — one artifact, and future doc surfaces inherit the precedent; the cost is a decision whose instances list needs touching when gates are added.
- One decision per gate — two artifacts for one idea; the per-gate parsing details aren't independent choices.
- Rule the mechanisms below corpus altitude — cheapest, but the pattern's precedent value (new enumerating docs should get gates) is lost, and the stories' promises reduce to sentences with nothing recording how they're kept.

The ruling decides whether the gate pattern is a recorded architectural choice or ordinary test detail.

## Ruling

> Recommended ruling (/verify-issues): record one decision — enumerating documentation is kept honest by build-time gates that mechanically diff the prose against the code facts it enumerates — with both gates as its current instances, then reduce both stories to their sentences.
>
> Rationale: the pattern has a real rejected alternative (review discipline) and real precedent value for every future doc surface that enumerates code facts, which is exactly what decisions exist to carry; the per-gate option splits one idea in two, and below-altitude discards the precedent. Flip case: if the owner reads these as ordinary regression tests with no expectation that future doc surfaces follow suit, the pattern has no forward pull and below-altitude is the honest call.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
