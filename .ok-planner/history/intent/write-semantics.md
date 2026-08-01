# Intent Dossier: write-semantics

Distilled 2026-07-13 from session transcripts (2026-06-12..2026-07-13) and ok-planner
history artifacts (2026-05-04..2026-06-11). Transcript tier outranks artifact tier;
later intent supersedes earlier. Part of the drift-remediation intent ledger.

## Net position

- The write-semantics vocabulary is four values: sync, staged_async, blocking_async, read_only — carried as a producer capability set (write_semantics_allowed, renamed from write_semantics_envelope) and as a per-claim realized value returned by Open and stored on the claim handle.
- The four-value enum is deliberately retained even though the conflict gate consumes only one bit of it: it serves producer capability advertisement, operator narrowing via per-producer write_semantics_allowed, per-claim realization, and per-value conformance contracts (2026-06-19, transcript).
- Byte-equal-scope uniformity: all claim handles with the same (producer, scope-bytes) carry identical realized write-semantics. Consequently the conflict gate only ever answers the same-semantics question, and cross-semantics coexistence is deliberately undefined — it must not be modeled, tested, or documented as a defined case.
- ModeCoexists takes (holder intent, candidate intent, one shared write-semantics value); mvccPassThrough (true only for staged_async) replaces isSync and panics loudly on unknown enum values.
- Envelope conformance: every realized value returned by Open must be a member of the producer's Capabilities set; the operator-declared write_semantics_allowed must be a subset of the producer-declared set, validated fail-fast at startup.
- Blessed-invariant 9b: a staged_async producer must not internally serialize writers on a lock-shaped predicate (reader-lease pattern); honest staged_async requires snapshot delegation or native MVCC pass-through, and the conformance suite actively probes it.
- The bundled filesystem store is sync-only, in-place writes, no atomic staging — a permitted subset, ruled by-design (2026-07-13).

## Required behaviors (open promises)

- Four-value vocabulary on the wire, as capability set and per-claim realized value stored on the handle; claim intent is two-valued "r"/"rw" (2026-05-04, service-protocol-contract, artifact).
- Byte-equal-scope uniformity across a producer's lifetime: two Opens returning byte-equal scope return the same RealizedWriteSemantics; producers enforce, foundation relies on it for the conflict predicate; the coexistence predicate is evaluated at insert time only (2026-05-04, foundation-contract + service-protocol-contract, artifact; reaffirmed as ruling 2026-06-19, a02fe167, transcript).
- Envelope conformance + startup subset validation, fail-fast (2026-05-04, service-protocol-contract, artifact): "`RealizedWriteSemantics` returned by `Open` MUST be a member of the `WriteSemanticsEnvelope` returned by `Capabilities`." A template referencing a write-semantics the producer does not advertise is refused at registration (2026-06-08, corpus-bootstrap, artifact).
- Invariant 9b enforcement in conformance: `rimsky conformance claim-producer` drives the four terminal verbs incl. idempotency under retry plus the serialization-9b probe (concurrent reader Opens against an open writer), failing a dishonest serializing producer with a 9b-named check (2026-06-06 + 2026-06-08, artifact).
- Single-semantics conflict-gate shape: ModeCoexists(holder intent, candidate intent, one shared semantics value); mvccPassThrough true only for staged_async; panic on unknown enum instead of silently defaulting (2026-06-19, a02fe167, transcript, user: "fix it. fix the concept doc, too. fix it all.").
- The write-semantics concept doc models per-value intent-by-intent sub-matrices with an explicit note that cross-value cells do not exist under byte-equal-scope uniformity (2026-06-19, transcript).
- In conflict logic, sync, blocking_async, and read_only behave as the sync block; only staged_async is MVCC pass-through (2026-05-04, layer-crystallization, artifact; confirmed by the 2026-06-19 gate reshape).
- The atomic-staging reference producer exists as a copyable example: Open reserves a private staging area returned with realized staged_async, Commit atomically swaps into canonical position, Abandon drops staging, Release of an uncommitted rw claim ≡ Abandon — the atomicity lives in the producer, not rimsky (2026-05-14, subscription-cascade-and-quality-of-life, artifact; restored per 2026-06-06 as examples/atomic-staging-fs-producer/).
- Terminal-atomic-commit: the settling verdict, the attributes_delta writeback, and tags persistence ride the same caller-provided transaction and commit together (2026-06-17, b31002b8, transcript).
- Attribute writeback and scratch writes bump last_progress_at in the same transaction as the write itself (2026-06-17, b31002b8, transcript).
- Fan-out intent inheritance honored end-to-end (STORY-fanout-intent-inheritance): intent: r on a fan-out claim means each sub-claim Commit performs no write-back; intent: rw exhibits the write-back (2026-06-18, 9fb55f08, transcript, user-accepted story).
- The claude-agent sign-off gate cryptographically binds the run's real effective bound output (terminal-final delta merged with accumulated incremental writebacks) regardless of emission path (2026-06-06, comprehensive-gap-closure, artifact — fixing the signature-over-"null" bypass).

## Intentional absences

- Cross-semantics coexistence semantics: deliberately undefined; TestModeCoexistsCrossQuadrant was deleted as pinning an undefined region (2026-06-19, transcript).
- Atomic staging in the bundled filesystem store: deliberately absent; it does in-place writes and advertises sync-only from day one; concept:write-semantics permits the sync-only subset. The story claiming an atomic staging swap was overstated at corpus bootstrap — adjudicated fix-doc, finding 1763 (2026-07-13, 3f71f90a, transcript).
- Reader-lease serialization for staged_async: forbidden by invariant 9b (2026-05-04, artifact).
- The single-value write_semantics: YAML shortcut and the write_semantics_envelope key name: retired (2026-05-12, nomenclature-resolution, artifact — see rimsky-yml dossier).
- A write-semantics field on ClaimProducerObservability.Capabilities: the envelope is served on ClaimProducer.Capabilities (WriteSemanticsAllowed); the observability message has no such field (2026-05-24, host-agent-and-proxy divergences, artifact).

## Corrections and restorations (drift-fight record)

- The loose-ends sketch framed "the 4-value enum collapses to 2 at the conflict gate" as a defect; archaeology showed the collapse is exactly what the 2026-05-04 specs called for. The actual drift was the concept doc and TestModeCoexistsCrossQuadrant asserting outcomes in the deliberately-undefined cross-semantics region. Ruling: fix the doc and the test, keep the gate binary, keep the enum (2026-06-19, a02fe167, transcript). Key adjudication precedent: verify "drift" findings against the original specs before ruling fix-code.
- The stage-then-swap filesystem reference producer was built (e1487e1) then deleted wholesale (c1ce756); ruled restore — shipped as a fresh Apache-licensed examples module (2026-06-06, comprehensive-gap-closure, artifact). Note: this is the standalone example; the bundled filesystem store's sync-only posture (2026-07-13) is a separate, by-design fact.
- The quickstart stub config's write_semantics_envelope key was silently dropped by the parser — silent config drift fixed by the rename (2026-05-13, nomenclature-resolution, artifact).
- No-op-commit expectations were rebased under the pessimistic-invalidate model: "dependent still fresh after no-op commit" is an old-model assertion; what is preserved is the event kind, absence of attributes_committed, and the idempotent cascade back to fresh (2026-05-14, artifact).
- The claude-agent sign-off gate verified a signature over the literal "null" on the incremental path — silent security bypass, fixed to bind the real effective output (2026-06-06, artifact).

## Superseded / historical

- Old constants Direct / StagedBlocking → sync / blocking_async, read_only added, staged_async carried forward (2026-05-04, layer-crystallization).
- write_semantics_envelope → write_semantics_allowed; single-value shortcut retired (2026-05-12).
- Postgres items-table queue mode default direct → staged_async (2026-05-04, layer-crystallization) `(artifact-only)`.
- isSync helper with silent default-true on unknown values → mvccPassThrough with loud panic (2026-06-19).
- The sketch's remediation ideas of expanding the gate or collapsing the enum → narrowed ModeCoexists signature making cross-semantics input unrepresentable (2026-06-19, transcript).
