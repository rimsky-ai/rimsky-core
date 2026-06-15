# Bundled-service stories: true-user-stories vs captured-task drift — Design Sketch

**Date:** 2026-06-14 (origin); 2026-06-15 (scope narrowed)
**Status:** Sketch (not a spec; not authorization to build)
**Origin:** surfaced by the post-execute-plan `review-work` cycle 2 (design-doc compliance) run during the closing of `plan:2026-06-13-plumbline-comment-hygiene-sweep`. The plumbline plan never modified the flagged files — this is pre-existing structural drift in the rimsky design corpus that the cycle-2 reviewer caught.

## Idea

The rimsky design corpus carries six story files — `stories/store-filesystem.md`, `stories/store-postgres.md`, `stories/sensor-cron.md`, `stories/sensor-http.md`, `stories/claude-agent.md`, `stories/mcp-transport.md` — whose bodies and filename slugs name a **specific bundled service**. Under the project's design-doc methodology, this is a category drift, not a true user-story shape:

- **Concepts** are general — load-bearing nouns, definitions, purposes, boundaries, invariants. Not implementation detail.
- **Decisions** record technical tradeoffs (Choice + Rationale + Alternatives). The specific artifact picked to satisfy a tradeoff is incidental scaffolding; the tradeoff itself is the content.
- **Stories** are intended as **true user stories** — role / capability / business value / acceptance / falsifier / proof, where the capability is the user-outcome and the surface a user reaches through is owned by the relevant decision (a CLI verb / HTTP route / wire message / scheduled job lives in `decisions/`, not in the story).
- **Specifics live in specs, code, and other documentation. NOT in `design/`.**

A story file named `stories/store-postgres.md` whose body says "as a maintainer, I can use the bundled postgres store to persist runs" is a **captured task in the shape of a story**, not a true user story. The user-outcome ("I can persist runs to a relational backend with the queue paths intact under load") is general; the bundled-service-name is implementation detail that belongs in a decision (e.g., `decisions/store-postgres-bundled.md` recording the tradeoff between using a bundled postgres backend vs. an external one, with its rationale and alternatives).

Rewrite each of the six story files into the true-user-story shape: a general user-outcome plus its acceptance/falsifier/proof, with the specific bundled-service choice extracted into one or more sibling decision files. Update `@story:` annotation sites in code to point at the new general story slugs.

## Shape

### Today (what we're replacing)

Six story files name specific bundled services:

| Story file | Names | Likely true-story extraction |
|---|---|---|
| `stories/store-postgres.md` | the bundled postgres store + protocol verbs | "as a maintainer, I can persist runs to a relational backend so that…" + decision `store-postgres-bundled` |
| `stories/store-filesystem.md` | the bundled filesystem store + claim-producer protocol surface | "as a maintainer, I can run rimsky without a relational backend (single-node)…" + decision `store-filesystem-bundled` |
| `stories/sensor-cron.md` | the bundled cron sensor | "as a publisher, I can schedule sensor invocations on a time cadence…" + decision `sensor-cron-bundled` |
| `stories/sensor-http.md` | the bundled HTTP-poll sensor | "as a publisher, I can poll an HTTP endpoint for messages…" + decision `sensor-http-bundled` |
| `stories/claude-agent.md` | the bundled agentic executor + the underlying tool + MCP-catalog wire formats | "as a publisher, I can run an agentic executor against my templates…" + decisions for catalog mechanism, transport, env-var resolution |
| `stories/mcp-transport.md` | the MCP client product name + the executor-MCP surface | "as a publisher, I can register an executor that speaks MCP…" + decision recording the MCP-transport choice |

These were authored in earlier discover-design / refine-design / execute-plan runs that didn't fully internalize the story-vs-decision split. The cycle-2 reviewer flagged them as "naming external library / wire-format / service-image identifiers" — which is the right call structurally, but the simple text-fix (replace the literal with prose) doesn't address the underlying shape: a true user story shouldn't be reaching for the literal in the first place.

Each story file also has `@story: <slug>` annotation sites in code (per `git grep '@story:'`). Those annotations point at the literal `store-postgres` / `claude-agent` / etc. — they'll need retargeting when the stories are reshaped.

### Proposed

A spec with a `## Manifest` carrying:

- **One general user story per bundled service surface**, expressing the user-outcome at the level the user actually experiences ("persist runs to a relational backend", "schedule sensor invocations on a time cadence", "run an agentic executor against my templates"). Each story keeps the acceptance / falsifier / proof shape; the proof artifact stays at its current test path (`test/scenarios/`-shaped) but is described in terms of the user-observable outcome, not the bundled service identity.
- **A sibling decision per bundled-service choice** recording the tradeoff that ELECTED that service for the bundled tier — Choice ("use the bundled postgres backend for the relational-persistence story"), Rationale (why postgres vs. mysql vs. a non-relational option), Alternatives (the others considered).
- **Retargeting of `@story:` annotation sites in code** from the old specific slugs to the new general slugs. (Per-site sweep similar to the plumbline plan's per-module passes; well-bounded mechanical work.)

The TDs on the spec capture the structural decisions:

- **TD-bundled-services-extract-to-decisions** — bundled-service-name belongs in a decision (where the tradeoff lives), not in a story (where the user-outcome lives).
- **TD-story-slugs-name-outcomes-not-products** — `stories/<slug>.md` filenames name the outcome being delivered, not the artifact delivering it.
- **TD-annotation-retarget-mechanical** — `@story:` annotation sites in code retarget per-site without rationale rewriting; the annotation marks where the new story is enforced, not what its content is.

## Context: A/B/C from the cycle-2 batch are already cleaned

For reference: the cycle-2 reviewer surfaced ~25 findings split across four classes. Three of them (A — incidental literals like HTTP routes, env-vars, license-text section IDs; B — CLI-grammar concepts that were enumerating literal verbs; C — tool/license/library-choice decisions that were naming the chosen artifact in the Choice section) were applied during the 2026-06-15 follow-up to the plumbline plan's closing — the rewrites are role-description shape, same pattern as the plumbline plan's cycle-1 fixer work. This sketch's scope is ONLY the fourth class, the bundled-service stories.

The applied A/B/C work also incorporated the methodology framing more explicitly than the original cycle-2 reviewer's framing did: not "literal X is disallowed by the rule" but "literal X is implementation detail that lives outside `design/`; the concept / decision should express its content at the durability altitude, with specifics in specs, code, or other docs." That framing settled the borderline cases the reviewer self-flagged.

## What was layered into the original surface (now resolved)

The cycle-2 reviewer's initial finding set also reflected an ok-planner rule tightening at v3.0.0 (2026-06-11) that added a "Current-state only" sub-rule. That tightening is genuine and applies to: `## Notes` / `## History` / `## Changelog` sections, dated audit-trail entries, backward-looking phrasing ("previously", "(was 60s)"), forward-looking phrasing ("we plan to", "TODO"). Cycle-1 of the design-doc compliance fixer (and the follow-up A/B/C pass) caught the rimsky cases of this drift (e.g. `concepts/node.md:24`'s "also numbered §17" phrasing). No known residual drift of this class remains as of 2026-06-15 — confirm by re-running cycle 2 if a fresh signal is wanted.

## Open questions

- **Story split granularity for `claude-agent.md`.** The current story conflates several user-outcomes (catalog mechanism, sign-off gate, error classes, validator-header secret refs). The natural extraction is multiple general stories — one per user-observable outcome — plus per-mechanism decisions. The spec phase will need to surface these.

- **Should `mcp-transport` survive as a story, or migrate entirely into decisions?** "I can register an executor that speaks MCP" is a meaningful user-outcome; "we use this specific MCP client product" is a decision. The split needs walking case by case.

- **Spec timing.** The bundled-service tier is fairly settled; this refactor is documentation-shape, not behavior change. Whether to drive it via `/brainstorm` now or wait until adjacent design corpus work surfaces it (e.g., the still-open `S-`-prefix promotion conversation from the plumbline plan's closing) is a sequencing call.

- **Upstream methodology surface.** The "story is non-prescribed user outcome; decision owns the surface and the choice; specs/code own specifics" framing is consistent with ok-planner's `discover-design` SKILL.md (which says "the delivery surface is not part of the story; which surface a user reaches through — CLI verb, HTTP route, wire message, scheduled job, UI — is a technical choice and lives in `decisions/`"). The drift in the rimsky corpus is not a methodology disagreement; it's pre-existing corpus debt that should be cleaned per the methodology that already exists. No upstream change is implicated.

## Starting data

Six story files, file paths above. `@story:` annotation sites in code can be enumerated with `git grep -nE "@story:\s*(claude-agent|store-postgres|store-filesystem|sensor-cron|sensor-http|mcp-transport)\b"` to scope the per-site retargeting phase.

## What this is NOT

- Not a code-behavior change. The bundled services keep working as they do. This is corpus shape.
- Not a methodology debate. The story-vs-decision split is already canonical in ok-planner's `discover-design` rules; the rimsky stories drifted from it during their original authoring.
- Not part of the plumbline plan's responsibility. The plumbline plan's outcomes are delivered and committed. This is pre-existing structural debt with its own spec.
