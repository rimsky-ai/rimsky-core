# Plumbline comment-hygiene sweep

`decision:coding-style` records that rimsky has adopted Plumbline as its
methodology and runs two of three lint checks. The third — `comment_hygiene`
— was off because the lint had no GoDoc-shape exemption; that gate has been
satisfied (Plumbline v0.2.2 added the GoDoc exemption, v0.2.3 added JSDoc).
This spec drives the codebase to a state where Plumbline runs all three
checks clean.

The work is one user outcome — the codebase passes Plumbline's full
enforcement — decomposed into mechanical and prose-judgment passes against
6,810 known violations clustered by Plumbline's shape classifier into six
shapes (divider, commented-out-code, todo-marker, license-fragment,
doc-residue, untagged-prose).

## User outcomes

> **STORY-clean-lint** — As a rimsky maintainer, I can verify that the
> codebase passes Plumbline's full enforcement with every check active, so
> `decision:coding-style` accurately describes the active configuration.
>
> **Acceptance:** the maintainer runs Plumbline's lint against the
> post-work tree → the lint reports the codebase clean, and the project's
> Plumbline configuration shows every check active.
>
> **Falsifier:** the maintainer runs Plumbline's lint and either sees any
> violation reported, or finds any check inactive in the project's
> Plumbline configuration.
>
> **Proof:** executable — a script that runs Plumbline's lint against the
> post-work tree, asserts the lint reports clean, and asserts the project's
> Plumbline configuration has every check active.

## Technical decisions

### TD-comment-hygiene-uniform-rule

**Choice:** Every comment-hygiene violation outside the doc-residue cluster
is resolved by tag-or-delete. A comment is tagged with `@constraint:`,
`@deliberate:`, `@agent-contract`, or one of the project-extended tags
(`@concept:`, `@story:`, `@decision:`) when it encodes a load-bearing why
the agent or contributor would otherwise lose. A comment is deleted as
residue otherwise.

**Rationale:** Plumbline's thesis is that load-bearing prose must be
mechanically distinguishable from generation residue, and the tag
vocabulary is the project's existing structural-comment surface. Uniform
per-site application keeps the rule simple and the lint enforceable; the
cluster taxonomy is for sampling and prioritization, not for parallel
rules.

### TD-mechanical-cluster-sweep

**Choice:** The four small clusters — divider (59), commented-out-code
(105), todo-marker (4), license-fragment (6), ~174 sites total — are
handled in a dedicated up-front pass. Per-cluster defaults: divider →
delete; commented-out-code → delete; todo-marker → delete; license-fragment
→ resolve per TD-comment-hygiene-uniform-rule (the cluster is
mis-classified by the lint's shape heuristic — none of the six sites are
license-text fixtures; all are prose comments in
`code:tools/license-check/headers_test.go` whose surrounding code mentions
SPDX/Copyright).

For the four todo-marker sites in `code:lib/runtime/peer/{dial.go,
publisher_client.go, validation_client.go, data_processing_client.go}` —
all identical `// TODO(host-agent-proxy v2): install ServiceName
interceptor here when this protocol gains late-bind support` — the deletion
is unconditional: the deferral is recorded in
`history:plans/2026-05-24-host-agent-and-proxy-divergences.md:324`, where it
belongs; the source markers are forward-looking residue and have no place in
the tree.

**Rationale:** Mechanical and judgment work want different validator
reviews. Sweeping the mechanical clusters up-front leaves every subsequent
pass purely about prose judgment, and the sweep's own validator review is a
single shape check ("are these all the expected-shape deletions?").

### TD-doc-residue-reshape-pass

**Choice:** The 849 doc-residue cluster sites get their own pass with a
per-site decision rule:

1. **GoDoc-position site** (the comment sits directly above a package-level
   `func`, `type`, `const`, or `var` declaration in a `.go` file): reshape
   the comment so its first word names the declaration on the next
   non-comment line and the body describes what the symbol IS. The reshaped
   comment satisfies Plumbline's GoDoc exemption.
2. **Not in GoDoc position** (the comment sits above an inside-function
   `var x := ...` line, is a divider that the lint's heuristic mis-clustered
   into doc-residue, or otherwise): fall through to
   TD-comment-hygiene-uniform-rule.

The same rule applies to the `.ts` / `.tsx` analog (JSDoc above
package-level `export function`, `export class`, etc.).

**Rationale:** The doc-residue cluster is bimodal: about half the sites are
package-level declaration docs where GoDoc/JSDoc is the conventional shape
agents reading the code expect; the other half are inside-function
why-comments where GoDoc doesn't apply. A dedicated pass with reshape
evaluated first nudges the package-level half toward the conventional shape
without forcing the executor to invent that priority during a
tag-or-delete-framed pass.

### TD-untagged-prose-by-module

**Choice:** The ~5,787 untagged-prose sites decompose into one pass per
top-level module root in rimsky's source layout: `cmd/`, `lib/foundation/`,
`lib/graph/`, `lib/runtime/`, `lib/control/`, `lib/services/` (including
its TypeScript subtree under `lib/services/executors/claude-agent/`),
`lib/protocols/` (excluding the generated `lib/protocols/proto/v1/gen/`
which is already in `.plumbline.json`'s `ignore` list), `examples/`,
`test/`, `tools/`, and any remaining top-level files under `.claude/` and
`dockerfiles/`. One pass per module-root. Within each pass, every site
resolved per TD-comment-hygiene-uniform-rule.

Pass sizes will be uneven (`cmd/` is small; `lib/runtime/` and
`lib/services/` are large); uneven sizing is accepted because each pass is
a coherent architectural review surface, matching the module layout
described in `concept:module-layout` and enforced by the depguard rules in
`code:.golangci.yml`.

**Rationale:** A 5,787-violation single pass is not validator-reviewable.
The natural splitting axis is rimsky's existing top-level module layout —
the same axis the import-boundary lint, the five-Go-module split
(`file:go.work` listing the root, `lib/foundation`, `lib/protocols`,
`lib/services`, and `examples`), and `concept:module-layout` already use.
Per-module passes match how reviewers (human and agent) already navigate
the tree.

### TD-config-flip

**Choice:** `file:.plumbline.json`'s `comment_hygiene` check is flipped to
`true` in the **last** pass, after the final module pass goes clean. At
that point the existing `source_validity` and
`blessed_invariant_test_coverage` are already `true`, the post-work tree is
clean across all three checks, and the maintainer-facing lint enforces the
new state.

**Rationale:** The lint blocks edits that introduce new violations of any
enabled check. Flipping `comment_hygiene` early would block intermediate
passes from committing partial progress; flipping it last keeps each pass's
verify-and-commit step honest and converges to the full enforcement state
at the moment the codebase actually meets it.

## Mechanism

The work is a sequence of passes against Plumbline's lint output. Pass
boundaries are validator-review surfaces; within a pass, sites are touched
in whatever order the executor finds tractable.

**Pass order:**

1. **Mechanical-cluster sweep** (TD-mechanical-cluster-sweep) — delete the
   ~174 sites across divider / commented-out-code / todo-marker /
   license-fragment-mis-clustered. Verify via Plumbline's `patterns`
   subcommand (with `comment_hygiene` temporarily enabled in a config copy,
   not in the committed `file:.plumbline.json` yet) that those four
   cluster counts read zero. Commit.
2. **Doc-residue reshape** (TD-doc-residue-reshape-pass) — process the 849
   doc-residue sites with the GoDoc-position-first rule. Verify the
   doc-residue cluster count is zero. Commit.
3. **Per-module untagged-prose** — one pass per module root in any order:
   `cmd/`, `lib/foundation/`, `lib/graph/`, `lib/runtime/`, `lib/control/`,
   `lib/services/`, `lib/protocols/`, `examples/`, `test/`, `tools/`,
   and a final catch-all pass for top-level files under `.claude/` and
   `dockerfiles/`. Each pass: process all untagged-prose sites under that
   root per TD-comment-hygiene-uniform-rule; verify the cluster count for
   the module's file paths drops to zero; commit. The aggregate
   untagged-prose cluster count reaches zero after the last module pass.
4. **Config flip** (TD-config-flip) — set `file:.plumbline.json`'s
   `comment_hygiene` check to `true`. Run Plumbline's full lint against the
   committed tree; verify clean exit. Commit.

**Verification across passes:** each pass's clean state is verified by
running the lint with all three checks temporarily enabled (via a config
copy or override) and confirming the per-cluster count for that pass's
target shape is zero. The final pass is the only one that commits the
enabled state into `file:.plumbline.json`.

**Tooling used per pass:** Plumbline's `patterns` subcommand for
shape-cluster verification within a pass; Plumbline's `suggest` subcommand
for per-violation proposed fixes (advisory; the per-site rule in
TD-comment-hygiene-uniform-rule is the final arbiter); the standard
test + lint suite from `Makefile` (`make build-all`, `make test-all`,
`make lint`) at each commit to confirm no Go-level regression.

## Design changes

Each design-doc body below is the path-free, current-state-only text to
write verbatim. The spec body above carries spec-specific citations
(`code:`, `history:`, file paths) appropriate for the executor but
disallowed in design-doc bodies; the bodies below restate the same
decisions in design-doc-appropriate form.

- **Story:** create `design/stories/clean-lint.md` capturing
  STORY-clean-lint's "As ... I can ... so that ..." opening sentence
  (which carries the role / capability / business-value triple in the
  brainstorm-skill canonical shape) plus the Acceptance, Falsifier, and
  Proof sections verbatim.

- **Decision:** mutate `design/decisions/coding-style.md` Choice section
  in place. Replace the entire Choice section with:

  > Rimsky's coding methodology is Plumbline, consumed as a Claude Code
  > plugin. The plugin materializes the methodology's per-session cheatsheet
  > into the repo where every contributor and agent reads it; the cheatsheet
  > is committed so contributors without the plugin still see the rules. The
  > lint runs all three checks — `source_validity`,
  > `blessed_invariant_test_coverage`, and `comment_hygiene` — with the
  > comment-hygiene check honoring GoDoc-style and JSDoc-style block
  > exemptions for Go and TS/JS respectively, so canonical doc shapes pass
  > without per-comment tagging. A PostToolUse lint blocks edits that
  > introduce new violations across any check; CI invokes the same lint
  > against the full tree. Project-specific tag-vocabulary extensions
  > configure the plugin to recognize the design-citation tags this project
  > uses (`@concept:`, `@story:`, `@decision:`).

- **Decision:** create `design/decisions/comment-hygiene-uniform-rule.md`
  from the template, with the following sections:

  > **Choice:** Comment-hygiene violations are resolved by tag-or-delete on
  > a per-site basis. A comment is tagged with `@constraint:`,
  > `@deliberate:`, `@agent-contract`, or one of the project-extended
  > design-citation tags (`@concept:`, `@story:`, `@decision:`) when it
  > encodes a load-bearing why an agent or contributor would otherwise
  > lose. A comment is deleted as residue otherwise. The doc-residue
  > cluster overrides this rule with a reshape-first evaluation per
  > `decision:doc-residue-reshape-pass`.
  >
  > **Rationale:** Plumbline's thesis is that load-bearing prose must be
  > mechanically distinguishable from generation residue, and the
  > structured-tag vocabulary is the project's existing surface for that
  > distinction. Uniform per-site application keeps the rule simple and
  > the lint enforceable; the cluster taxonomy the lint surfaces is for
  > sampling and prioritization, not for parallel rules.
  >
  > **Alternatives:** per-cluster bespoke rules (different action vocabulary
  > for divider vs commented-out-code vs prose) — rejected because the
  > cluster heuristic is for grouping by *shape*, not for licensing
  > different categorical actions; the per-site decision is the same
  > tag-or-delete in every case.

- **Decision:** create `design/decisions/mechanical-cluster-sweep.md`
  from the template, with the following sections:

  > **Choice:** The mechanical comment-hygiene clusters — divider,
  > commented-out-code, todo-marker, and license-fragment-mis-classified —
  > are swept in a dedicated pass distinct from the per-site prose-judgment
  > passes. Per-cluster defaults: divider → delete; commented-out-code →
  > delete; todo-marker → delete; license-fragment → resolve per
  > `decision:comment-hygiene-uniform-rule` (the cluster is shape-misclassified
  > — its sites are prose comments rather than license-text fixtures).
  >
  > **Rationale:** Mechanical work and per-site prose judgment want
  > different validator reviews. Sweeping the mechanical clusters in their
  > own pass leaves every subsequent pass purely about judgment, and the
  > sweep's own validator review collapses to a single shape check.
  >
  > **Alternatives:** folding the mechanical sites into the per-module
  > prose-judgment passes — rejected because mixing mechanical deletes
  > and per-site judgment in one pass forces the validator to switch
  > review modes mid-pass.

- **Decision:** create `design/decisions/doc-residue-reshape-pass.md`
  from the template, with the following sections:

  > **Choice:** The doc-residue comment-hygiene cluster gets a dedicated
  > pass with a reshape-first per-site rule. When the comment sits directly
  > above a package-level declaration (Go: `func` / `type` / `const` /
  > `var`; TS/JS: `export function` / `export class` / `export const` /
  > etc.), the comment is reshaped so its first word names the declaration
  > on the next non-comment line and the body describes what the symbol IS,
  > satisfying Plumbline's GoDoc / JSDoc exemption. When the comment is not
  > in a doc-position (above an inside-function declaration, a divider that
  > the cluster heuristic surfaced here, or otherwise), the comment is
  > resolved per `decision:comment-hygiene-uniform-rule`.
  >
  > **Rationale:** The doc-residue cluster is bimodal: roughly half its
  > sites are package-level declaration docs where GoDoc / JSDoc is the
  > conventional shape agents reading the code expect, and the other half
  > are inside-function why-comments where the doc convention doesn't
  > apply. A dedicated pass with reshape evaluated first nudges the
  > package-level half toward the conventional shape without forcing the
  > executor to invent that priority during a tag-or-delete-framed pass.
  >
  > **Alternatives:** treating doc-residue uniformly under
  > `decision:comment-hygiene-uniform-rule` — rejected because the
  > GoDoc-position half of the cluster genuinely benefits from the
  > conventional shape, and a tag-or-delete framing under-serves that.

- **Decision:** create `design/decisions/untagged-prose-by-module.md`
  from the template, with the following sections:

  > **Choice:** Untagged-prose comment-hygiene violations decompose into
  > one sweep per top-level module root, using the splitting axis described
  > by `concept:module-layout`. Pass count equals module-root count; pass
  > sizing is uneven by design. Within each pass, every site is resolved
  > per `decision:comment-hygiene-uniform-rule`.
  >
  > **Rationale:** A single sweep over thousands of judgment-only sites is
  > not validator-reviewable. The module-layout axis is the project's
  > existing coherent review boundary — it's the axis the import-boundary
  > rules and multi-module split already use — so per-module passes match
  > how reviewers (human and agent) already navigate the tree.
  >
  > **Alternatives:** bucketing the violations into fixed-size passes —
  > rejected because module-coherent review surfaces beat uniform-size
  > buckets when the work is per-site judgment.

- **Decision:** create `design/decisions/config-flip.md` from the
  template, with the following sections:

  > **Choice:** Activation of an inactive Plumbline check follows clean
  > state, not preceding it. The configuration change that flips a check
  > from inactive to active is committed only after the codebase is already
  > clean against that check.
  >
  > **Rationale:** The lint blocks edits that introduce new violations of
  > any active check. Activating a check whose backlog is non-zero would
  > block the very edits that would resolve the existing violations.
  > Activating after the backlog reaches zero converges to full enforcement
  > at the moment the codebase actually meets it.
  >
  > **Alternatives:** activating the check before the sweep — rejected
  > because the edit-blocking PostToolUse lint would prevent the sweep
  > itself.

## Manifest

### Stories

- **STORY-clean-lint** — codebase passes Plumbline's full enforcement
  with every check active (Proof: executable)

### Technical decisions

- **TD-comment-hygiene-uniform-rule** — tag-or-delete per site, with the
  Plumbline tag vocabulary
- **TD-mechanical-cluster-sweep** — divider / commented-out-code /
  todo-marker / license-fragment-mis-clustered in one up-front pass
- **TD-doc-residue-reshape-pass** — doc-residue sites get a dedicated
  reshape pass with GoDoc/JSDoc-position-first rule
- **TD-untagged-prose-by-module** — one pass per top-level module root
- **TD-config-flip** — `.plumbline.json`'s `comment_hygiene` check flipped
  to `true` in the final pass

### Design changes

- **Story:create:clean-lint** — new `design/stories/clean-lint.md`
- **Decision:mutate:coding-style** — replace Choice section in
  `design/decisions/coding-style.md`
- **Decision:create:comment-hygiene-uniform-rule** — new file
- **Decision:create:mechanical-cluster-sweep** — new file
- **Decision:create:doc-residue-reshape-pass** — new file
- **Decision:create:untagged-prose-by-module** — new file
- **Decision:create:config-flip** — new file
