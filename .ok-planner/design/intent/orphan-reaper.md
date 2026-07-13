# Intent Dossier: orphan-reaper

Distilled 2026-07-13 from session transcripts (2026-06-12..2026-07-13) and ok-planner
history artifacts (2026-05-04..2026-06-11). Transcript tier outranks artifact tier;
later intent supersedes earlier. Part of the drift-remediation intent ledger.

## Net position

- Heartbeat-keyed liveness is retired entirely (2026-06-16/17, transcript): heartbeats are dishonest by construction (a sidecar goroutine can beat while the real task stalls). Migration 013 dropped last_heartbeat_at from rimsky_node_runs, rimsky_supervisors, and rimsky_claim_handles; the Heartbeat event type is removed from the executor protocol.
- The reaper is re-keyed: sync dispatches key on the gRPC client connection state in-band (the supervisor's client failure drives in-band cleanup); async dispatches key on persistent last_progress_at + dispatched_at checked against per-row deadlines (max_quiet_period, max_runtime). Quiet-period dispatch-level reaping is the preferred dead-supervisor mechanism; re-assignment is the normal pending-queue claim path.
- last_progress_at is fed by two keepalive channels: the dedicated POST /v1/runs/{run_id}/keepalive endpoint, and attribute writebacks counting as incidental keepalives (bumped in the same transaction as the write). Keepalive is not tied only to attribute writes.
- Every reap path is claimant-guarded (invariant 4): no path removes a holder's row without holder verification; verify-before-run (invariant 5) makes the racing runner bail without acting on the claim.
- The periodic reaper never calls producer verbs (Commit/Abandon/Release); producers own their own state TTL. The unified terminal-resolution engine is the single audited verb-then-delete site; the reaper is not it.
- Sweeps keep split-by-purpose naming (SweepOrphaned<Noun>, etc.); there is deliberately no unified single orphan reaper and no umbrella SweepOrphans.
- The reaper's claim-handle candidate predicate is state='active' AND expires_at < now(); held-durable preservation flows structurally from the state filter. Parked rows leave phase='active' and are never reaped. rimsky_api_keys is never touched.

## Required behaviors (open promises)

- Claimant-guarded deletion everywhere (invariant 4): every operation deleting a claim handle or nullifying claimed_by is conditioned on holder = expected; stale sweeps and verb-driven releases share the guard (2026-05-04, foundation-contract, artifact): "The foundation MUST NOT expose any path that removes a holder's row without holder verification."
- Verify-before-run (invariant 5): immediately before invoking a service acting on a claim, the runner re-reads claimed_by and bails if ownership moved (2026-05-04, foundation-contract, artifact).
- Cross-boundary cleanup by reapers on both sides, not two-sided commit (invariant 10): acquisition is atomic on the foundation side; any orphan state on either side is eventually cleaned up (2026-05-04, foundation-contract, artifact).
- No producer verbs from the periodic reaper; producer-side TTL owns producer cleanup (2026-05-04, foundation-contract, artifact; reaffirmed 2026-06-11 last-mile-stability: "the periodic reaper continues to fire no producer verb").
- Quiet-period / runtime deadlines: SweepExecutorDeadlines releases claims where quiet time exceeds per-template max_quiet_period or total time exceeds max_runtime; sync liveness is the RPC connection plus a deadline (2026-06-16, 055468fc + 2026-06-21, 21306ffe, transcript, user: "the new mechanism is quiet-period. which we like better than heartbeat").
- Keepalive channels: dedicated keepalive endpoint plus writebacks bumping last_progress_at, atomically with the write (2026-06-16, 055468fc, transcript, user: "attribute write can count as a keepalive … fine to also have a dedicated endpoint"; 2026-06-17, b31002b8, transcript).
- Parked rows are un-reapable: parked worker_request rows leave phase='active'; on resume the row returns to active with a fresh claimant and verify-before-run applies as on first dispatch (2026-05-08, platform-extensions, artifact).
- Held-durable claims skipped structurally: state filter (active-only candidates) preserves committed/abandoned and durable rows; durable claims release only on explicit operator action or instance termination (2026-05-15 + 2026-05-17, artifact).
- rimsky_api_keys exclusion (2026-05-15, control-plane-mcp-and-auth, artifact).
- SweepOrphanedBlobs as a distinct sweep with its own retention window (default 24h, configurable) and cadence (2026-05-08, platform-extensions, artifact).
- Ownership-bail unification: the verify-before-run bail path (handleOrphanedClaim) resolves through the unified claim-handle resolution engine as source kind OwnershipBail; its hand-rolled verb-then-delete site is deleted; the acquire-unavailable handler remains the single named carve-out (acquisition tx already rolled back), pinned by a deterministic injection test (2026-06-11, last-mile-stability, artifact).
- Race-honest testing: deterministic race-injection hooks pin the orphan-reaper vs in-flight-terminal overlap among the four defended seams; make test-all carries a -race slice, test-race runs -count=3, release requires it (2026-06-11, last-mile-stability, artifact).
- Breakpoint housekeeping piggybacks on the reaper cadence (no new ticker): SweepExpired deletes past-TTL breakpoints; AutoResumeStale auto-resumes stale hits with a structured WARN (2026-05-24, instance-debugger, artifact).
- Supervisor-crash recovery through the reaper: dispatch rows reclaimed and picked up by another supervisor, invisible to the host-agent proxy (2026-05-24, host-agent-and-proxy, artifact).

## Intentional absences

- Heartbeat liveness in any form: the Heartbeat protocol event, the heartbeat ticker, SweepStaleHeartbeats, the 5x-heartbeat_interval cutoff (old blessed-invariant 6), the last_heartbeat_at columns, and the deprecated HeartbeatTimeout compat field are all gone (2026-06-16/17, transcript). The claim-handle expires_at TTL math survives under the liveness vocabulary (liveness-extend, LivenessInterval, tickLiveness).
- A single unified orphan reaper: the spec'd unification was deliberately not implemented — the two reapers act on different tables with different staleness predicates; unification is documentation-level only (2026-05-04, layer-crystallization plan-notes, artifact).
- An umbrella SweepOrphans function: split-by-purpose naming is the convention (2026-05-12, nomenclature-resolution, artifact).
- Reaping held-phase worker-requests at the worker-request level: never done (2026-05-04, foundation-contract, artifact).
- Producer verbs during reap: never (see above).
- A held_durable bool clause in the reaper predicate: replaced by the structural state filter (2026-05-17, post-data-platform-cleanup, artifact).

## Corrections and restorations (drift-fight record)

- Heartbeat remnants after the retirement (HeartbeatLostPayload proto message, HEARTBEAT_LOST enum value, the stale heartbeat-cutoff-asymmetry tension) were ruled incomplete-cleanup residue and removed with proto field numbers reserved; the host-agent-to-supervisor heartbeat is a different surface and stays (2026-06-24, 8a8539a4, transcript, user). Precedent: post-retirement remnants are defects, not compat.
- The user's frustration at surviving heartbeat vocabulary ("why is heartbeat still there to confuse us?") drove the confirmation that quiet-period is the mechanism of record (2026-06-21, 21306ffe, transcript).
- The reaper-vs-bail-abandon-asymmetry tension was resolved by folding handleOrphanedClaim into the unified engine (2026-06-11, artifact), superseding the 2026-05-11 decision to keep it a separate carve-out.

## Superseded / historical

- Invariant 6 (dead runner = no heartbeat within 5x heartbeat_interval; OrphanedClaimTimeout shared cutoff) → retired in favor of max_runtime / max_quiet_period deadlines (2026-06-17, transcript).
- SweepStaleHeartbeats → removed with the heartbeat mechanism (2026-06-17).
- handleOrphanedClaim as a third Abandon carve-out using abandonOpenedClaim (2026-05-11, log-convergence) → folded into the unified engine as OwnershipBail (2026-06-11).
- Heartbeat pause on durable claims as the reason for the reaper skip (2026-05-15) → moot once heartbeats retired; the structural state filter carries the guarantee.
