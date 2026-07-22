# Durable audit-log write path (local spool → batched shipper → Postgres) — Design Sketch

**Date:** 2026-05-30
**Status:** Sketch (not a spec; not authorization to build)

## Idea

The per-request auth-audit write is currently synchronous and Postgres-coupled: after a handler returns, the request goroutine writes the `auth.*` audit row to Postgres inline, under a detached `context.Background()` + 2-second deadline, and **drops the row (logs at Error) if the write fails or exceeds 2s**. Under a healthy Postgres this is a few milliseconds and durable. Under a *degraded* Postgres it is the worst of all worlds at once: every gated request pays up to 2s of latency, holds a pooled DB connection that whole time, and at the ceiling the audit row is dropped anyway. So it is neither low-latency nor actually durable under stress.

Replace it with the standard durable-log shape: the request path appends the event to a **local durable spool** (a per-process SQLite database in WAL mode) — a microsecond-to-low-ms local write that is always available, never touches the network — and a **single background shipper** drains the spool into the Postgres audit table in batched (group-committed) transactions, deleting spool rows once Postgres confirms them. The result: **no request-path latency** (the hot path is a local append, no Postgres round-trip), and **no dropped events** (a row is durable the instant it hits the local spool; Postgres being slow or down only delays shipping, never loses or blocks). The accepted loss boundary is ~200ms of the most-recent events on a hard power loss — fine for this log.

This is explicitly the **audit** log — the forensic, never-drop record — as distinct from a future dev/chatter/debug log. The recurring "degraded Postgres" concern is itself a signal that those two logs want to be separated (see "The two-logs direction" below).

## Shape

### Today (what we're replacing)

```
request goroutine:
  handler runs ──► emitAttempted ──► insertEvent
                                       └─ Tables.Transaction(Background+2s){ Events().Append }  ── Postgres
                                          on err/timeout: slog.Error, DROP the row, return
```

The audit row only ever lives in Postgres, written synchronously in the hot path. `concept:event-log`'s Pass-9 durability invariant ("synchronous in the request path; never silently dropped") describes *this* path — and overstates it, since a timeout/outage does drop (with a log).

### Proposed

```
request goroutine (HOT PATH — no network, no Postgres):
  handler runs ──► emit ──► spool.Append(kind, payload, occurred_at, instance_id, node_id)
                              └─ local SQLite (WAL, synchronous=NORMAL): one tiny INSERT, returns
                                 (sub-ms; always available; durable across process crash)

background shipper goroutine (OFF the hot path, one per process):
  loop (ticker + optional notify):
    rows := spool.ReadBatch(N order by seq)         -- oldest first
    if len(rows)==0 { sleep; continue }
    Postgres.Transaction(synchronous_commit=local){  -- ONE txn, group commit, one fsync / N rows
        Events().AppendBatch(rows)                    -- preserves occurred_at from emit time
    }
    on success: spool.DeleteThrough(rows.last.seq)
    on failure (pg slow/down): keep rows, back off, retry  -- nothing lost, nothing blocked
```

### Components

- **Local spool (new).** A per-process SQLite database, separate file from any other state, opened in WAL mode with `PRAGMA synchronous = NORMAL`. One append-only table, e.g. `audit_spool(seq INTEGER PRIMARY KEY AUTOINCREMENT, kind TEXT, payload TEXT, occurred_at TEXT, instance_id TEXT NULL, node_id TEXT NULL)`. Reuses the pure-Go `modernc.org/sqlite` driver already shipped — no new dependency. WAL + `synchronous=NORMAL` is the durability sweet spot the constraint allows: survives a clean process crash (WAL is on disk), loses only the small trailing window on a hard power loss.
- **Spool writer (new, hot path).** Replaces `insertEvent`'s Postgres write. Builds the same `(kind, payload)` it does today (payload still carries `response_status`/`duration_ms`/`occurred_at`, all known after the handler), and does a single local INSERT. This is the *only* work the request path does for audit.
- **Shipper (new, background).** A single goroutine driven the way the rotation-grace sweep is driven from the scheduler (`code:control/config/scheduler.go`). Reads the oldest N spool rows, inserts them into `rimsky_events` in one transaction with `synchronous_commit` relaxed to `local` (or `off`), then deletes the shipped rows. Ships in `seq` order; the Postgres row's `occurred_at` is the original emit time carried in the spool row, so the audit table reflects when the event happened, not when it shipped.
- **Postgres audit table (unchanged).** Still `rimsky_events`; the audit-read expression indexes (the six `payload->>'…'` partials scoped to `kind LIKE 'auth.%'`) stay on it — and crucially now sit on the *shipper's* batched insert, off the request path entirely. Index maintenance no longer taxes per-request latency.

### Failure modes (the point of the design)

| Event | Outcome |
| --- | --- |
| Process crash after spool append, before ship | Spool persists (WAL on disk); shipper resumes on restart and ships. **No loss.** |
| Hard power loss | Lose only the last ~200ms of spool appends (`synchronous=NORMAL`). **Accepted.** |
| Postgres down or slow | Spool accumulates on local disk; shipper retries; drains on recovery. **No loss, no request latency.** |
| Shipper falls behind (pg up but slow) | Spool grows; request path unaffected. Recent events visible in spool but not yet in `/audit` (read-staleness — see risks). |
| Local disk full | The new hard edge: a local append that can't be made durable. Needs a policy (alert + high-water mark; block vs. last-resort drop). The one place "no drop" meets physics again. |

### The two-logs direction (why "degraded Postgres" keeps coming up)

`concept:event-log` today conflates two very different things in one `rimsky_events` table: high-value, low-volume **audit** rows (`auth.*` access + key lifecycle, and the forensically-meaningful state transitions) and high-volume, low-value-per-row **operational chatter** (lock/work events, debug-grade transitions). They have opposite requirements:

- **Audit log** — must never drop, must be durable, is read for forensics/compliance, tolerates seconds of shipping lag. Wants the spool treatment above.
- **Dev / chatter / debug log** — high volume, individually disposable, fine to be best-effort, async, sampled, or even dropped under pressure. Does *not* want to pay for durability.

"Degraded Postgres is concerning" is really the observation that one store and one write path can't serve both profiles well. This sketch scopes only to the **audit** path. The natural follow-on is to split the concept (and likely the table/sink) so the chatter path stays cheap and the audit path gets durability — at which point the spool is audit-only and its volume is small, making the disk-bound spool trivially sized.

## Open questions

- **`/audit` read-your-write.** Today the synchronous write guarantees a row is in Postgres before the request returns, so a read-after-write on `route:GET /audit` sees it. With a spool, the freshest events are in-flight and not yet in `rimsky_events`. Is bounded staleness (read pg only, accept a few seconds' lag) acceptable for the audit read surface, or does anything depend on read-your-write? (Union-reading spool+pg is possible but adds real complexity.)
- **Which kinds spool vs. write-through.** Only the per-request `auth.*` access rows are on the hot path today. Key-lifecycle rows (`key_created/revoked/rotated`) and operational state-transition rows are written elsewhere, often inside an existing mutation transaction. Do those also move to the spool, or only the access-attempt rows the request path emits? (Lifecycle rows that already ride a mutation txn arguably want the transactional-outbox shape instead — written in the same txn as the mutation.)
- **One spool per process vs. shared.** Each control-api process gets its own spool file and its own shipper draining into the shared Postgres. Cross-process ordering is by `occurred_at` (already non-total in a distributed deployment). Assume per-process; confirm that matches the deployment model.
- **Spool bound + backpressure policy.** What is the high-water mark, what alerts fire, and what happens at the limit (block the request, or last-resort drop with a loud signal)? "No drop" needs a defined behavior when local disk is the constraint.
- **`concept:event-log` invariant rewrite.** This supersedes the Pass-9 durability invariant ("synchronous in the request path"). The new invariant is "durable on local-spool append; shipped to Postgres asynchronously and exactly once; never dropped except on the bounded power-loss window." That concept mutation goes through brainstorm → spec → plan, not this sketch.
- **Batch size / ship cadence / `synchronous_commit` setting.** `local` vs `off` for the shipper; ticker interval; batch N; whether to add `LISTEN/NOTIFY`-style wakeup or just poll. All tunable; defaults TBD.

## Risks / unknowns

- **Read-staleness is a real semantic change**, not just an implementation detail — any consumer that assumed `/audit` reflects an action the instant its request returned would now see eventual (bounded) visibility. Has to be an explicit, accepted contract change.
- **A second persistence engine in the write path.** The spool is SQLite even on a Postgres deployment. It's the driver we already ship, but it adds a local-disk dependency and a local file to operate/monitor/rotate. Operationally heavier than "just Postgres."
- **Exactly-once shipping needs care.** Delete-after-confirm with `seq`-ordered batches is straightforward, but a crash between Postgres commit and spool delete must re-ship idempotently (the existing `Idempotency`/dedup machinery, or a shipped-watermark, prevents duplicates). Get this wrong and you get either duplicates or a stuck spool.
- **Disk-full is a genuinely new failure surface** the all-Postgres design didn't have. Local disk exhaustion now threatens audit capture; it must be bounded and alerted, not discovered.
- **Scope creep into the two-logs split.** This sketch deliberately stops at the audit path. It will be tempting to do the chatter/audit table split at the same time; that's a larger concept change and should be its own decision.

## What this is not

- **Not** the dev/chatter/debug log design. This is audit-only. The split of `concept:event-log` into audit vs. operational logs is named as the natural follow-on but deliberately left out.
- **Not** a transactional-outbox redesign for the lifecycle/state-transition rows that already ride mutation transactions. Whether those move to the spool or to a same-transaction outbox is an open question, not decided here.
- **Not** a change to the `/audit` read surface's filters or gating — only to where/when rows become visible to it (the read-staleness question).
- **Not** a Postgres-as-queue (`FOR UPDATE SKIP LOCKED` + `LISTEN/NOTIFY`) consumer design. That pattern matters if audit events ever need reliable *delivery* to an external sink (SIEM); for durable *storage* the spool-and-ship shape above is sufficient, and the queue mechanics are noted only as the shape to reach for when external delivery becomes a requirement.
