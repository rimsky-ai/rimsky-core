# Divergence Report — Collapse `sdk/go` into `protocols`

Plan: `.ok-planner/plans/2026-05-26-collapse-sdk-into-protocols.md`
Spec: `.ok-planner/specs/2026-05-26-collapse-sdk-into-protocols-design.md`
Audited working tree as of 2026-05-26 (uncommitted; renames staged, edits unstaged).

The implementation tracks the plan very closely across all six passes:
the module moves, package renames (`server`→`serverkit`, `publisher`→`publisherkit`),
the `testpg` carve-out, the `internal/ops` AGPL header flip, the `go.work`/`go.mod`/
`Makefile`/`.golangci.yml`/`licensing.yml` rewiring, the `foundation/locks` alias
removal (all 17 re-exports gone, no `= claimproducer.` lines remain, no
`ParseWriteSemantics` wrapper existed to remove), and the concept-doc mutations
(including the planned Task 23 step 2 stub-exemption note and Task 17 lint
exemption) all landed as written. `make build-all`, `make lint`, and
`make license-lint` are green; `make license-stamp` is a verified no-op against
the current tree.

One substantive divergence and a few small notes follow.

---

## 1. `stores/stub/cmd/main.go` reclassified Apache → AGPL (header flip + new `licensing.yml` entry)

**What the plan said:** Task 13 repoints the single `ops` consumer
`stores/stub/cmd/main.go` by rewriting `…/sdk/go/ops` → `…/internal/ops`, and
explicitly scopes the fallout to the *depguard* layer only: "*`stores/stub/cmd`
is matched by the `consumption-side-isolation` depguard rule's `stores/**`
target, which denies `internal/`… The build (`go build`) compiles regardless;
the lint resolution lands in Task 17.*" Task 17 then adds the
`!stores/stub/**` / `!executors/stub/**` depguard exemptions. No plan task
mentions touching `stores/stub/cmd/main.go`'s license header, and Task 15's
`licensing.yml` edits are limited to removing `sdk/go/`, adding `testpg/`, and
removing the stale `conformance/` entry. The Final/Task-16 verification asserts
`make license-stamp && git diff --quiet`.

**What was implemented:**
- `stores/stub/cmd/main.go:1-3` — license header flipped from the Apache
  SPDX block to the AGPL dual-license block.
- `licensing.yml:82` — a new entry added to the `agpl:` block:
  `- stores/stub/cmd/  # rimsky-internal stub entrypoint: wires in internal/ops (AGPL); the stub store/server/testfixture packages stay Apache (adopter-referenceable)`.
- The CHANGELOG entry documents it as "*A Pass-5 reconstruction additionally
  reclassified the stub store's `cmd/` entrypoint as AGPL…*".

**Inferred reason:** Forced choice the plan did not anticipate. The plan only
reasoned about the *depguard* boundary (import-layering), but `licensing.yml`
enforces a *second, independent* boundary — license direction (Apache may not
import AGPL), checked by `cmd/rimsky-license-check`. `stores/` is Apache-tier;
once `stores/stub/cmd/main.go` imports `internal/ops` (now AGPL), an
Apache-classified file imports an AGPL package, which `make license-lint` would
reject. The depguard exemption (Task 17) fixes layering but does nothing for
license direction. The minimal clean fix — reclassify just the `cmd/`
sub-directory to AGPL via longest-prefix-match, leaving the adopter-referenceable
`stub/store`, `stub/server`, `stub/testfixture` packages Apache — is exactly
what landed. The implementer flagged it transparently in the CHANGELOG. (Note: the
CHANGELOG calls this "a Pass-5 reconstruction," but mechanically it is Pass-4
Task-13 fallout; the Pass-5 framing is a minor mislabel, not a behavior
difference.) This is the single instance of "edits beyond the plan's enumerated
scope" in the run.

---

## 2. `make license-stamp && git diff --quiet` does not hold as literally written (benign)

**What the plan said:** Task 16 and the Final verification both require
`make license-stamp && git diff --quiet` to exit 0 (no header drift).

**What was implemented:** `git diff --quiet` returns non-zero — but only because
the entire refactor is uncommitted, so the working tree always shows a diff.
Verified independently that `make license-stamp` is a true no-op: the
`git diff --stat` summary is byte-identical before and after running it
(108 files, +451/-533 both times), and the AGPL headers on `internal/ops/*` and
`stores/stub/cmd/main.go` are not reverted.

**Inferred reason:** Plan-verification phrasing assumes a clean committed baseline
where `git diff --quiet` isolates stamp-induced drift. Against an uncommitted
tree the check is unsatisfiable by construction. The *property* the check is
guarding (no header drift) holds. Not a real divergence — recorded only so a
reviewer who runs the literal command and sees a non-zero exit understands why.

---

## 3. Conformance subtree moved with fewer content edits than the plan's mechanical framing implied (expected, no action)

**What the plan said:** Task 4 steps 2–3 describe rewriting intra-conformance
import paths across the moved tree and re-grepping for stray `sdk/go` imports.

**What was implemented:** Only the 9 `executor/scenarios/*.go` files (plus
`execute_happy_path.go`'s package doc-comment) needed import-line rewrites — they
import the sibling `conformance/executor` package by full path. The other ~24
conformance files moved with zero content change (verified: they are same-package
or import only `protocols/proto/...` and third-party, so the `git mv` sufficed).
No stray `sdk/go` references remain anywhere under `protocols/`.

**Inferred reason:** Cleaner-than-feared reality, consistent with the plan's own
Task 4 note that "the conformance tree is self-contained." Recorded only because
the staged-rename list shows many conformance files moved while the unstaged-edit
list shows only a handful touched — a reviewer skimming the diff stats might
wonder whether files were missed. They were not.

---

## 4. `foundation/locks/types.go` retains code-path references in doc comments (in-scope, allowed)

**What the plan said:** Task 20 removes the 17 alias declarations and "the
surrounding explanatory comments," keeping genuinely-local declarations.

**What was implemented:** The aliases and their doc comments are gone. The
rewritten file-level doc comment and the surviving `NamedLockSpec` block still
name code paths — e.g. `github.com/rimsky-ai/rimsky-core/protocols/claimproducer`
in the package doc (`foundation/locks/types.go`).

**Inferred reason:** Not a divergence — this is source code, where path
references are fine; the concept self-containment rule applies only to
`.ok-planner/design/` concept bodies (which were audited and are path-free).
Recorded only to preempt a false-positive flag.

---

## Items checked and found matching (no divergence)

- `go.work` / root `go.mod` rewiring (`sdk/go` → `testpg` require+replace): matches Task 11.
- `protocols/go.mod` gains exactly `google/uuid` + `gopkg.in/yaml.v3` as direct deps: matches.
- `testpg/go.mod`: standalone module, correct path, three required deps, no `replace`: matches Task 10.
- All 4 `internal/ops/*.go` carry the AGPL dual-license block; `grep -l 'Dual-licensed under AGPL' internal/ops/*.go` = 4: matches Task 13.
- `.golangci.yml`: `sdk-purity`→`protocols-purity` (retargeted `**/protocols/**`, testcontainers deny added), `pgx-isolation` allow-list flipped to `testpg`, `foundation-purity`/`graph-purity` sdk denies removed, `consumption-side-isolation` stub exemptions added with explanatory comment: matches Task 17 exactly.
- `foundation/internal/pgtest/pgtest.go`: both `@source:` annotations repointed `sdk/go/testpg` → `testpg`: matches Task 12.
- `Makefile`: all four `cd sdk/go` sites (lint, test-all, build-all, lint-docker) retargeted to `testpg`; comments updated: matches Task 15. (Plan named three targets; the `lint-docker` variant was also correctly caught, as the plan's grep instruction anticipated.)
- `concept:sdk` retired to `_retired/`, frontmatter `status: retired`, retirement blockquote + Notes entry verbatim from spec: matches Task 22.
- `concept:module-layout` and `concept:conformance` mutations: all spec sub-bullets applied, path-free, with dated Notes entries: matches Tasks 23–24.
- `concepts.md` TOC: `sdk` line moved to Retired section, `conformance` headline updated: matches Task 25.
- CHANGELOG: single Unreleased bullet covering all six passes: matches Task 26.
- `sdk/go/` fully deleted (`doc.go`, `README.md`, `go.mod`, `go.sum`); no `rimsky/sdk/go` references remain in any `*.go`: matches Task 14.
