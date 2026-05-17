// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// lineage_writer.go — E8. Lineage writer.
//
// Spec
// .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
// §Content lineage. At every leaf-run terminal and every claim-handle
// Commit, append a record to `table:rimsky_lineage`. Bytes are inert
// (@blessed-invariant 20/21); rimsky stores hashes, run identifiers,
// frame identifiers, and the per-kind payload as opaque JSON.
//
// @concept: lineage
//
// Append-only — never updated. The retention sweep in E10 deletes rows
// whose corresponding run / claim_handle has been removed AND whose
// observed_at is older than the retention window.

package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/shared"
)

// LeafRunRecord is the JSON payload of a "leaf_run" lineage row. Field
// names match spec §Content lineage / Leaf-run record shape.
type LeafRunRecord struct {
	RunID            shared.UUID    `json:"run_id"`
	NodeID           shared.UUID    `json:"node_id"`
	FrameID          shared.UUID    `json:"frame_id"`
	ChildKey         string         `json:"child_key,omitempty"`
	ParamsHash       string         `json:"params_hash,omitempty"`
	UserdataHash     string         `json:"userdata_hash,omitempty"`
	ScopeDataHash    string         `json:"scope_data_hash,omitempty"`
	State            string         `json:"state"`
	LastOutcome      string         `json:"last_outcome"`
	ErrorClass       string         `json:"error_class,omitempty"`
	SubstitutionRefs []string       `json:"substitution_refs,omitempty"`
	Extra            map[string]any `json:"extra,omitempty"`
}

// ClaimTerminalRecord is the JSON payload of a "claim_terminal" lineage
// row. Fires at every claim-handle terminal — Commit (`outcome:
// committed`), natural Abandon (`outcome: abandoned`), and force-
// cancelled Abandon (`outcome: force_cancelled`). The `Cause` field
// further distinguishes force-cancellation provenance (sibling-cancel
// vs descendant-cancel) on `force_cancelled` rows so post-mortem
// queries can reconstruct the actual cancellation walk.
type ClaimTerminalRecord struct {
	ClaimHandleID       shared.UUID    `json:"claim_handle_id"`
	RunID               shared.UUID    `json:"run_id"`
	NodeID              shared.UUID    `json:"node_id"`
	FrameID             shared.UUID    `json:"frame_id"`
	ParentClaimHandleID *shared.UUID   `json:"parent_claim_handle_id,omitempty"`
	ProducerName        string         `json:"producer_name,omitempty"`
	ScopeDataHash       string         `json:"scope_data_hash,omitempty"`
	VersionID           string         `json:"version_id,omitempty"`
	Outcome             string         `json:"outcome"`
	Cause               string         `json:"cause,omitempty"`
	ProducerMetadata    map[string]any `json:"producer_metadata,omitempty"`
}

// WriteLeafRunLineage emits a leaf_run lineage record. Caller is
// responsible for the surrounding transaction; the row commits
// atomically with the run's terminal write.
func WriteLeafRunLineage(
	ctx context.Context, tx persistence.Tx, lt persistence.LineageTable,
	instanceID shared.UUID, frameID shared.UUID, observedAt time.Time, rec LeafRunRecord,
) error {
	if rec.State == "" {
		return fmt.Errorf("WriteLeafRunLineage: state required")
	}
	payload, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("WriteLeafRunLineage: marshal: %w", err)
	}
	return lt.Insert(ctx, tx, persistence.LineageRow{
		ID:         shared.UUID(uuid.New()),
		RecordKind: persistence.LineageRecordKindLeafRun,
		InstanceID: instanceID,
		FrameID:    frameID,
		ObservedAt: observedAt,
		Record:     payload,
	})
}

// WriteClaimTerminalLineage emits a claim_terminal lineage record. Fires
// at every claim-handle terminal: Commit (`outcome: committed`), natural
// Abandon (`outcome: abandoned`), and force-cancelled Abandon (`outcome:
// force_cancelled`). The terminal-decision engine (`runtime/terminal_decision.go`)
// is the single emit site so every Commit/Abandon path lands in the
// lineage projection regardless of which branch fires it.
//
// The rec.Outcome field is mirrored onto the persistence-layer
// LineageRow.Outcome column so analytical queries can filter without
// JSON extraction. Empty Outcome on rec defaults to `committed` for
// backward compatibility with code paths that only resolve Commits;
// callers from the post-2026-05-16 forensics surface always populate
// Outcome explicitly.
func WriteClaimTerminalLineage(
	ctx context.Context, tx persistence.Tx, lt persistence.LineageTable,
	instanceID shared.UUID, frameID shared.UUID, observedAt time.Time, rec ClaimTerminalRecord,
) error {
	if rec.Outcome == "" {
		rec.Outcome = persistence.LineageOutcomeCommitted
	}
	payload, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("WriteClaimTerminalLineage: marshal: %w", err)
	}
	return lt.Insert(ctx, tx, persistence.LineageRow{
		ID:         shared.UUID(uuid.New()),
		RecordKind: persistence.LineageRecordKindClaimTerminal,
		InstanceID: instanceID,
		FrameID:    frameID,
		ObservedAt: observedAt,
		Record:     payload,
		Outcome:    rec.Outcome,
	})
}

// HashCanonicalJSON returns the sha256-hex hash of a canonical JSON
// representation of v. Used by the lineage writer to produce stable
// hashes for params / userdata / scope_data fields. Spec §Content
// lineage / Hash convention — the existing
// `graph/template/canonical/CanonicalSpecHash` is the canonical-JCS
// helper for template specs; for plain JSON payloads the cheaper
// `json.Marshal` approach is sufficient since the payload shape is
// already controlled by rimsky (we generate the canonical form).
func HashCanonicalJSON(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return "sha256-" + hex.EncodeToString(sum[:]), nil
}

// HashBytes returns the sha256-hex hash of arbitrary bytes. Used by the
// lineage writer for scope_data hashing where the bytes already are the
// canonical-encoded form (rimsky doesn't re-canonicalize per
// @blessed-invariant 20).
func HashBytes(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	sum := sha256.Sum256(b)
	return "sha256-" + hex.EncodeToString(sum[:])
}

// EmitLeafRunLineage is the runtime-side convenience that the terminal
// handlers call after a leaf-run reaches a settled state. Loads its own
// short tx, hashes the per-run inputs, and inserts the lineage row.
// Best-effort: failures are logged on `args.Logger` but do not roll back
// the surrounding terminal verdict. The leaf-run lineage record is
// observability metadata, not control-plane state.
//
// `args.Persist.Lineage()` is the accessor; the write goes through
// `WriteLeafRunLineage` inside the new tx so the lineage commit
// participates with the events.Append + state-write transactions of the
// terminal handler in spirit (each runs its own short tx; the
// downstream lineage retention sweep cleans up the row when the run is
// GC'd per spec §E10).
func EmitLeafRunLineage(
	ctx context.Context, args RunArgs,
	instanceID, frameID, runID, nodeID shared.UUID, childKey string,
	state, lastOutcome, errorClass string,
	params, userdata map[string]any,
) {
	if args.Persist == nil {
		return
	}
	lt := args.Persist.Lineage()
	if lt == nil {
		return
	}
	paramsHash, _ := HashCanonicalJSON(params)
	userdataHash, _ := HashCanonicalJSON(userdata)
	rec := LeafRunRecord{
		RunID:        runID,
		NodeID:       nodeID,
		FrameID:      frameID,
		ChildKey:     childKey,
		ParamsHash:   paramsHash,
		UserdataHash: userdataHash,
		State:        state,
		LastOutcome:  lastOutcome,
		ErrorClass:   errorClass,
	}
	if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return WriteLeafRunLineage(ctx, tx, lt, instanceID, frameID, args.Clock.Now(), rec)
	}); err != nil {
		if args.Logger != nil {
			args.Logger.Warn("EmitLeafRunLineage: write failed",
				"run_id", runID.String(),
				"error", err.Error())
		}
	}
}
