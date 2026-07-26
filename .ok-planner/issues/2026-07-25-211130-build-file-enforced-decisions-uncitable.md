---
issue: build-file-enforced-decisions-uncitable
kind: audit
category: proof
artifacts:
  - decision:depguard-graph-purity
status: verified
opened: 2026-07-25T21:11:30Z
---

# About twenty decisions are enforced only in lint and build files, which carry no citations

The annotation-integrity convention links every decision to its enforcement point with an in-code citation tag, and the audit's coverage check greps for those tags. But roughly twenty decisions — the seven dependency-boundary lint rules, the pinned-library choices (HTTP router, both database drivers, cron, UUID, YAML), the build and image decisions — are enforced solely inside `.golangci.yml`, `go.mod`, the Makefile, or Dockerfiles, and this codebase has never stamped citations into those files. The vendored comment linter cannot police them either: its grammar list covers code file types only, so a citation placed in YAML would never be checked for staleness — though the audit's own grep, which is extension-blind, would find it.

So the gap is convention, not tooling impossibility, and there are three genuinely different mechanisms to close it. Stamping tags into the config files is cheapest but leaves those tags permanently unpoliced by the per-edit lint — a renamed decision would orphan them silently. Per-decision fitness tests (Go tests asserting the config carries the rule, annotated normally) are more code but self-police and double as strong, exhibitable proofs; the repo's existing fitness-test suite already works this way. Amending the annotation rule to exempt and enumerate config-enforced decisions avoids both costs but weakens the every-decision-links-to-code invariant.

## Options

- Config-file citation comments — cheapest; permanently unpoliced by the per-edit lint.
- Grouped fitness tests per enforcement surface (one test file asserting each config-enforced rule, annotated with the decisions it proves) — more code; self-policing, and doubles as non-vacuous proof.
- Amend the annotation-integrity rule to exempt an enumerated config-enforced set — least work; weakens the linking invariant it exists to keep real.

## Ruling

> Recommended ruling (/verify-issues): adopt grouped fitness tests — one per
> enforcement surface (the dependency-lint config, the module manifests, the Makefile/image
> set), each asserting the presence and shape of every config-enforced rule it covers and
> carrying the `@decision:` annotations for all of them.
>
> Rationale: the repo already proves config-enforced choices this way (the
> canonicalization-pin and env-registry checks), the tests are exhibitable proofs rather
> than bare pointers, and the per-edit lint keeps policing the annotations — the only
> option that strengthens rather than weakens the integrity invariant.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
