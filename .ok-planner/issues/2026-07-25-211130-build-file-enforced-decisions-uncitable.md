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

The annotation convention links a decision to its enforcement point with an in-code citation tag (`@decision: <slug>`), and the periodic implementation audit navigates by those tags. But roughly twenty decisions — the seven dependency-boundary lint rules, the pinned-library choices (HTTP router, both database drivers, cron, YAML), the build and image decisions — are enforced solely inside `.golangci.yml`, `go.mod`, the Makefile, or Dockerfiles, and this codebase has never stamped citations into those files. The vendored comment linter can't police tags there either: its grammar covers code file types only, so a YAML citation would never be checked for staleness. Re-verification confirms `decision:depguard-graph-purity` remains cited nowhere.

The gap is convention, not impossibility, and the repo already contains the strongest evidence about which closing mechanism works: `decision:env-var-registry` is proven by a working Go fitness test (`code:test/plumbline/env_var_registry_test.go` with `tools/env-registry/`) that asserts what the config enforces and carries the annotation normally — self-policing, and exactly the shape this issue would generalize. The choice among the three mechanisms is a genuine tradeoff no rule picks, and the annotation rule's text lives in the suite-materialized definitions file this project can't hand-edit — so the "amend the rule" option isn't locally available anyway. Whatever mechanism wins becomes the landing path for the config-enforced bucket in `issue:coverage-gap-decisions-bulk-160-uncited`.

## Options

- **Grouped fitness tests per enforcement surface** — one test file asserting each config-enforced rule's presence and shape, annotated with the decisions it covers. More code; self-policing; matches the env-registry precedent.
- **Citation comments stamped into the config files** — cheapest; permanently unpoliced by the per-edit lint, so a renamed decision orphans them silently.
- **Exempt config-enforced decisions from annotation** — least work; the rule text isn't locally editable, and the audit loses its navigation for exactly the decisions hardest to find by reading.

The ruling picks the mechanism.

## Ruling

> Recommended ruling (/verify-issues): adopt grouped fitness tests —
> one per enforcement surface (the dependency-lint config, the module
> manifests, the Makefile/image set), each asserting the presence and
> shape of every config-enforced rule it covers and carrying the
> `@decision:` annotations for all of them.
>
> Rationale: the repo already proves config-enforced choices this way
> (the env-registry and canonicalization-pin checks), the tests are
> exhibitable evidence the audit can point at rather than bare
> pointers, and the per-edit lint keeps policing the annotations —
> the only option that strengthens the linking convention instead of
> weakening it. The flip case: if the comment linter someday gains
> YAML/manifest grammar support, cheap in-file citations become
> police-able and the calculus reopens.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
