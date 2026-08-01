---
issue: concepts-missing-what-it-is-heading
kind: audit
category: unspecified
artifacts:
  - design/concepts/*
status: repaired
opened: 2026-07-24T00:00:00Z
---

Question: should the 25 concept files spelling their opening section `## Definition` be swept to the catalog's canonical `## What it is`, and should atomic-staging's missing `## Invariants` section ride along?

Rule that determined the fix: `CONCEPT-TEMPLATE` (`.claude/skills/_shared/artifact-definitions.md`) fixes the opening section's heading as `## What it is` — a single canonical spelling, no alternative — so bringing the 25 dissenting files into line is a heading-shape repair per `MECHANICAL-VS-JUDGMENT-RULE`'s own example ("a heading brought to canonical shape"), not a judgment call.

What changed: renamed `## Definition` to `## What it is` in all 25 files (`asset.md`, `atomic-staging.md`, `cancel-siblings.md`, `claim-co-holdership.md`, `child-execution.md`, `claim-tree.md`, `claim-lifetime.md`, `data-processing.md`, `fan-out.md`, `delegation.md`, `frame.md`, `lineage-record.md`, `graph.md`, `lineage.md`, `message-sender-node.md`, `message.md`, `message-schema.md`, `publisher.md`, `publisher-subscription.md`, `replica.md`, `sensor.md`, `service.md`, `sub-graph.md`, `validation.md`, `terminal-tag.md`); the whole concept catalog (75 files) now reads `## What it is` uniformly. Also reframed `atomic-staging.md`'s bespoke "Substrate atomicity caveats" table into a canonical `## Invariants` section, one bullet per substrate shape — pure repackaging of the same per-substrate atomicity envelopes already stated, no new commitment.

Left undone, deliberately: 15 of the 25 files (`asset`, `atomic-staging`, `cancel-siblings`, `claim-co-holdership`, `claim-lifetime`, `claim-tree`, `data-processing`, `delegation`, `fan-out`, `graph`, `lineage-record`, `lineage`, `message`, `sub-graph`, `validation`) still lack a `## Purpose` section. Unlike the heading spelling and atomic-staging's table, no existing sentence in any of these files states "why this concept exists" for mechanical extraction — writing one is content authorship, not a repair with one rule-determined end state, so it falls outside this repair's scope.

How verified: `grep -rn '^## Definition' .ok-planner/design/concepts/*.md` returns zero matches; `grep -l '^## What it is' .ok-planner/design/concepts/*.md | wc -l` reports 75 (all live concept files). Markdown-only change, no build/test impact.
