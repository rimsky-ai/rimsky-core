# Design-corpus compliance drift after the v3.0.0 rule tightening — Design Sketch

**Date:** 2026-06-14
**Status:** Sketch (not a spec; not authorization to build)
**Origin:** surfaced by the post-execute-plan `review-work` cycle 2 (design-doc compliance) run during the closing of `plan:2026-06-13-plumbline-comment-hygiene-sweep`. The plumbline plan never modified the flagged files — this is pre-existing drift in the rimsky design corpus that the cycle-2 reviewer caught.

## Idea

The ok-planner `review-work` skill's cycle 2 (design-doc compliance) runs unconditionally when `.ok-planner/design/concepts/` exists and audits **every live design file** against the concept self-containment rule and the tension surface rule. The rule is canonically stated in `discover-design/SKILL.md` as the `{{SELF-CONTAINMENT-RULE}}` block; the cycle 2 reviewer prompt embeds it. Findings get passed through `review-cleanup` to a fixer loop.

That cycle just surfaced ~80 + ~25 findings against the rimsky design corpus. The plumbline plan didn't touch any of them — they're pre-existing drift. Two layered causes:

1. **The rule got stricter in ok-planner v3.0.0 (2026-06-11).** The compliance reviewer prompt's "Disallowed in artifact body" / "Current-state only" sections were tightened — new prohibitions on `## Notes` / `## History` / `## Changelog` sections, on dated audit-trail entries, on backward-looking phrasing ("previously called X"), and on forward-looking phrasing ("we plan to", "TODO"). The previously-allowed "Spec slugs in dated Notes entries (`spec:YYYY-MM-DD-<topic>`)" and "Dates" were removed from the allowed list. Rimsky's design corpus was built between 2026-06-09 and 2026-06-13, with a prior compliance pass (`commit:3f800f3 design-corpus compliance pass`, 2026-06-10) cleaning to the v2.x rule. The v3.0.0 release on 2026-06-11 then redrew the line; files compliant with v2.x are not all compliant with v3.0.0.

2. **The reviewer extended the rule beyond its canonical text.** The canonical `{{SELF-CONTAINMENT-RULE}}` block's explicit "Disallowed in artifact body" list calls out: file/dir paths, `code:`/`pkg:`/URL citations, references to external documentation, quoted code / lint-config / external prose, and "Owns / Does NOT own" sections naming code paths. The reviewer this dispatch extended that spirit to CLI verb literals (`rimsky agent`), HTTP route literals (`POST /instances`), env-var literals (`RIMSKY_PROCESS_ROLE=unified`), proto verb names (`Capabilities`, `Open`, `Commit`), external-library names (`testcontainers-go`, `pgx`), license identifiers (`Apache-2.0`, `AGPL-3.0-or-later`), license-text section IDs (AGPL `§5` / `§13`), regex literals, network-address literals, registry coordinates (`golang:1.25-alpine`), and bundled-service slugs (`store-filesystem`, `claude-agent`). Half of these the reviewer self-flagged as "Borderline" — explicit acknowledgement that the canonical text doesn't actually enumerate them. The other half were flagged firmly even though the canonical text is silent on the class.

The result is a thrash pattern: every post-execute-plan compliance cycle will surface a fresh batch of findings depending on (a) which subset of v3.0.0 tightenings the corpus has accumulated and (b) how strictly the dispatched reviewer extends the rule's spirit. Same corpus, different reviewer dispatch, different verdict on the borderline classes.

Drive the corpus to a stable compliance state, AND drive the rule's canonical text to a state where reviewer judgment doesn't keep producing different verdicts on the same shapes. Two pieces of work, distinct in scope.

## Shape

### Today (what we're replacing)

The corpus carries three classes of compliance debt:

**Class A — real v3.0.0 drift.** Files written under the v2.x rule that carry `## Notes` sections, dated spec-citation entries, or backward-looking phrasing. The most current data point from the cycle-2 run found this in: `concepts/node.md:24` ("also numbered §17" — backward-looking lineage). A fresh enumeration is part of this work's first phase; the cycle-2 run only sampled.

**Class B — reviewer-over-extension on CLI-grammar artifacts.** `concepts/rimsky.md`, `concepts/host-agent.md`, `concepts/conformance.md`, `concepts/role-template.md`, `decisions/cli-verb.md`, `decisions/conformance-suite-per-protocol.md`. These artifacts describe surfaces — CLI verbs, subcommand families, flag conventions — whose vocabulary IS the artifact's content. Applying "no CLI literal" strictly produces concepts whose bodies are paraphrases ("an auth subcommand family covering bootstrap, login, key management, status") that lose the actual surface a reader / agent needs.

**Class C — reviewer-over-extension on tool-choice and license-choice decisions.** `decisions/testcontainers-go.md`, `decisions/persistence-driver.md` (`sqlite` named), `decisions/licensing-dual-apache-agpl.md` (license names), `decisions/implementation-language-go-plus-ts.md` (language names), `decisions/cron-robfig-v3.md`, `decisions/postgres-pgx-v5.md`, `decisions/yaml-gopkg-v3.md`, etc. The decision's filename IS the choice; the body cannot avoid restating it. The canonical rule's "no external-documentation references / no quoted external prose" does not say "no library names" — but the reviewer extended it that way.

**Class D — reviewer-over-extension on bundled-service stories.** `stories/store-filesystem.md`, `stories/store-postgres.md`, `stories/sensor-cron.md`, `stories/sensor-http.md`, `stories/claude-agent.md`, `stories/mcp-transport.md`, etc. These describe specific bundled services — and the slug naming the service is also a `story:<slug>` reference in the corpus's own grammar. Whether the literal name `store-filesystem` is a violation or a project-owned slug is a rule-text question.

### Proposed

A spec with two stories on its `## Manifest`:

**STORY-corpus-compliant-with-v3** — As a rimsky maintainer / future agent, I can run the post-execute-plan `review-work` cycle 2 (design-doc compliance) against the rimsky design corpus and have it report clean against the v3.0.0 rule, so that every subsequent execute-plan run closes cleanly without surfacing pre-existing drift.
- **Acceptance**: the maintainer runs `review-work` against a tree with no uncommitted code changes; cycle 2 reports approved.
- **Falsifier**: the maintainer runs cycle 2 and it reports any finding against a live design file in the rimsky corpus.
- **Proof**: executable — the post-cycle reviewer's output records "Approved — no findings."

**STORY-rule-text-disambiguates-its-edges** (upstream-in-ok-planner work; potentially not for rimsky to drive) — As a rimsky maintainer running compliance cycles, I can trust that the canonical rule text explicitly answers whether CLI verb literals, tool / library names, license identifiers, and bundled-service slugs are allowed or disallowed, so that two consecutive reviewer dispatches against the same file produce the same verdict. (Rimsky's role here is to surface the question upstream; the actual rule update is ok-planner's work.)

And technical decisions covering: per-class treatment (auto-fix Class A; auto-fix Classes B/C/D only if rule is updated to say so; otherwise document); whether to file an issue on ok-planner for Classes B/C/D before reshaping rimsky; whether to write a project-specific extension or carve-out to the rule.

## Why this can't ride a Plumbline-style mechanical falsifier

The 2026-06-13 plumbline plan's falsifier was mechanical (run the lint, count violations). That works because plumbline's check is a pure-shape predicate.

The compliance rule's edges are not pure-shape. Whether `concept:rimsky` naming the rimsky CLI's verbs is "quoting external prose" or "the artifact's own vocabulary" is a category-decision, not a shape-decision. Whether `decision:testcontainers-go` naming testcontainers-go is "referencing an external library" or "stating the decision content" is also category. The rule's spirit covers both readings depending on how you interpret "external."

So this work has two phases:
1. **Resolve the rule-interpretation question upstream** — file the rule-text ambiguity with ok-planner (it's not rimsky's call to make alone; ok-planner is the methodology owner). Until that's settled, every rimsky compliance run will produce different verdicts on Classes B/C/D.
2. **Apply the resolved rule to the corpus** — auto-fix Class A regardless (the v3.0.0 tightening is concrete). Apply the upstream-resolved verdict on B/C/D.

The downside of accepting the strict reading without upstream resolution: rimsky's design corpus loses surface information (CLI grammar paraphrased into prose). The downside of taking the carve-out reading without upstream resolution: every subsequent run flips between approve and find-issues depending on the reviewer dispatched.

## Open questions

- **Should rimsky drive both phases or only phase 2 (after upstream resolves)?** Phase 1 is methodology-owner territory. Rimsky filing a precise issue describing the ambiguity (with the four classes named and the trade-off articulated) gives ok-planner the input to resolve. Then rimsky's phase 2 is purely mechanical against the resolved rule.

- **Does the prior `commit:54f2fcf refactor(design): make concept docs self-contained` work bear on this?** It was a v2.x-era refactor. Reviewing what it did and what cycle-2 still flags on the same files might reveal whether it intentionally accepted Class B/C/D literals (because the rule allowed them at v2.x) or just missed them.

- **Is there value in a project-local extension to the rule** (`.ok-planner/CLAUDE.md` adds: "in this project, CLI verb literals are allowed when cited by a CLI-grammar concept; bundled-service slug references are allowed")? Cleaner than a sweeping reshape, narrower than waiting on upstream. But it splits compliance between project-local and upstream-canonical, which has its own thrash risk.

- **Cycle 2's whole-corpus scope is by design (since 2026-05-26).** Cycle 1 is uncommitted-only; cycle 2 explicitly is not. Confirming this isn't a skill bug — it's the intended shape — so any conversation about "this shouldn't trip on pre-existing drift" is a question for ok-planner, not a skill bug to report.

## Starting data

The cycle-2 reviewer's full finding list (~80 + ~25) is preserved in the 2026-06-13 plumbline plan's task notification context. The fixer for cycle 1 produced rewrites for ~80 of them; the cycle-2 reviewer then surfaced ~25 more (which were not auto-fixed pending this decision).

Concrete files to enumerate fresh in phase 1 of this work:
- Class A (real drift): `concepts/node.md` (backward-looking phrase), plus any others with `## Notes` / dated entries / "previously" / "we plan to" phrasing.
- Class B (CLI grammar): the 6 files named above.
- Class C (tool/license): the 7+ decision files named above.
- Class D (bundled-service stories): the 6+ story files named above.

## What this is NOT

- Not part of the plumbline plan's responsibility. The plumbline plan delivered its stated outcomes (clean lint, active check, proof, design deltas) and closed. The corpus drift surfaced here is independent.
- Not a methodology critique. ok-planner's whole-corpus cycle-2 scope (since 2026-05-26) is deliberate; the v3.0.0 rule tightening is deliberate. This work is about applying the methodology consistently to the rimsky corpus, plus surfacing the genuine rule ambiguity upstream.
- Not a code change. No source files outside `.ok-planner/design/` are in scope.
