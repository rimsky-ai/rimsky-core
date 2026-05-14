# Parked-state dashboard surface

**Date:** 2026-05-13
**Status:** sketch / minor improvement
**Companion sketches:** `2026-05-13-fan-in-conditional-subgraphs.md`

## The gap

`concept:parked` covers nodes paused awaiting time-based wake, signal-based
wake, or watchdog timeout. The state itself is a clean primitive. What's
missing is **observability granularity**: today every parked node looks
alike to `concept:operational-health` and to operators reading dashboards.

Distinct usage shapes that converge on the same state:

- **Time-wait** — rate-limit retry, scheduled resume after backoff.
- **Signal-wait** — awaiting an external event (webhook callback, async API
  completion, message-queue notification).
- **Awaiting-human** — paused for operator approval, sign-off, manual
  intervention.
- **Barrier-wait** — see `2026-05-13-fan-in-conditional-subgraphs.md`;
  waiting for upstream subgraph completion signals.
- **Retry-backoff** — waiting between retry attempts after a transient
  failure.

From an operations perspective these are very different alerts:

- A long-parked rate-limit-retry is normal and expected.
- A long-parked awaiting-human is a paging signal (someone forgot a
  review).
- A long-parked signal-wait might mean the external system is broken.
- A long-parked barrier-wait probably means an upstream subgraph is stuck.

Today they all show up as "parked." Operators have to read the node's
context to understand which case applies.

## Proposal

A small extension to the `Snooze` (formerly `ParkRequested`) executor event:
include a typed `reason` enum.

### Wire change

```protobuf
message Snooze {
  optional google.protobuf.Timestamp resume_at = 1;
  optional bytes payload = 2;
  optional string session_token = 3;
  optional ParkReason reason = 4;       // new
}

enum ParkReason {
  PARK_REASON_UNSPECIFIED = 0;
  PARK_REASON_TIME_WAIT = 1;
  PARK_REASON_SIGNAL_WAIT = 2;
  PARK_REASON_AWAITING_HUMAN = 3;
  PARK_REASON_BARRIER_WAIT = 4;
  PARK_REASON_RETRY_BACKOFF = 5;
  PARK_REASON_OTHER = 99;
}
```

The supervisor stores `parked_reason` alongside `parked_at`, `resume_at`,
and `max_park_duration` on the node row.

### DB

```sql
ALTER TABLE rimsky_nodes ADD COLUMN parked_reason TEXT;
```

Pre-v1, so a baseline migration update rather than a new migration.

### Diagnostics endpoints

`GET /admin/diagnostics/parked-nodes` already exists per `concept:parked`.
Extend with optional `?reason=<name>` filter:

- `?reason=awaiting_human` — surface only human-awaiting parks for a
  "pending review" dashboard.
- `?reason=signal_wait` — surface external-signal-waiting parks for an
  "is the external system healthy" dashboard.
- `?reason=barrier_wait` — surface barrier-pattern parks for a "is the
  fan-in stuck" dashboard.

### `rimsky-cli`

```sh
rimsky-cli parked list                       # all parked
rimsky-cli parked list --reason=awaiting_human
rimsky-cli parked list --reason=signal_wait --older-than=1h
```

### Dashboards

The bundled dashboard in `dashboards/` (per the rimsky repo layout)
distinguishes the categories. Awaiting-human stands out visually (often the
operator-relevant case); time-wait fades into background context.

## Bundled executors that emit reason

- **`barrier`** (proposed in `2026-05-13-fan-in-conditional-subgraphs.md`):
  emits `Snooze{reason: BARRIER_WAIT}`.
- **Rate-limit-aware HTTP executors** (e.g. a `http-node` extension):
  emit `Snooze{reason: TIME_WAIT, resume_at: <when rate-limit resets>}`.
- **Webhook-shaped executors** that emit `AsyncAccepted` and then `Snooze`
  awaiting callback: `Snooze{reason: SIGNAL_WAIT}`.
- **Approval-gate executors** (consumer-built, but conventional):
  `Snooze{reason: AWAITING_HUMAN}`.

The reason is the executor's call. The supervisor doesn't infer it.

## Why this is small

This is a usability paper-cut, not a structural issue. The machinery exists;
we're adding a typed annotation. The work is:

- Proto change + regeneration.
- Schema column.
- Supervisor stores and surfaces the value.
- Diagnostics endpoint accepts the filter.
- CLI accepts the flag.
- Dashboards consume the categorization.

None of this touches the cascade engine, the held-claim machinery, the
scheduler, the executor protocol surface beyond the new optional enum.
Low-risk; high-value-per-line-of-code.

## Open design questions

1. **`PARK_REASON_OTHER` and free-form labels.** Should consumers be able
   to carry a custom string label alongside the enum? E.g.
   `{reason: PARK_REASON_OTHER, custom_reason: "awaiting-tax-bureau-approval"}`.
   Yes — useful for consumer-specific categorization without expanding the
   rimsky-side enum. Add an optional `reason_label` field.
2. **Default reason.** Today's executors don't emit a reason. Migration:
   absent reason defaults to `PARK_REASON_UNSPECIFIED` (visible in
   dashboards as "uncategorized"). Operators can encourage executors to
   adopt explicit reasons over time.
3. **Reason-driven policy.** Should `max_park_duration` be per-reason?
   E.g. `max_park_duration: { time_wait: 1h, awaiting_human: 7d }`. Could
   be useful — different categories have different reasonable bounds.
   Probably yes; add to the node-level config.
4. **Reason transitions.** A barrier-wait that times out and converts to
   a different reason — say it's actually pinging an operator now. Does
   the resume dispatch update the reason on the next Snooze, or is the
   reason fixed at first park? Probably "update on each Snooze" — each
   dispatch can re-park with a different reason.
5. **Lifecycle-subscriber event emission.** Should a `node_parked` lifecycle
   event include the reason? Yes — useful for consumer-side alerting
   integrations.

## Phasing

**Phase 1**: proto + schema + supervisor surface.
**Phase 2**: diagnostics endpoint filter + CLI.
**Phase 3**: dashboard updates.
**Phase 4**: per-reason `max_park_duration` (optional follow-up).

Each phase small and self-contained.

## Sequencing relative to other sketches

The `barrier` executor from `2026-05-13-fan-in-conditional-subgraphs.md`
benefits from `PARK_REASON_BARRIER_WAIT` existing. Ship the reason enum
before or alongside the barrier; don't ship the barrier without the
reason support.
