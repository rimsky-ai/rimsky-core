# Intent Dossier: blob-backend

Distilled 2026-07-13 from session transcripts (2026-06-12..2026-07-13) and ok-planner
history artifacts (2026-05-04..2026-06-11). Transcript tier outranks artifact tier;
later intent supersedes earlier. Part of the drift-remediation intent ledger.

## Net position

- Attribute (and event/parked/scratch payload) persistence has a pluggable **BlobBackend**: values below `persistence.blob.spill_threshold_bytes` (default 64KB) stay inline in the row; larger values transparently spill to the configured backend with the handle in the column. Reads are transparent — executors and template authors always see values as values.
- Four backends: **inline** (default), **pg-largeobject**, **filesystem**, **memory**. Cloud backends (s3, gcs, azure-blob, redis) are deferred and operator-implementable against the interface without protocol changes.
- The **memory backend is legal only in the unified single-process topology** (`RIMSKY_PROCESS_ROLE=unified`): cross-process memory blobs are "broken by physics, not policy." Since 2026-06-11 the no-command entrypoint genuinely runs all three roles in one OS process, making the unified stamp truthful.
- Blob content is **inert** (blessed invariant 21) — the spill mechanism is the second named exception to "rimsky doesn't inspect content" after walkPath: bytes move between column and backend and are walked for substitution, never logged, %v-formatted, validated beyond schema gates, transformed, traced, or put in error messages. Blob bytes are byte-opaque inertness; attribute/event values structural inertness (2026-05-12 nomenclature).
- BlobBackend is an in-process Go interface, not a wire protocol — deliberately absent from the host-agent proxy surface.

## Required behaviors (open promises)

- Threshold spill with transparent reads for attributes (2026-05-08, platform-extensions-for-agent-consumers, artifact) and the same spill discipline for named-event payloads (append-only `rimsky_node_events` via NodeEventsStore), parked payloads, and scratch (scratch mirrors the parked_payload inline/handle/handle_backend triple — 2026-06-14, 752fe200, transcript).
- Memory-backend startup rejection unless `RIMSKY_PROCESS_ROLE == "unified"` (2026-05-08, artifact; reaffirmed 2026-06-11, last-mile-stability: the asymmetry with ungated SQLite is deliberate and recorded).
- `SweepOrphanedBlobs`: a dedicated foundation sweep removing orphaned handles (deleted rows, overwritten values) after a retention window (default 24h, configurable), distinct from orphan-claim reaping so cadences can differ (2026-05-08, artifact-only).
- Backend-mismatch reads fall back to the inline data column rather than erroring — continuity over strictness, an accepted silent storage downgrade for that row (2026-05-08, notes, artifact-only).
- Event-payload substitution: most-recent emission of (emitter, event_name) wins at substitution time; no built-in retention on the event ledger (2026-05-11, log-convergence, artifact).
- Genuinely huge/streaming outputs stay on the claim-producer write-channel pattern (acquire write claim, stream through it, emit small attributes_delta) — an explicit big-data choice documented separately from spill (2026-05-08, artifact).
- Compose run (one-shot mode): run state on a sqlite driver at `<runDir>/state.db`, blobs on a filesystem backend at `<runDir>/blobs`, leaving a post-mortem audit artifact openable with stock sqlite3 (2026-06-14, f0176bde, transcript); artifact dirs created 0o700 because state.db and spilled bodies may carry executor stdout, payloads, or claim contents (2026-06-14, f0176bde, transcript); the memory backend was rejected for this path because dangling handle references would gut the audit story — one-shot mode gets no bespoke blob semantics (2026-06-13, 65667e33, transcript).
- Launch-topology hardening direction (ratified sketch, 2026-06-21, ecde6dd1, transcript): the stringly `RIMSKY_PROCESS_ROLE` seam should become a typed Topology parameter on driver/blob config; migrate ownership by literal 'rimsky-control-api' string match should become a typed launcher-plan field; the SQLite-plus-replicas warn-only gate should ride the same typed machinery as the memory-blob hard gate. (Direction ratified; delivery not confirmed in this record.)
- A blob-backend conformance suite (`rimsky-blob-backend-conformance`) validating implementations against the interface contract (2026-05-08, artifact-only).

## Intentional absences

- **Cloud backends (s3, gcs, azure-blob, redis)** — explicitly deferred, operator-implementable (2026-05-08).
- **BlobBackend on the host-agent-proxy protocol surface** — excluded by design; no gRPC surface exists to front (2026-05-24, host-agent-and-proxy).
- **Bespoke blob semantics for one-shot/compose-run mode** — rejected; it reuses the standard backends (2026-06-13).
- **Blob spill as the big-data channel** — spill covers row-too-large-but-single-gRPC-event (~4MB); streaming stays on the claim write-channel pattern (2026-05-08).

## Corrections and restorations (drift-fight record)

- **False unified topology** (2026-06-11, last-mile-stability): the all-in-one entrypoint spawned three child processes while stamping each `RIMSKY_PROCESS_ROLE=unified` — the memory-blob gate's premise was never true (three processes cannot share an in-process map; the scheduler's orphan-blob sweep ran against its own empty map). Fixed: no-command path now runs migrations synchronously then all three roles in one process with single signal-handled shutdown; single-role deployments unchanged.
- **memory-blob-audit-gap tension recorded** (2026-06-13, 65667e33): the memory backend leaves event-row handle references pointing at nothing after process exit — a partially-self-referential audit trail for long-running unified deployments. Recorded as an open tension, not silently accepted.

## Superseded / historical

- Entrypoint-as-three-child-processes with per-child unified stamps → true single-process unified path (2026-06-11).
- "Opacity" vocabulary → "inertness" with byte-opaque vs structural sub-disciplines (2026-05-12).
