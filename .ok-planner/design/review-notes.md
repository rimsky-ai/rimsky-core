# Discover-Design Review Notes

Generated during phase 2 review. Distinct from `tensions/` — tensions are about the codebase being muddy; these are about the artifact (the concept/tension catalog itself) being uncertain. The human consumes this file first in `refine-design` before walking the tensions catalog.

## Phase 2 review notes (cycle 2 - final approval)

### Judgment calls

- `event-log`: The two `events` tables (`rimsky_events` audit log vs. `rimsky_node_events` named-event ledger) were merged into a single concept whose Boundaries call out the split. Defensible because both are append-only event tables with distinct opacity disciplines (a unifying frame). A split (one concept per table) would also have been defensible and would have produced a sharper opacity-discipline contrast.
- `observability`: Bundles the optional peer protocols, the cascade-graph HTTP endpoint, the handshake/Discovery cache, and userdata_schema validation under one slug. Splitting out `cascade-graph` (operator dashboard backplane) and `discovery-cache` (the per-peer capability cache that drives `on_event` cross-check at registration) could be defended — the userdata_schema concern in particular is the subject of its own tension.
- `claim-producer` boundaries: The concept treats the protocol-side interface and the bundled reference impls under `stores/` as one. Could be split into `claim-producer-protocol` and `bundled-claim-producer-impls`; current framing leans on the `Store = ClaimProducer` legacy-alias note in the contract.
- `node-state` vs `last-outcome`: Defensible split — they are two columns on the same row, but blessed-invariant logic gates on each independently (dispatch eligibility reads `state`; cascade firing reads `last_outcome`). A merged `node-row-state` concept would also be defensible.
- `named-lock` and `scope` as separate concepts: Defensible split — different conflict mechanics (capacity-counted name vs. byte-equal scope_data) and different acquisition shapes. A unified `lock` concept could also defensibly absorb both as variants.
- `tag` vs `template`: Defensible split — content-addressed hash vs. movable alias have distinct mutability semantics. Could fold tags into `template` as a sub-section.

### Suspected-but-unconfirmed concepts

- `on-event-handler`: Treated as a concept in cross-links (`Adjacent: on-event-handler` in `concepts/lifecycle-handler.md`, `concepts/named-event.md`, `concepts/node.md`) but no concept file exists. Either (a) promote to its own concept file (it has distinct resolve-verdict structure from the four lifecycle handlers — key-indexed map vs. single slot), or (b) fold into `lifecycle-handler` and scrub the dangling slug refs. This is the most concrete promotion candidate from the cross-link integrity scan.
- `frame-stuck`: Named as `Adjacent: frame-stuck` in `concepts/error-policy.md` but no concept file exists. Likely intended to be either an inline mention (the `frame.stuck.observed` slog warning, which lives in `frame`) or a small sub-concept on the advisory frame_timeout mechanism. Either reword the prose or promote.
- `claimant-guarded`: Mentioned with backticks as if a noun in `concepts/lifecycle-handler.md` "claim release (see `auto-terminal`, `claimant-guarded`)". This is an invariant pattern (claimant-guarded release per `@blessed-invariant 4`), not a concept. Could be promoted to a tiny invariant-shaped concept, but more naturally a backtick-stripped inline term.
- `transition-reason`: Mentioned inline in `concepts/node-state.md` ("audit metadata live in `transition-reason`"); tension `transition-reason-vs-last-outcome.md` exists. No concept file. The tension implies it's load-bearing enough that a reader hunting "what is `transition_reason`?" will not find a concept entry. Promotion candidate.
- `discovery-cache`: The in-memory per-peer Capabilities cache populated by the observability handshake, consulted at template registration for `on_event` cross-check. Currently absorbed into `observability`; could be its own concept since it's the structural object that mediates between executor capabilities and rimsky-side validation gates.

### Possible merges / splits to reconsider

- 45 concepts is well over the 15-25 heuristic. Several below are merger candidates beyond the items listed above:
- `mcp-server`: A single optional binary, not a noun the system traffics in at runtime. Could be folded into `control-api` as a one-paragraph sub-section ("agentic MCP shim") with no real loss of information.
- `scenario-harness`: Testing infrastructure (`modeling/scenario/harness.go`). Borderline whether it qualifies as a load-bearing noun versus a tooling fixture. Could fold into `conformance` (sibling test surface) or drop entirely.
- `licensing-boundary`: Repo-organization concern, not a runtime noun. Could fold into `module-layout` (already cited as Adjacent).
- `userdata` vs `userdata-overrides`: Defensible split (overrides are an instance-time merge applied to template-time userdata), but a unified `userdata` concept with an overrides sub-section would also work — overrides are a thin mechanism, not a separable concept.
- `claim` vs `claim-handle`: The cleanest split in the catalog (protocol-side claim object vs. rimsky-side ledger row), but worth flagging because a reader new to rimsky may not understand why claim semantics need two concepts. Annotation that "`claim` is the protocol-layer noun; `claim-handle` is the rimsky-persistence-layer noun for the same conceptual thing" would help — boundaries currently imply this but don't say it directly.
- `node-state` and `last-outcome`: As above; defensible split, defensible merge. Worth a human pass.
- `parked-state`: Borderline. It's a node-state value (`parked`) elevated to its own concept because of the park-payload, resume mechanism, and watchdog timeout coupling. Could fold into `node-state`; current framing leans on the rich mechanism around park.
- `cascade` and `invalidate`: Two concepts that name nearly the same thing from different angles (state-propagation engine vs. the one graph-level message that drives it). Possibly mergeable.
- `claim-producer` Invariants section says "five-method protocol plus `Capabilities()` startup handshake" but lists 5 methods that include `Capabilities`. CLAUDE.md says "4 verbs + the `Capabilities()` startup handshake." Minor count-framing inconsistency worth a sweep (similar in shape to the `handler-slot-count-drift` tension, which catalogs the same kind of issue for lifecycle handlers).

### Thin discovery areas

- Quality rules (`concepts/quality-rule.md`): The Apache spec / AGPL eval split is mentioned in passing in `licensing-boundary` but the quality-rule concept itself reads thin on the eval mechanics — what evaluators exist, what they trigger, how they interact with attribute writeback. A second discovery pass on `modeling/qualityrule/eval/` would help.
- `frame-resolution` vs. `frame` concept boundary: `frame` covers the mode (`coalesce` vs `serial_queue`), but the template-author surface (`frame_resolution:` field) and the runtime mechanism (`rimsky_frames.mode`) blur together. A discovery pass focused on the template-to-runtime mapping for frame resolution would tighten this.
- Conformance suite: `conformance` concept names three conformance binaries but the relationship between them, what each tests, and how the stub-mode gate (`stub-mode-runtime-only-gate.md` tension) plays into it is thin. A discovery pass on `cmd/rimsky-conformance/`, `cmd/rimsky-conformance-probe/`, and `cmd/rimsky-claim-producer-conformance/` together would help.
- Cross-process error semantics: How an executor's `Errored` or `Blocked` propagates through `error-policy` → `lifecycle-handler` → terminal-decision engine is alluded to across `executor`, `error-policy`, `lifecycle-handler`, `auto-terminal`, but no single concept describes the end-to-end flow. Could be a missing concept (`terminal-resolution` or similar) or thin coverage in the existing ones.
- Schedule mechanics: `schedule` is short and references `next_fire_at` advancement and `force-fire`, but the missed-fire policy (no backfill) and its interaction with frames is mostly in tensions and CLAUDE.md gotchas. A discovery pass on `modeling/scheduler/schedule_ticker.go` and the admin force-fire endpoint would round it out.

### Unresolved issues

- Three dangling Adjacent-slug references (`on-event-handler` x3, `frame-stuck` x1) and one dangling backtick-as-noun reference (`claimant-guarded` x1) survived to final approval. They are minor cross-link integrity bugs — concept slugs named in `Adjacent:` lines that have no corresponding concept file. Human consumer can either resolve via promotion (most natural for `on-event-handler`) or by rewording the Adjacent lines.

## Phase 2 review notes (back-edge - final)

### Back-edge outcome
- Thin discovery requests addressed: 4 (quality-rule, frame-resolution, conformance, schedule) + 1 promoted gap (`terminal-resolution`)
- Concepts updated: `quality-rule`, `frame`, `conformance`, `schedule`
- New concepts: `terminal-resolution`
- New tensions: 9
  - quality-rule (2): `quality-rule-severity-string-footgun`, `quality-rule-custom-handler-ordering`
  - frame (2): `frame-lookup-on-every-enqueue`, `frame-resolution-vs-mode-vocabulary`
  - conformance (2): `blob-backend-conformance-fixture-asymmetry`, `stub-mode-signature-no-proto-surface`
  - schedule (2): `force-fire-204-hides-asynchrony`, `coalesced-fire-observability-gap`
  - terminal-resolution (1): `abandon-on-pass-duplicated-path`
- Skipped candidates: 0 (all listed thin areas got coverage; the cross-process error-semantics gap was the trigger for the new `terminal-resolution` concept)

### Back-edge judgment calls

- **Promoting `terminal-resolution` to a concept rather than absorbing into one of the four upstream stages.** Reading the new concept against the four upstream concepts (`executor`, `lifecycle-handler`, `error-policy`, `auto-terminal`), the spine concept *does* add new content rather than recapitulate: the kind→verb table, the two-convergence-points framing (`releaseLocksInTx` per-acquired-lock fan-out, `ResolveClaimHandleTerminal` per-claim-handle), and the explicit `OnAcquireUnavailable` carve-out as the upstream sibling are not stated together anywhere else. Keep. A defensible alternative would have been to add a "see also" cross-reference table to each of the four upstream concepts and skip a dedicated concept — but the spine view is genuinely the load-bearing artifact, and 4-way cross-links would have been weaker than a single owner.
- **Tension `abandon-on-pass-duplicated-path` framing is partially imprecise.** The tension body lists two sites that "do not route through `ResolveClaimHandleTerminal`": (1) `handleAcquireUnavailable` (pre-dispatch) and (2) `applyTerminalPass`. Source inspection shows that `applyTerminalPass` calls `releaseLocksInTx(success=false)`, which *does* route through `ResolveClaimHandleTerminal` for both held and non-held branches (`runner_terminal_release.go:137`). The genuinely duplicated path is the *pre-dispatch* `handleAcquireUnavailable.abandonPartialLocks`, which calls `lk.Store.Abandon` directly at `runner_lifecycle.go:75`. The tension's evidence list and resolution candidates are still pointed at the right files; the body just over-states the second site. Worth a one-line rewording at refine-design time but not a blocker.
- **Inconsistency between `terminal-resolution` and `auto-terminal` invariant 5.** `auto-terminal` invariant 5 says "Unified `ResolveClaimHandleTerminal` is also the entry point for orphan-reaper bail paths and error-policy `pass`/`error` resolutions on already-Open'd claims." `terminal-resolution` observations say the pre-dispatch and pass paths "do not currently route through it". Both are partly right depending on which `pass` site you mean (the `OnAcquireUnavailable` pass vs the `OnExecutorBlocked/Errored` pass). The two concepts use overlapping language for non-overlapping code paths. Adjacent to the tension above; a future refine-design pass should reconcile.
- **Quality-rule severity tension is genuinely a footgun, but very small.** `quality-rule-severity-string-footgun` is "operator typos a severity string and the rule silently blocks instead of warning." Real but low-stakes — the only way to hit it is to write `severity: warn` instead of `severity: warning`. Marginal; reasonable to keep as a low-priority cleanup signal.

### Residual thinness

None survived. The four thin-discovery items from the cycle-2 review notes (`quality-rule`, `frame-resolution` vs `frame` boundary, `conformance`, `schedule`) are now all covered with concrete file:line citations and tensions. The fifth thin-area note ("cross-process error semantics") was promoted to the new `terminal-resolution` concept.

### Suspected-but-unconfirmed concepts (added during back-edge)

None. The four dangling cross-link references identified in cycle 2 (`on-event-handler` x3, `frame-stuck` x1, `claimant-guarded` x1) were not part of the back-edge scope and remain as-is.

### Possible merges / splits to reconsider (back-edge)

- `terminal-resolution` plus the four upstream concepts is 5 concepts for one flow. A reader who wants to understand "what happens when an executor returns Errored" walks five files. Defensible (each owns its stage), but a future refactor that folded the spine's role into a top-of-file diagram in each upstream concept could also work. Worth a human pass at v1.

### Anything else the human should know

- Concept count now 46 (well over the 15-25 heuristic flagged in cycle 2). The back-edge added one and did not consolidate any. The merge candidates in the cycle-2 review notes (mcp-server, scenario-harness, licensing-boundary, claim vs claim-handle, node-state vs last-outcome, parked-state, cascade vs invalidate) are unchanged and still candidates.
- Tension catalog now 39. The two paired stub-mode tensions (`stub-mode-runtime-only-gate` and the new `stub-mode-signature-no-proto-surface`) are distinct in shape: the former is "the gate is opt-in", the latter is "the agreed shape has no schema surface." Not a merge candidate.
- The new `terminal-resolution` concept references `_discover/terminal-resolution.md` (no date prefix, matching the existing un-prefixed `_discover/` entries like `error-policy-retry-loop-cap.md`, `reactive-loops-and-lifecycle-handlers.md`). Consistent with the catalog's existing naming heterogeneity but worth noting.
- This is the final pass; no further loops.
