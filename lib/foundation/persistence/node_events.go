// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// node_events.go is the persistence-layer interface for the
// rimsky_node_events ledger (migration C6 of the 2026-05-08
// platform-extensions plan). The ledger records executor-emitted
// NamedEvent emissions, indexed by (instance_id, emitter_node_id,
// event_name, emitted_at DESC).
//
// Substitution reads via LatestByName for the
// `nodes.<emitter>.event.<name>.<path>` source kind (plan F4).
// DeleteByInstance is used at instance termination to sweep all rows
// for the instance (and queue blob orphans for any spilled payloads).
//
// @blessed-invariant 21: payload bytes pass through this file inert —
// not logged, not formatted with %v, not normalized.

package persistence

import (
	"context"
	"time"
)

// NodeEvent mirrors a row of rimsky_node_events.
//
// Exactly one of PayloadInline / PayloadHandle is non-nil/non-empty per
// row — the write path in F5/H1 enforces. The read path
// LatestByName returns whichever shape is in the row; callers must
// resolve the handle through the BlobBackend if PayloadHandle is set.
type NodeEvent struct {
	ID                   int64
	InstanceID           string
	EmitterNodeID        string
	EventName            string
	PayloadInline        []byte
	PayloadHandle        string
	PayloadHandleBackend string
	EmittedAt            time.Time
	FrameID              string
}

// NodeEventTable is the rimsky_node_events accessor.
//
// Insert is append-only; the ledger never UPDATEs a row. The row's id
// is auto-generated and returned via the persistence layer's ID alloc
// (BIGSERIAL on postgres, INTEGER PRIMARY KEY AUTOINCREMENT on sqlite).
//
// LatestByName returns the most recent emission for the
// (instance, emitter, event_name) tuple, or (nil, nil) when none exists.
// (nil, nil) — not an error — because "no emission yet" is the normal
// case during early dispatch.
//
// DeleteByInstance is invoked at instance termination; it returns the
// number of rows deleted plus a slice of (handle, backend) pairs that
// were referenced by those rows. The caller (graph/instance
// teardown) is responsible for queueing those handles into
// rimsky_blob_orphans via BlobOrphanTable.Insert.
//
// @concept: named-event
type NodeEventTable interface {
	Insert(ctx context.Context, evt NodeEvent, tx Tx) (int64, error)
	LatestByName(ctx context.Context, instanceID, emitterNodeID, eventName string, tx Tx) (*NodeEvent, error)
	DeleteByInstance(ctx context.Context, instanceID string, tx Tx) (deleted int64, orphans []NodeEventOrphan, err error)

	// @deliberate: DeleteOlderThan deletes rimsky_node_events rows whose
	// emitted_at is before cutoff. The named-event ledger is time-keyed
	// (its frame_id is a non-FK column), so it is reaped by the trailing
	// trace-retention window alone — the count cap applies only to
	// structural frame/node_run rows. Standalone sweep: no caller-supplied
	// tx, run directly against the db handle (mirrors
	// LineageTable.DeleteOlderThan).
	// Returns the number of rows deleted plus the (handle, backend) pairs of
	// any reaped row whose payload had spilled to a blob backend — the
	// caller MUST queue those into rimsky_blob_orphans (as DeleteByInstance's
	// caller does) or the spilled bytes leak. This is the durable-instance
	// path: a long-lived instance's node_events age out by time while the
	// instance is alive, so the instance-delete cascade never reclaims them.
	//
	// concept:named-event trace retention.
	DeleteOlderThan(ctx context.Context, cutoff time.Time) (deleted int, orphans []NodeEventOrphan, err error)
}

// NodeEventOrphan is the (handle, backend) pair returned by
// DeleteByInstance for each row that had a spilled payload. The caller
// queues these into rimsky_blob_orphans for the SweepOrphanedBlobs
// sweep to reap.
type NodeEventOrphan struct {
	Handle  string
	Backend string
}
