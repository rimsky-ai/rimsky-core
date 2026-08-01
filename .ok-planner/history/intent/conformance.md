# Intent Dossier: conformance

Distilled 2026-07-13 from session transcripts (2026-06-12..2026-07-13) and ok-planner
history artifacts (2026-05-04..2026-06-11). Transcript tier outranks artifact tier;
later intent supersedes earlier. Part of the drift-remediation intent ledger.

## Net position

- One conformance suite exists per service protocol under the protocols module's conformance package, exposed as `rimsky conformance <protocol>` subcommands of the single rimsky CLI; the seven standalone cmd/rimsky-*-conformance binaries are retired and the rimsky-conformance image runs the subcommands (2026-05-27, root-folder-reorg; 2026-06-08, corpus-bootstrap, artifact).
- Conformance runner logic is an importable shared library; external Go service authors can invoke conformance from their own tests without shelling out; the CLI is a thin wrapper (2026-05-24, repo-reorganization, artifact).
- Conformance is the compatibility gate for protocol implementations outside the rimsky repo: a third-party executor must pass the executor runner against its live endpoint cleanly (2026-05-19, crimefinder, artifact).
- Every written constraint gets a mechanical check that fails on violation: wire contract → conformance suite, layering → lint, invariant → assertion + test, boundary shape → type; lint wins over prose; shared code carries a fast contract suite runnable in isolation — "only testable end-to-end" is a design smell (2026-06-12/13, c41b7afe, transcript).
- The capability handshake is one-shot at startup: rimsky dials each declared peer, calls Capabilities per declared protocol, validates operator-declared properties against the producer-declared envelope, fails fast on mismatch; capabilities are cached for the process lifetime (2026-05-04, service-protocol-contract, artifact).
- Numbered-invariant references must not exist in source code, error strings, tests, or repo-root docs; invariants live in concept docs under descriptive names and diagnostics describe the violated rule in plain language (2026-06-19, a02fe167, transcript).
- There is deliberately no host-agent-proxy conformance suite: protocol transparency means a protocol-conformant service works behind the proxy by construction (2026-06-06, comprehensive-gap-closure, artifact).

## Required behaviors (open promises)

- Claim-producer suite: drives the full claim lifecycle — Open then Commit, Abandon, and Release on real claims, plus a retried terminal verb asserting idempotency — each its own pass/fail row with non-zero exit on any failure (2026-06-06, comprehensive-gap-closure; 2026-06-08, corpus-bootstrap, artifact).
- Claim-producer suite: the serialization-9b probe — concurrent reader Opens against an open writer must detect a producer dishonestly serializing writers on a lock-shaped predicate (the reader-lease pattern forbidden for staged_async), failing it with a named check and passing honest snapshot/MVCC pass-through (2026-06-06; 2026-06-08, artifact).
- Claim-producer suite: envelope conformance — every RealizedWriteSemantics returned by Open must be a member of the Capabilities WriteSemanticsEnvelope; startup validates the operator-declared envelope is a subset of the producer-declared one and fails fast (2026-05-04, service-protocol-contract, artifact).
- Claim-producer suite: SplitScope coverage for the cross-producer contract only — rejection when supports_split_scope is not advertised, round-trip of the {"list": [...]} pass-through shape, and the SubScopeDescriptor wire shape including address and payload; substrate-native shapes are tested in each store's own suite (2026-06-18, 9fb55f08, transcript). Four named checks landed: SplitScopeListReturnsAllElements, SplitScopePreservesPartitionKey, SplitScopePreservesPayload, SplitScopeAddressFieldPresent (2026-06-19, 8e7e4c10, transcript).
- Both bundled stores (filesystem and postgres) advertise supports_split_scope: true in Capabilities (2026-06-18, 9fb55f08, transcript).
- Executor suite, reshaped for the unary protocol: heartbeats, stream-close-without-terminal, and terminal-is-last scenarios deleted; tags_round_trip, attributes_delta_on_error_park, and async_callback_survives_restart added — the restart scenario proves the persistent async-ack registry in CI. "fix everything. implement every missing thing. we want 100%." (2026-06-17, b31002b8, transcript; registry persistence 2026-06-16, 055468fc, transcript)
- Executor suite: park_reason_emission validates Park terminals against the closed reason set {await_callback, snooze}; every bundled executor's stub mode handles the probe_park probe (fixed in claude-agent, then http-node for parity) (2026-06-15, c60b550a, transcript).
- Executor suite follows the async callback to observe real terminals: callback receiver routing by async_ack_id, AwaitTerminal falling through from the gRPC stream on AsyncAccepted, --callback-bind / --callback-host flags (2026-05-08, platform-extensions, artifact-only).
- Supervisor populates prior_dispatch_id / prior_dispatch_disposition on every superseding dispatch (heartbeat-stale recovery, retry-after-error, recalculate), unset on initial dispatches — conformance-tested (2026-05-22, fan-out-safety-scope-first, artifact-only).
- Blob-backend conformance suite validating implementations against the BlobBackend interface contract (2026-05-08, platform-extensions, artifact-only).
- Publisher conformance takes --instance-id because the unified message path is instance-scoped (2026-05-17, sensor-messaging-unification, artifact); its happy-path check is named PublisherHappy after the vocabulary sweep (2026-06-19, 08d65bfe, transcript).
- Persistence conformance: the five RunScope-first must-pass tests (RunScope lifecycle, AffirmNodeRunRow, in-flight lookup by node+scope, state-write isolation by RunScope, recovery-aware dispatch population) run on both Postgres and SQLite (2026-05-22, fan-out-safety-scope-first, artifact).
- The stub executor library scripts the park and named-event surfaces (TypeBuilder.Park, TypeBuilder.EmitNamedEvent; heartbeats first, queued events in order, then the scripted terminal) as the known-good conformance baseline; "stub" is documented as the Meszaros-sense test double (2026-05-08; 2026-05-12, nomenclature-resolution, artifact-only).
- Write-semantics keeps its four-value enum partly because per-value conformance contracts consume it (staged_async requires snapshot reads and forbids reader-lease; read_only requires write rejection at Open) (2026-06-19, a02fe167, transcript).

## Intentional absences

- Standalone conformance binaries (cmd/rimsky-*-conformance) — folded into `rimsky conformance <protocol>` (2026-05-27, root-folder-reorg, artifact).
- The separate sdk/go module as the conformance library's home — dissolved into protocols two days after creation; protocols is the single public-facing implementer module (2026-05-26, collapse-sdk-into-protocols, artifact, reversing 2026-05-24 repo-reorganization).
- A dedicated host-agent-proxy conformance suite — the proxy must pass the existing executor and claim-producer suites unmodified (2026-05-24, host-agent-and-proxy, artifact), and protocol transparency makes a separate suite permanently unnecessary (2026-06-06, comprehensive-gap-closure, artifact).
- The old fanout-disambiguator persistence conformance tests — retired; their cases are inexpressible under (node_id, run_scope_id) unique-index keying; replacement coverage is the RunScope-first suite (2026-05-22, fan-out-safety-scope-first, artifact).
- Executor stream-lifecycle scenarios (heartbeats, stream-close, terminal-is-last) — deleted with the unary reshape (2026-06-17, b31002b8, transcript).
- A claimant guard on sqlite UpdateNodeRunID — deliberate, matching postgres, sanctioned by a conformance carve-out test; the finding claiming it as missing was refuted (finding 1215) (2026-07-13, 3f71f90a, transcript).
- Numbered-invariant references anywhere in source/tests/diagnostics (2026-06-19, a02fe167, transcript).

## Corrections and restorations (drift-fight record)

- The planned MCP conformance-probe extension (--mode=auth-mcp) was never implemented; coverage lives only in an in-process scenario test that misses resources/list method-not-found and the initialize handshake assertion (2026-05-15, control-plane-mcp-and-auth, artifact-only — recorded gap, no later retraction).
- The fused postgres store's automated conformance probe was dropped: the landed test file is documentation-only, leaving no automated assertion that the fused store passes the Executor and ClaimProducer suites (2026-05-19, multi-instance-template-ergonomics, artifact-only — recorded gap).
- Crimefinder execution replaced the testcontainers scenario harness with in-process handler calls, leaving the rimsky wire path unproven end-to-end for that consumer (2026-05-19, crimefinder-divergences, artifact — recorded as drift).
- The conformance concept doc said four standalone binaries when the count was six — corrected to enumerate all six (2026-05-24, repo-reorganization, artifact); the concept was then rewritten for the subcommand model (2026-06-02, rimsky-core-remediation, artifact).
- The CLI docstring claimed the claim-producer suite checks "the four runtime verbs" when it did not — false doc-drift corrected alongside the lifecycle-coverage fix (2026-06-06, comprehensive-gap-closure, artifact).
- The verify-rule lives in .claude/rules/rules.md, not CLAUDE.md as the plan said; invocation updated to `go run ./cmd/rimsky conformance executor --endpoint <executor> --transport grpc`. The conformance CLI handlers gained no dedicated unit tests — covered via the library Run surface plus build/lint/smoke (2026-05-27, root-folder-reorg-divergences, artifact).
- SensorHappy → PublisherHappy and the sensor→publisher role/field sweep across the conformance runner (2026-06-19, 08d65bfe, transcript).

## Superseded / historical

- rimsky-store-conformance → rimsky-claim-producer-conformance (2026-05-04, layer-crystallization); rimsky-<protocol>-conformance naming pattern (2026-05-12, nomenclature-resolution); sensor conformance → rimsky-publisher-conformance (2026-05-17); all binaries → `rimsky conformance <protocol>` subcommands (2026-05-27).
- --check-lifecycle flag on executor conformance driving the six LifecycleSubscriber RPCs (2026-05-04) — historical shape from the standalone-binary era.
- sdk/go as the conformance library home (2026-05-24) — reversed into protocols (2026-05-26); SDK-era tracked duplicates with @source/@diverged tags predate the no-comments/citation-tag regime.
- Executor stream-scenario coverage — superseded by the unary-protocol scenario set (2026-06-17).

## Conflicts needing human ruling

None recorded — the precedence rules resolve the record's tensions on this concept.
