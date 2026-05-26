# Divergence audit — 2026-05-25-concept-doc-self-containment

Plan: `.ok-planner/plans/2026-05-25-concept-doc-self-containment.md`
Spec: `.ok-planner/specs/2026-05-25-concept-doc-self-containment-design.md`
Audited working tree: 70 concept docs + 15 tensions + `concepts.md` + ~24 Go source files.

This is a record of creative choices, not a code review. Expected breadth
(every concept doc touched, 15 tensions touched, TOC regenerated, ~13 Go files
gaining `@concept:` comments) is NOT flagged. All grep-based compliance checks
(concept bodies, TOC, tension Resolution-candidates, cross-ref integrity,
`go build ./...`) pass clean.

---

## Out-of-plan / pre-existing (noted, not implementer divergences)

- `.ok-planner/CLAUDE.md` is modified in the working tree. It predates this run
  and is unrelated to the plan.
- `.ok-planner/plans/2026-05-25-concept-doc-self-containment.md` and
  `.ok-planner/specs/2026-05-25-concept-doc-self-containment-design.md` are
  untracked — they are the plan and spec for this run.

---

## Divergences

### D1 — Stale-slug repoint of `aggregation-policy`: implementer chose `terminal-resolution`, not `cancel-siblings`

**What the plan said** (Task 2 / spec "Stale-annotation cleanup"):
`aggregation-policy` → `cancel-siblings` **or** `terminal-resolution` ("read
both, pick the one the annotated code is about").

**What was implemented:** All `aggregation-policy` annotations repointed to
`terminal-resolution` (`foundation/persistence/run_tree.go`,
`runtime/state_propagation.go`). None went to `cancel-siblings`.

**Inferred reason:** The annotated sites (run-tree state column, child-state
propagation) are about how a parent run's terminal color is decided from
children, which is terminal-resolution's territory, not the sibling-cancel
flag. A within-bounds judgment call the plan explicitly left open.

### D2 — `retention` annotations: split into repoint AND removal

**What the plan said** (Task 2 / spec): `retention` → `claim-lifetime` **or**
`orphan-reaper`; repoint to the obvious successor, or remove if nothing fits.
The candidate-successor framing reads as "repoint."

**What was implemented:** A split. Repointed to `claim-lifetime` at the
claim-handle retention-sweep / delete-resolved sites
(`foundation/persistence/{claim_handles,postgres/claim_handles,sqlite/claim_handles}.go`,
`graph/scheduler/scheduler.go`, `runtime/retention_sweeps.go::ClaimHandlesTrailing`,
`runtime/sweep_claim_handle_retention.go`). **Removed entirely** at two sites
where no successor concept fit: `runtime/retention_sweeps.go` top-of-file
comment, `runtime/retention_sweeps.go::MessageIdempotenciesTrailing`, and
`runtime/sweep_message_idempotencies.go` (message-dedup sweep — a message-TTL
concern, not a claim-lifetime or orphan-reaper one).

**Inferred reason:** Message-idempotency dedup-row retention has no live concept
(neither `claim-lifetime` nor `orphan-reaper` covers it), so the implementer
took the plan's "remove if nothing fits" leg rather than force a wrong slug.
Removal of annotations is a judgment call worth recording.

### D3 — Cross-ref repairs beyond the plan's enumerated mapping

The plan enumerated: `scope`→`claim-scope`, `rimsky-cli`→`rimsky`,
`node-state`→`node-run`, `schedule`→`sensor`, `on-event-handler`→`node-subscription`,
`last-outcome`→`signal`, `quality-rule`→prose, `held-claim`→confirm.

The implementer also made these **active cross-ref repairs not in that list**:

- **`lifecycle-handler` → `terminal-resolution`** in `cascade.md` Boundaries
  ("Does NOT own: terminal-handler resolution (see `concept:terminal-resolution`)"
  and the Adjacent line). `lifecycle-handler` is retired; the Owns/Adjacent
  slot is about terminal-handler resolution, which is terminal-resolution's
  concept. (This is the item the plan flagged as a possible `cascade`'s
  `lifecycle-handler`→? repair — it went to `terminal-resolution`.)
- **`last-outcome` → `signal`** also appears in `cascade.md` Adjacent (the
  enumerated mapping covered `last-outcome` but only as an annotation/`see also`
  case; here it's an Adjacent-line repair).
- **`lifecycle-handler`** cross-refs in `error-policy.md` and `invalidate.md`
  were reworded to prose (no live successor for the slot in those contexts).

**Inferred reason:** These are the same class of dangling-slug-after-retirement
the plan targeted; they surfaced during per-file conversion and were repaired
per the plan's discipline (confirm successor or reword to prose). They simply
weren't pre-enumerated.

Separately, a large set of **retired-slug provenance mentions** were converted
to prose rather than repointed: `userdata`, `opacity`, `worker-request`,
`subscription`, `persistence-driver`, `sensor-watch`, and bare `scope` all
appear in "renamed from `concept:X`" / "the former X concept" Notes lines.
These are NOT cross-refs to repair (no live successor carries that exact role);
rewording them path-free ("the former opacity concept", "renamed from
`persistence-driver`") is correct B2 handling, not a divergence.

### D4 — Section headers restructured beyond `## Annotation sites` in two files

**What the plan said** (conversion discipline): delete the `## Annotation sites`
section entirely. It did not authorize renaming or merging any other section
header; route literals / quoted shell were to be reworded in place.

**What was implemented:**

- `backfill.md`: the `## Control-api` section (5 route-literal bullets) and the
  `## CLI` section (a shell code block) were **merged into a single new
  `## Operator surface` section** that restates the same five operations in
  prose. (`## Annotation sites` separately deleted, as authorized.)
- `lineage.md`: the `## Query surface (control-api)` section was **renamed to
  `## Query surface`** and its 7 route-literal bullets restated as 7 prose
  bullets. (`## Annotation sites` separately deleted.)

**Inferred reason:** Both sections were almost entirely route literals and a
shell block — banned forms that had to go. Rather than leave hollow
path-named headers ("Control-api", "CLI", "control-api"), the implementer
re-titled them to role-based headers and folded content together. No prose
content was lost; the change is structural. Recorded because it exceeds the
plan's literal "only delete `## Annotation sites`" scope for section structure.

---

## Areas checked — no meaningful divergence

- **Pass-1 backfill set:** 13 of 17 candidates annotated at primary load-bearing
  sites (`claim`, `claim-producer`, `claim-scope`, `conformance`, `executor`,
  `graph`, `named-event`, `persistence-database`, `rimsky-yml`, `sdk`, `service`,
  `supervisor`, `write-semantics`). The 4 "no in-repo site" skips
  (`atomic-staging`, `module-layout`, `replica`, `sensor`) match the plan's
  expectation exactly. No stretch placements; each annotation sits on the
  concept's primary type/interface/function. `claim`'s new site
  (`foundation/spec/template.go::NodeStoreRef`) is an addition to an
  already-annotated concept, consistent with the plan's "re-derive fresh"
  instruction.
- **All 5 stale annotation slugs gone:** no `last-outcome`/`run-tree`/
  `held-durable`/`aggregation-policy`/`retention` remain in any `@concept:`
  annotation. Every remaining annotation slug resolves to a live concept file.
  `run-tree`→`run-scope` and `held-durable`→`claim-lifetime` and
  `last-outcome`→`signal` repoints all match the plan.
- **Heavy-file meaning preservation** (`attribute`, `frame`, `lineage-record`,
  `message`, `sdk`, `module-layout`): all citation strips are meaning-preserving.
  Notable removals all authorized: the SQL block in `frame.md`, the
  `## Annotation sites` blocks in `lineage-record.md` and `message.md`. No
  Definition / Purpose / Boundaries / Invariant claim changed beyond losing
  implementation specificity (the accepted B2 trade).
- **Tension Resolution-candidate restatements (15):** every candidate maps 1:1
  to its predecessor; resolution shapes preserved (e.g. "Return 202" → "answer
  with an accepted/queued status"; "Add metric `rimsky_frame_coalesced_total`"
  → "expose a coalesced-fire counter in the observability surface"). The
  "rejected by design" annotations are kept. No resolution's proposal changed.
- **TOC regeneration:** 70 live-concept bullets + 7 retired-concept bullets,
  matching disk. Retired concepts (`last-outcome`, `lifecycle-handler`,
  `node-state`, `on-event-handler`, `quality-rule`, `schedule`, `userdata`)
  were summarized clean rather than copied verbatim — those files' own lead
  sentences carry banned tokens, so the implementer wrote path-free summaries
  for them (the implementer's noted choice; sound). Live-concept summaries are
  path-free restatements close to each file's lead sentence, not always literal
  first sentences (required, since some leads carried bare symbols like
  `ModeCoexists`).
- **Cross-ref integrity:** every genuine `see also:`/`Adjacent:`/`concept:`
  slug resolves to a live file. Remaining grep flags (`scope`, `failed`,
  `fresh`, `kind`, `main`, `payload`, `strict`) are prose-word false positives
  or retired-slug provenance mentions, not dangling cross-refs.
- **Bare protocol-verb / type-name tail forms** (`Open`, `Commit`, `Abandon`,
  `StreamClose`, `ModeCoexists`, `Database`, `Tables`, etc.) remain in some
  concept bodies. The spec explicitly treats these as a tolerated tail form the
  compliance grep can't catch, left to the review-work design-doc cycle. Within
  plan latitude; not a divergence.

## Minor observation (pre-existing, not introduced by this run)

- `frame.md`'s "Open within this concept" cites `tension:frame-resolution-vs-mode-vocabulary`,
  which now lives under `tensions/_resolved/`. The implementer only changed the
  citation form (path → `tension:` slug), not the staleness; the stale
  tension-status reference predates this run and is outside the plan's
  concept-slug-cross-ref repair scope.
