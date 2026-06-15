// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Package frame implements the frame-resolution engine.
//
// The producer helper (EnqueueFrame) is called by the controlapi message-
// emit / invalidate / reset / instance-create routes, the runtime cascade
// path (cascade_invalidate.go), and any other in-process source of a
// frame-creation event. The engine (RunTick) is called by the scheduler
// tick under the existing pg_try_advisory_lock(SCHEDULER_TICK_KEY).
//
// Frames are per-instance and carry a triggering message envelope
// (rimsky_frames.triggering_message_id NOT NULL). Frames execute one at
// a time per instance — at most one rimsky_frames row in 'running' state
// per instance, enforced by uq_rimsky_frames_running. One message per
// frame is the only delivery shape; coalesce retires under the
// message-schema-layer redesign.
package frame
