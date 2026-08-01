# Intent Dossier: named-lock

Distilled 2026-07-13 from session transcripts (2026-06-12..2026-07-13) and ok-planner
history artifacts (2026-05-04..2026-06-11). Transcript tier outranks artifact tier;
later intent supersedes earlier. Part of the drift-remediation intent ledger.

## Net position

- Counting-mode named locks derive eligibility from the claim state that also drives the "running" presentation: only rows with `state='active'` count toward the counting limit — committed/abandoned rows are no longer held and must not count (2026-05-17).
- Every template-referenced named lock must be declared in the config; startup validation fails fast on undeclared references (2026-05-04).
- Named-lock acquisitions are metered by a labeled Prometheus counter (both acquired and unavailable outcomes), distinguishable from producer-claim acquisitions, so lock saturation is graphable/alertable rather than reconstructed from events (2026-06-11).
- Lock names participate in attribute substitution: the auto-subscribe parser scans lock names (and store selectors) for `{{nodes.X.attribute.Y}}` directives so acquisition-time reads are wait-set-backed (2026-05-20).
- A parked node's held claim contends under **regular lock semantics** — contenders queue; no preemption of a parked holder (2026-06-16, transcript).
- Genuinely internal lock machinery (Registry, ModeCoexists, lifecycle registry, late-bind proxies) lives in foundation/locks; the claim-producer contract types' one canonical home is protocols/claimproducer (2026-05-26).

## Required behaviors (open promises)

- Counting-limit predicate is `state='active'` — "committed/abandoned named-lock rows are no longer held; they MUST NOT count against the named-lock counting limit." The CountByNamedLock predicate was flipped from `expires_at > now()` to `state='active'` (2026-05-17, post-data-platform-cleanup-notes, artifact).
- "Running" presentation feeds lock-eligibility counts: a node's running presentation is computed only from the worker-request's claim state (claimed_by non-null in the active phase), never any other source; counting-mode named locks join against this (2026-05-04, foundation-contract, artifact) (artifact-only).
- Config validation: every template-referenced producer name and every template-referenced named lock must be declared in the config; startup fails fast (2026-05-04, modeling-layer-contract, artifact) (artifact-only).
- Auto-subscribe parser covers lock names: `{{nodes.X.attribute.Y}}` directives in lock names (and store selectors) are auto-subscribed, because acquisition-time substitution sites also read attributes — "If those reads weren't auto-subscribed... the substitution context (drained-rows only) would miss it." Load-bearing for the this-frame model (2026-05-20, attribute-pull-resolution-divergences, artifact).
- Labeled Prometheus acquisition counter for named locks, acquired and unavailable both labeled — "lock saturation is an operational condition; events are forensics, metrics are monitoring." (2026-06-11, last-mile-stability, artifact) (artifact-only)
- Parked-node claim contention uses regular lock semantics, exactly as if the node were not parked; no forced release — "regular lock semantice, as it the node weren't parked." (2026-06-16, 055468fc, transcript, user)

## Intentional absences

- **Preemption of a parked claim holder** — rejected; forcing release without consent would contradict park being "hold my claim" (2026-06-16, 055468fc, transcript).
- **claimproducer re-export aliases in foundation/locks** — all 17 removed; protocols/claimproducer is the canonical and only Go home for the contract types, 58 files repointed; no external consumer needs foundation at all (2026-05-26, collapse-sdk-into-protocols).

## Corrections and restorations (drift-fight record)

- **CountByNamedLock predicate flip** (2026-05-17, post-data-platform-cleanup-notes): during execution the counting predicate was corrected from `expires_at > now()` to `state='active'` — a refinement beyond the plan's literal text, ratified in the notes. Precedent: liveness-by-expiry was the drift; state-based counting is intent.
- **Auto-subscribe parser gap fix** (2026-05-20, attribute-pull-resolution-divergences): the parser was extended beyond the plan to scan lock names and store selectors for attribute directives — a structural gap fix, not scope creep.
