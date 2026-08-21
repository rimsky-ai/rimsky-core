# Concept catalog triage — dispositions against the code

Date: 2026-08-20. Method: five parallel readers, one batch each over all 74
files under `.ok-planner/design/concepts/`. Each reader read its files, read
the alignment findings (`2026-08-20-concept-catalog-alignment-findings.md`),
grepped the `@concept:` citing sites, and judged every factual claim against
the code. Every reader skipped Invariants sections; the owner has since ruled them
out of the catalog entirely, so the sweep's reader test governs their
content, not these per-file verdicts. This document feeds the repair-sweep sprint ruled
in issue `2026-08-20-105405-concept-catalog-carries-non-definitional-content`.

## Verdict

- **Zero stale claims across all 74 files.** Every checked factual claim
  about current behavior matched a code, proto, migration, or config site.
  The catalog's defect is placement and duplication, not drift.
- **Zero deletion candidates.** No file's load-bearing content lives wholly
  elsewhere.
- **68 keep / 1 revise / 0 delete / 5 split**, plus trim candidates inside
  the keeps (below).

## Splits — 5 files

| File | Block to extract | Target |
|---|---|---|
| `attribute` | The whole `## Non-goals` section: five rejected-alternative paragraphs | Decisions. Fold the function-form paragraph into `substitution-grammar-closed` and the multi-pipe paragraph into `substitution-grammar-fallback-routing`; write three new decisions: cross-frame-caching rejection, subgraph-closure rejection, `force_fresh`/`trigger_if_missing` rejection |
| `claim-scope` | The `## Purpose` numbered rationale for byte-equal-conflict as the default (points 1–3) | New decision; nothing in `decisions.md` covers byte-equal conflict |
| `dry-run` | The `## Purpose` user-facing promise (preview any write without committing its side effect) | Existing stories `dry-run-request-flag` and `dry-run-mode-floor`; the section points at them instead of restating |
| `inertness` | "Auth audit log: verbatim request bodies" (lines 40–42): verbatim storage argued safe because the key never lands in the body | New decision; no existing decision covers it |
| `module-layout` | `## Licensing boundary` (lines 45–47) | Existing `decision:licensing-dual-apache-agpl` states the same split near-verbatim; trim to a bare citation |

## Revise — 1 file

- `lifecycle-subscriber` — facts accurate (the seven-method interface at
  `lib/protocols/lifecycle/lifecycle.go:9-19` matches); the Boundaries
  paragraph's literal event-name list duplicates the interface and shrinks
  to a reference. The taxonomy statement stays.

## Trim candidates inside the keeps

Accurate content that duplicates the decision catalog or over-enumerates;
each trims to a pointer under the ruled reader test.

- `node-run` — the seven-state table and transition diagram (lines 28–74)
  restate six existing decisions: `in-place-retry`,
  `held-as-state-not-phase`, `non-cascade-direct-to-stale`,
  `prior-stale-recovery-rename`, `scratch-protocol`, `scratch-column`.
- `node-subscription` — the Owns-section edge-map mechanics restate
  `structural-root-edges-derived-on-demand` and
  `subscription-edges-only-from-explicit-block`.
- `conformance` — accurate but enumeration-heavy (nine scenarios, ten blob
  checks, backend list); trims toward kind-of-thing language.
- `module-layout` — secondary to its split: the dependency/library lists
  trim toward the budget rule.
- `signal` — the CEL-schema-resolution prose (~lines 145–165) is accurate
  but dense; flagged for form review.

Many of the alignment sweep's remaining form findings sit inside Invariants
sections (all of batch 5's, several elsewhere); the owner has ruled those
sections out, so the findings resolve by the sections' removal.

## Keep — 68 files

advisory-lock, anonymous-mode, api-key, asset, atomic-staging,
auto-terminal, blob-backend, breakpoint, cancel-siblings, cascade,
cascade-graph, cascade-mode, child-execution, claim, claim-co-holdership,
claim-handle, claim-lifetime, claim-producer, claim-tree, conformance,
control-api, data-processing, delegation, discovery-cache, error-policy,
event-log, executor, fan-out, frame, graph, host-agent, host-agent-proxy,
instance, lineage, lineage-record, message, message-schema,
message-sender-node, named-lock, node, node-run, node-subscription,
observability, orphan-reaper, parked-state, peer-auth, permission,
persistence-database, publisher, publisher-subscription, rimsky,
rimsky-yml, role-template, run-scope, sensor, service,
service-address-book, signal, sub-graph, supervisor, tag, template,
terminal-resolution, terminal-tag, transition-reason, validation,
wait-set, write-semantics

## Verification gaps the readers named

- `cascade` — 145 citing sites; the reader spot-checked the walk and the
  pure-cascade fallthrough rather than re-deriving them line by line.
- `node` — the claim that tag-substitution failures are fatal at instance
  creation found no single named function; consistent, not contradicted.
- `run-scope` — "kind is derivable, not stored" is confirmed by the absence
  of a `RunScopeKind` enum, not pinned to a line.
- `host-agent-proxy` — the reader checked the registration and routing
  paths but not the collision-rejection path line by line.
