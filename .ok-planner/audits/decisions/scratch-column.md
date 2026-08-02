---
audit: scratch-column
artifact: decision:scratch-column
determination: supported
commit: b767a27d
audited: 2026-08-02T09:28:34Z
---

# Executor scratch persists inline-or-spilled on the node-run row

Supported. `rimsky_node_runs` (both postgres and sqlite `001-initial.sql` schemas) carries `scratch_inline`, `scratch_handle`, `scratch_handle_backend` as nullable columns alongside the row's other dispatch state, defaulting empty. `persistence.Queue.WriteScratch`/`LoadScratch` read and write this triple, and the runtime's terminal-scratch write path (`applyTerminalScratchInTx`) applies the same spill-threshold decision (`shouldSpillBlob`) used elsewhere for blob-backed payloads, writing through the same `concept:blob-backend` abstraction on overflow and falling back to inline on a failed spill. A persistence-conformance suite (`scratch_spill_round_trip.go`) exercises both the over-threshold-spills and at-or-below-threshold-stays-inline cases against the real backend, and an end-to-end scenario test (`scratch_round_trip_e2e_test.go`) round-trips scratch through a live dispatch.
