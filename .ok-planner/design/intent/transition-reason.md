# Intent Dossier: transition-reason

Distilled 2026-07-13 from session transcripts (2026-06-12..2026-07-13) and ok-planner
history artifacts (2026-05-04..2026-06-11). Transcript tier outranks artifact tier;
later intent supersedes earlier. Part of the drift-remediation intent ledger.

## Net position

- TransitionReason is a **closed enum of exactly 17 real values** (2026-06-23, transcript), exhaustively enumerated as Go constants with no free-form runtime additions. Docs citing `handler_error`, `infra_reenqueue`, or `handler_resume` cite phantoms: the real parked-deadline reason is `deadline_resume` and running→held is `handler_held`; all live design docs were swept to the real values.
- The enum's primary surviving role is **state-machine validation in NextState**: illegal transitions are rejected with an error, never silently no-opped (foundation invariant 1 — a double `dispatch_claimed` must surface so callers cannot believe two acquisitions succeeded).
- The **audit-write role is split**: signal-bearing transitions write signal type-paths into `rimsky_events.kind`; non-signal transitions (dispatch_claimed, pure_cascade, …) keep writing the TransitionReason kind (2026-05-23, signal-taxonomy).
- TransitionReason and `last_outcome` are deliberately separate vocabularies for the same transition — audit consumers vs the cascade-fire predicate; reconciling them is a recorded open tension (2026-05-11).
- The node-run **creation-reason** enum is a distinct closed set: `cascade`, `operator_invalidate`, `recalculate`, `message_delivery`. `policy_retry` and `infra_reenqueue` are NOT creation reasons; in-place retry fires no state transition at all (2026-06-23, transcript).

## Required behaviors (open promises)

- NextState rejects illegal transitions with an error; re-entering running under `dispatch_claimed` errors (2026-05-04, foundation-contract, artifact): "The foundation does not silently absorb double-claims."
- Reasons written at every transition per the split audit model; absence from the audit row is a defect for the transitions that still write them (2026-05-11, log-convergence, artifact, as narrowed 2026-05-23).
- `policy_give_up` is legal from stale (stale→failed) as well as running — the fix for the swallowed illegal-transition bug under on_acquire_unavailable→error (2026-05-05, reactive-loops notes, artifact).
- Parked arms: running→parked on ParkRequested; deadline resume drives parked→stale (reason `deadline_resume`) with the persisted dispatch-time attribute bag preserved, so a resumed run still sees its dispatch-time snapshot (2026-05-08 correction — resume retargeted to stale so the waking supervisor needn't run an executor pool; outcome reaffirmed through the 2026-06-20 seven-state absorption); parked→failed legal only via the max_park_duration watchdog (2026-05-08, artifact).
- `instance_killed` is a state-machine-validation-only reason (never an audit-event kind) with exactly running→failed and parked→failed arms; the teardown's auditable cause is the single administrative `instance_terminated` event-log row (underscore form) carrying the operator's optional reason (2026-05-28, quality-of-life-features, artifact). fresh/stale runs hold no claim and are left inert on a terminated instance — the narrowing from the spec's broader list is the intent.
- Parent-run machine variant admits transitions illegal for leaves (terminal→stale, terminal→running, running→running) with audited reasons `ReasonChildTransitioned` (child id + new state) and `ReasonSubGraphInternalCascadeFired` (2026-05-15, data-platform-extensions, artifact-only).
- A fan-out parent transitions running→held immediately after dispatching partition children under the dedicated reason `fanout_dispatched` (not `handler_held`, which asserts an executor returned), releasing its queue claim; parent resolution unifies onto the same auto-terminal walker as co-holders (2026-06-22, 10cf843b, transcript).

## Intentional absences

- **A `scheduled` node state** — rejected; resolution flavor lives in the separate `last_outcome` column, keeping the state machine minimal (2026-05-05).
- **`handler_error` / `infra_reenqueue` / `handler_resume` as enum values** — phantoms; not in the 17-value enum (2026-06-23, transcript). (The 2026-05-11 description of ReasonHandlerError as a legal-in-audit dead-end sentinel is superseded.)
- **A distinct `resuming` node-run state, `ReasonDeadlineResume`-as-shipped machinery (NodeStateResuming, IsResume, loadResumeAttributes, migration 015)** — shipped 2026-06-19 then deliberately abandoned and absorbed into the seven-state redesign; the decision parked-resume-distinct-state is retired to _retired/ (2026-06-20, 8a3b8c19, transcript). The user-visible outcome (dispatch loads the persisted bag; deadline resume is parked→stale) survives.
- **`policy_retry` / `infra_reenqueue` as creation reasons** — not in the closed creation-reason set; in-place retry fires no transition (2026-06-23).
- **`instance_killed` arms for fresh/stale** — deliberately narrowed away (2026-05-28).

## Corrections and restorations (drift-fight record)

- **Swallowed give_up from stale** (2026-05-05): the state machine only allowed policy_give_up from running, so the give_up chain silently failed (`ErrIllegalTransition` swallowed); fixed by adding the stale→failed arm. The recorded open follow-up — policy_retry / policy_invalidate still rejected from stale — was never resolved in this record.
- **Concept doc drifted at birth** (2026-05-28, divergences): the transition-reason doc said instance_killed accepts any non-terminal state, but the shipped machine (per the plan's deliberate narrowing) accepts only running/parked. Ruling: the code is the intent; the doc carried the spec's stale wording. Precedent for fix-doc adjudications on this concept.
- **Phantom-reason sweep** (2026-06-23, 10cf843b): live design docs cited nonexistent enum values; all swept to the real 17. Precedent: doc claims about enum membership must be verified against the Go constants.

## Superseded / historical

- Four-state machine (2026-05-05) → five states with parked (2026-05-08) → the seven-state model (2026-06-20 redesign, absorbing held and the abandoned resuming work).
- Spec's parked→running resume → parked→stale (2026-05-08 execution correction), reason later settled as `deadline_resume`.
- TransitionReason as the audit vocabulary for ALL transitions (2026-05-11) → audit role retired for signal-bearing transitions in favor of signal type-paths (2026-05-23).
- ReasonHandlerError as a dead-end audit sentinel (2026-05-11) → not a real enum value (2026-06-23).
