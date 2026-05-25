# Divergences — 2026-05-17 sensor-messaging-unification

Working-tree audit comparing the implementation against the literal text
of `.ok-planner/plans/2026-05-17-sensor-messaging-unification.md`. Every
divergence below is something a thoughtful reviewer would want to know
about — small naming/style differences are skipped.

This audit covers only this plan. A separate plan
(`.ok-planner/history/plans/2026-05-17-post-data-platform-cleanup.md`)
also executed in the same uncommitted working tree and its changes
(migration baseline flatten, blessed-invariant 4/22 refresh) are not
attributed here.

---

## 1. Sensor-cron state DB module not created

**What the plan said:** Task 50 — "Add (optional) state DB module for
sensor-cron." Files created: `sensors/sensor-cron/state_db.go` (new).
Per spec §Stage 3 step 2: "sensor-cron's state-DB env-var is plumbed but
in-memory mode remains the default."

**What was implemented:** No `sensors/sensor-cron/state_db.go` file
exists. The directory has only `main.go`, `sensor.go`,
`Dockerfile.sensor-cron`, `sensor_test.go`, `multi_replica_test.go`. The
env-var plumbing for `RIMSKY_SENSOR_CRON_STATE_DSN` is also absent.

**Inferred reason:** Implementer judged the optional state DB
unnecessary given sensor-cron's "in-memory state is reconstructible"
property — but the plan and spec said to add the plumbing anyway (set up
empty/null = in-memory, set = persistent). The task is skipped.

---

## 2. State-DB test fixtures import `internal/pgtest` (root module), not `foundation/internal/pgtest`

**What the plan said:** Task 51 — "The existing pgtest helper lives at
`foundation/internal/pgtest/pgtest.go`, but `foundation-internal-isolation`
depguard rule blocks `github.com/fallguyconsulting/rimsky/foundation/internal`
from external packages. Either extend the depguard's allow-list to
include the sensor packages (preferred — small allow-list edit
alongside the `pgx-isolation` work in Task 52), OR write a minimal
local testcontainers helper." Task 52 — "Find the
`foundation-internal-isolation` depguard rule (lines ~39-46 of
`.golangci.yml`). Extend its allow-list to include the same sensor
packages so they can import `foundation/internal/pgtest` from their
`_test.go` files."

**What was implemented:** A root-module `internal/pgtest/pgtest.go`
already exists (introduced by an earlier plan run) and is the one
the sensors import. The depguard's `foundation-internal-isolation`
allow-list was NOT extended; the pgx-isolation rule's allow-list
already exempts `**/internal/pgtest/**` and `**/sensors/**` so the
existing config covers the new imports. Concretely:

- `sensors/sensor-http/state_db_test.go`,
  `sensors/sensor-object-store/state_db_test.go`,
  `sensors/sensor-webhook/state_db_test.go` all import
  `github.com/fallguyconsulting/rimsky/internal/pgtest` (root module), not
  `foundation/internal/pgtest`.
- `.golangci.yml` was not touched for this plan's changes.

**Inferred reason:** A root-module `internal/pgtest` already exists and
is widely used (~20 importers across runtime/, control/, test/scenarios/,
graph/scheduler/, graph/frame/, subscribers/). It was the natural pick
and didn't require a depguard edit. The plan's assumption that
`foundation/internal/pgtest` was the only fixture was outdated.

---

## 3. Capability-check and idempotency-key unit tests in `messages_test.go` missing

**What the plan said:** Task 12 (Test idempotency-key behavior on
`handleCreateMessage`) and Task 32 (Test capability-check rejection
cases). Eight specific test functions named:

- `TestCreateMessage_IdempotencyKeyDuplicateReturnsExisting`
- `TestCreateMessage_IdempotencyKeyDifferentKeysCreateSeparateMessages`
- `TestCreateMessage_NoIdempotencyKeyCreatesNewMessageEachTime`
- `TestCreateMessage_SenderKindPublisherUnknownSubscriptionReturns403`
- `TestCreateMessage_SenderKindPublisherCrossInstanceReturns403`
- `TestCreateMessage_SenderKindPublisherStoppedSubscriptionReturns403`
- `TestCreateMessage_SenderKindPublisherMissingSubscriptionIDReturns400`
- `TestCreateMessage_SenderKindPublisherActiveSubscriptionSucceeds`

**What was implemented:** `control/controlapi/messages_test.go` contains
only the three pre-existing test functions:
`TestMessages_PostListGet`, `TestMessages_PostInvalidKind`,
`TestMessages_TargetTerminatedInstanceConflict`. None of the new tests
were added. The header comment in
`test/scenarios/sensor/message_routing_test.go:14-16` says "the deeper
capability-check unit tests live in
`code:control/controlapi/messages_test.go`" — but they don't.

**Inferred reason:** Unknown — implementer's DONE report doesn't
mention this. The new behavior on the messages endpoint
(`Idempotency-Key`, `sender_kind: "publisher"`, the 403/400 paths) is
exercised end-to-end by `runtime/sweep_message_idempotencies_test.go`
(idempotency persistence) and
`test/scenarios/sensor/message_routing_test.go` (publisher posts), but
the per-status-code unit-test matrix the plan called for is not present.

---

## 4. `rimsky_messages.sender_kind` CHECK constraint still allows `'sensor'`, not `'publisher'`

**What the plan said:** Spec §File map — "**updates the `sender_kind`
enum at message.md:22-23 from `(operator | sensor | instance)` →
`(operator | publisher | instance)`**". Plan Task 77 step 4 — same enum
update on CLAUDE.md. The wire-side handler in
`code:control/controlapi/messages.go:152` enforces
`sender_kind ∈ {"operator", "publisher"}`.

**What was implemented:** The DB-side CHECK constraint at
`code:foundation/persistence/postgres/migrations/001-baseline.sql:415`
(and the sqlite mirror at `:349`) still reads:
```sql
sender_kind TEXT NOT NULL CHECK (sender_kind IN ('operator','sensor','instance')),
```
A row insert with `sender_kind = 'publisher'` will fail the CHECK on
Postgres and SQLite. No code path in the handler currently writes
`'sensor'`, so the constraint is dead in normal flow — but the wire/DB
enum drift is a real divergence.

**Inferred reason:** The plan's task list focused on the
`rimsky_publisher_subscriptions` table rewrite (Tasks 23-24) and did
not explicitly call out the `rimsky_messages.sender_kind` CHECK
constraint. The implementer updated everything else in the spec's
sender_kind footprint (concept docs, Go handler) but missed the
baseline CHECK.

---

## 5. CLAUDE.md replaced wholesale instead of surgically edited

**What the plan said:** Task 77 prescribed 11 surgical edits to
CLAUDE.md (sweep "Sensor protocol" → "Publisher protocol", rename
`rimsky_sensor_watches` → `rimsky_publisher_subscriptions`, update the
sender_kind enum at line ~154, add idempotency-key gotcha, etc.).

**What was implemented:** CLAUDE.md was reduced from 228 lines (HEAD)
to 41 lines (a "minimal pointer index"). The CHANGELOG entry under
"CLAUDE.md trimmed to a pointer index" explains the rationale at
length: the old CLAUDE.md duplicated content from `.ok-planner/design/`,
`.golangci.yml`, `@blessed-invariant` annotations, and individual
concept docs; the duplication was the drift mechanism. The new file
is an orientation page pointing at those canonical surfaces.

After the rewrite, the file no longer contains any sensor refs (because
it no longer contains the sections that mentioned them) — so all 11 of
Task 77's surgical edits are effectively no-ops. The CHANGELOG entry
documents this as an intentional choice.

**Inferred reason:** Implementer judged the existing CLAUDE.md was
itself an anti-pattern (the in-band content was the drift source) and
chose to fix the upstream cause rather than apply Task 77's surgical
edits. The plan didn't authorize this wholesale rewrite; it specified
specific in-place edits.

---

## 6. `feature-index.md` post-rename surface still cites removed `runtime/sensors.go` + stale "messages + sensors" row

**What the plan said:** Task 63 — "Rename row 78
(`rimsky-sensor-conformance` → `rimsky-publisher-conformance`); delete
row 81 (stale 'Reference sensor binary (cron firing)' row pointing at
the non-existent `cmd/rimsky-sensor-cron/` directory); sweep for
`concept:subscription` → `concept:node-subscription` and
`rimsky_sensor_watches` → `rimsky_publisher_subscriptions`."

**What was implemented:** `feature-index.md` is a NEW (untracked) file
in the working tree; HEAD didn't have one. The file has the new
conformance binary entry (`rimsky-publisher-conformance` at line 78
✓), but other plan-relevant cleanup didn't land:

- Line 51 still says: "messages + sensors | `runtime/message_delivery.go`,
  `runtime/sensors.go`, `runtime/backfill.go` | persistence |
  Frame-boundary message delivery; sensor StartWatch/StopWatch lifecycle;
  backfill operation orchestration." Both `runtime/sensors.go` and
  `StartWatch/StopWatch` are gone post-this-plan.
- Line 59 still says: "templates, instances, nodes, lineage, assets,
  backfills, sensors, messages, observability, admin diagnostics" — the
  "sensors" here referred to the deleted `control/controlapi/sensors.go`
  route handler.

**Inferred reason:** The plan task assumed an existing
`feature-index.md` with row numbers 78/81. The implementer created the
file fresh (likely because the cleanup plan's note 109 had said it
wasn't created). Some of the spec's required edits weren't applied to
the new file.

---

## 7. `deploy/rimsky.yml` uses map syntax for `publishers:`, not the list syntax in the plan

**What the plan said:** Task 62 — "Add four publisher entries to
`deploy/rimsky.yml`":
```yaml
publishers:
- name: sensor-cron
  endpoint: "sensor-cron:9081"
  protocols: [publisher]
```

**What was implemented:** Map syntax keyed by name (matches the
existing `claim_producers:` / `executors:` blocks in the same file):
```yaml
publishers:
  sensor-cron:
    endpoint: "sensor-cron:9081"
    protocols: [publisher]
```

**Inferred reason:** The existing `claim_producers:` and `executors:`
blocks use the map shape in the same file
(`code:deploy/rimsky.yml`); the plan's list shape would have been
inconsistent with the rest of the config. The implementer adopted the
shape the rest of the file uses — likely correct, but a divergence
from the plan's literal text. The Go-side parser (in
`code:control/config/stores.go::RemotePublishersConfig`) likely uses
map shape to match.

---

## 8. New `runtime/clientiface/` directory introduced — plan assumed it pre-existed

**What the plan said:** Task 17 — "Rename `runtime/clientiface/sensor.go`
→ `runtime/clientiface/publisher.go`." The plan assumed the file +
directory already existed at HEAD.

**What was implemented:** The `runtime/clientiface/` directory is
entirely new — git status lists it as untracked. The directory now
holds `publisher.go`, `data_processing.go`, `validation.go`. The
client-interface types that the plan assumed lived in
`runtime/clientiface/sensor.go` actually lived inline in the deleted
`runtime/sensors.go` (verified via `git show HEAD:runtime/sensors.go`:
`SensorClient` interface and `StartWatchRequest` struct were both
defined directly in `runtime/sensors.go`).

**Inferred reason:** The plan's file-tree model was inaccurate. The
implementer extracted the client-interface types into a new
`runtime/clientiface/` directory while doing the rename — a reasonable
shape change, but not what the plan literally said. The pre-existing
`data_processing.go` and `validation.go` in the same dir suggest
multiple related interfaces were extracted at once.

---

## 9. Validation pipeline's per-sensor role check kept the validation-protocol role name `"sensor"`

**What the plan said:** Implicit in the rename sweep — Task 28 (rename
`tpl.Sensors` → `tpl.Publishers`) prescribes "Iteration body may need
adjustment if it referenced `OnObservation` fields." Doesn't explicitly
say what to do with the validation-protocol role string.

**What was implemented:**
`code:runtime/validation_pipeline.go:106` still does
`clientAdvertisesRole(client, "sensor")` (lowercase string literal,
NOT `"publisher"`). The surrounding code iterates `tpl.Publishers`
(correctly renamed) but probes the validation peer for the `"sensor"`
role. Inline comment at lines 100-103 explains: "the inner role name
on the Validation protocol stays `sensor` (sensors are one kind of
publisher; the validation role is kind-shaped)."

**Inferred reason:** The spec's framing is that "publisher" is the
*wire-level* role at the rimsky↔publisher boundary, while "sensor" is
a specific KIND of publisher (and the validation protocol's per-role
surface is the kind-shaped one). Keeping `"sensor"` as the validation
role is consistent with the spec's framing. This is a deliberate
domain-specific choice the user should know about.

---

## 10. `SweepMessageIdempotencies` signature richer than the plan prescribed

**What the plan said:** Task 9 — signature:
```go
func SweepMessageIdempotencies(ctx context.Context, mit persistence.MessageIdempotencyTable, cutoff time.Duration) (int64, error)
```

**What was implemented:** Signature is
`(ctx, mit, cfg RetentionConfig, now time.Time, log shared.Logger)`,
matching the sibling `SweepClaimHandleRetention` more closely. Also
emits structured info logs on non-zero deletions.

**Inferred reason:** Implementer matched the sibling sweep's shape
(per the plan's stated reference pattern), which uses the richer
signature. The plan's stripped-down signature would have diverged from
the sibling.

---

## 11. Concept-doc Task 71 'sensor-watch.md is absent' verified, but plan's framing was wrong

**What the plan said:** Task 71 — "The spec lists `concept:sensor-watch`
as deleted/folded; the file
`.ok-planner/design/concepts/sensor-watch.md` is already absent today
(verified pre-plan). This task is a no-op verification."

**What was implemented:** Correctly verified absent. `publisher-subscription.md`
was created with the folded content.

**Inferred reason:** No divergence — the plan acknowledged this would
be a no-op. Mentioned only because the spec's `## Concept doc changes`
section listed `concept:sensor-watch` deletion as a step (which can
read as a deletion divergence if checked superficially).

---

## Items the implementer flagged that turned out to be non-divergences

The implementer's DONE report surfaced 10 items. Reconciliation:

1. **Sensor-cron state DB deliberately skipped** — confirmed
   divergence; see §1.
2. **State DB tests use root-module `internal/pgtest`** — confirmed
   divergence; see §2.
3. **`@concept: subscription` sweep used perl, not sed** — tooling
   only; non-divergence.
4. **`MessageSenderKindSensor` renamed to `MessageSenderKindPublisher`**
   — matches spec intent; non-divergence.
5. **Validation pipeline kept the `"sensor"` role name** — see §9; a
   deliberate spec-aligned call.
6. **`rimsky_publisher_subscriptions.started_at`** — implementer was
   correct: baseline has `NOT NULL DEFAULT now()` matching the plan;
   non-divergence.
7. **`docs/concepts/README.md` is a minimal pointer index needing no
   edits** — verified; non-divergence.
8. **CLAUDE.md "no sensor refs" claim** — partially wrong. HEAD's
   CLAUDE.md (228 lines) had multiple sensor refs (lines 15, 151,
   153-154, 161, 168-169, 218-219). The implementer's claim only holds
   AFTER the wholesale rewrite was applied. See §5 for the real story.
9. **`last_observed_at` hits in `subscribers/openlineage/`** —
   confirmed unrelated `rimsky_openlineage_cursor.last_observed_at`
   column. Non-divergence.
10. **`StartWatch` in `protocols/proto/v1/gen/validation.pb.go`** —
    confirmed: single hit at line 449 inside a generated comment.
    Non-divergence.
