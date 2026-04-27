// Package frame implements the frame-resolution engine per
// docs/specs/2026-04-26-frame-resolution-design.md.
//
// The producer helper (EnqueueOrCoalesce) is called by schedule_ticker,
// controlapi/nodes invalidate route, and any other source of an
// invalidation event. The engine (RunTick) is called by the scheduler
// tick under the existing pg_try_advisory_lock(SCHEDULER_TICK_KEY).
//
// Frames are per-instance. Mode is per-template (coalesce | serial_queue).
// Under both modes frames execute one at a time per instance — at most
// one rimsky_frames row in 'running' state per instance, enforced by
// uq_rimsky_frames_running.
package frame
