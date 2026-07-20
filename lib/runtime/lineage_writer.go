// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: lineage

package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	graphnode "github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

type LeafRunHeldClaim struct {
	ClaimHandleID      string `json:"claim_handle_id"`
	Role               string `json:"role"`
	ProducerName       string `json:"producer_name"`
	ClaimScopeDataHash string `json:"claim_scope_data_hash"`
}

type SubstitutionRef struct {
	SourceKind        string `json:"source_kind"`
	SourceNodeAlias   string `json:"source_node_alias,omitempty"`
	SourceVersionOrID string `json:"source_version_or_id,omitempty"`
}

type LeafRunRecord struct {
	NodeRunID          shared.UUID        `json:"run_id"`
	NodeID             shared.UUID        `json:"node_id"`
	FrameID            shared.UUID        `json:"frame_id"`
	ChildKey           string             `json:"child_key,omitempty"`
	NodeAlias          string             `json:"node_alias,omitempty"`
	ParentNodeRunID    string             `json:"parent_run_id,omitempty"`
	FrameTriggerKind   string             `json:"frame_trigger_kind,omitempty"`
	TriggerMessageID   string             `json:"trigger_message_id,omitempty"`
	HeldClaims         []LeafRunHeldClaim `json:"held_claims,omitempty"`
	ExecutorName       string             `json:"executor_name,omitempty"`
	ExecutorVersion    string             `json:"executor_version,omitempty"`
	TemplateHash       string             `json:"template_hash,omitempty"`
	TemplateNodeAlias  string             `json:"template_node_alias,omitempty"`
	ParamsSnapshotHash string             `json:"params_snapshot_hash,omitempty"`
	AttributesHash     string             `json:"attributes_hash,omitempty"`
	ClaimScopeDataHash string             `json:"claim_scope_data_hash,omitempty"`
	State              string             `json:"state"`
	SettlingSignalType string             `json:"settling_signal_type"`
	Changed            bool               `json:"changed,omitempty"`
	TerminalKind       string             `json:"terminal_kind,omitempty"`
	ErrorClass         string             `json:"error_class,omitempty"`
	SubstitutionRefs   []SubstitutionRef  `json:"substitution_refs,omitempty"`
	Extra              map[string]any     `json:"extra,omitempty"`
}

type ClaimTerminalRecord struct {
	ClaimHandleID       shared.UUID    `json:"claim_handle_id"`
	NodeRunID           shared.UUID    `json:"run_id"`
	NodeID              shared.UUID    `json:"node_id"`
	FrameID             shared.UUID    `json:"frame_id"`
	ParentClaimHandleID *shared.UUID   `json:"parent_claim_handle_id,omitempty"`
	OpenLineageRunRef   string         `json:"open_lineage_run_ref,omitempty"`
	SubClaimHandleIDs   []string       `json:"sub_claim_handle_ids,omitempty"`
	CommittedAt         string         `json:"committed_at,omitempty"`
	ProducerName        string         `json:"producer_name,omitempty"`
	ClaimScopeDataHash  string         `json:"claim_scope_data_hash,omitempty"`
	VersionID           string         `json:"version_id,omitempty"`
	Outcome             string         `json:"outcome"`
	Cause               string         `json:"cause,omitempty"`
	ProducerMetadata    map[string]any `json:"producer_metadata,omitempty"`
	// @concept: lineage
	TerminatingSupervisorID string `json:"terminating_supervisor_id,omitempty"`
}

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

func WriteClaimTerminalLineage(
	ctx context.Context, tx persistence.Tx, lt persistence.LineageTable,
	instanceID shared.UUID, frameID shared.UUID, observedAt time.Time, rec ClaimTerminalRecord,
) error {
	if rec.Outcome == "" {
		return fmt.Errorf("WriteClaimTerminalLineage: outcome required (committed | abandoned | force_cancelled)")
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

func HashCanonicalJSON(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return "sha256-" + hex.EncodeToString(sum[:]), nil
}

func HashBytes(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	sum := sha256.Sum256(b)
	return "sha256-" + hex.EncodeToString(sum[:])
}

type LeafRunEmitInput struct {
	InstanceID         shared.UUID
	FrameID            shared.UUID
	NodeRunID          shared.UUID
	NodeID             shared.UUID
	ChildKey           string
	State              string
	SettlingSignalType string
	ErrorClass         string
	Changed            bool
	TerminalKind       string
	NodeAlias          string
	ExecutorName       string
	Params             map[string]any
	AttributesMerged   map[string]any
	HeldClaims         []LeafRunHeldClaim
	ParentNodeRunID    *shared.UUID
	TemplateHash       string
	SubstitutionRefs   []SubstitutionRef
}

func frameTriggerFieldsFor(ctx context.Context, args RunArgs, frameID shared.UUID) (kind, messageID string) {
	if args.Persist == nil {
		return "", ""
	}
	frames := args.Persist.Frames()
	if frames == nil {
		return "", ""
	}
	var row *persistence.FrameRowWithMessage
	if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, ferr := frames.GetForObservabilityWithMessage(ctx, frameID, tx)
		row = r
		return ferr
	}); err != nil {
		if args.Logger != nil {
			args.Logger.Warn("lineage_writer.frameTriggerFieldsFor failed",
				"frame_id", frameID.String(),
				"error", err.Error())
		}
		return "", ""
	}
	if row == nil {
		return "", ""
	}
	return row.MessageSenderKind, row.TriggeringMessageID.String()
}

func EmitLeafRunLineage(ctx context.Context, args RunArgs, in LeafRunEmitInput) {
	if args.Persist == nil {
		return
	}
	lt := args.Persist.Lineage()
	if lt == nil {
		return
	}
	paramsHash, perr := HashCanonicalJSON(in.Params)
	if perr != nil && args.Logger != nil {
		args.Logger.Warn("lineage_writer.HashCanonicalJSON failed",
			"field", "params",
			"run_id", in.NodeRunID.String(),
			"error", perr.Error())
	}
	attributesHash, uerr := HashCanonicalJSON(in.AttributesMerged)
	if uerr != nil && args.Logger != nil {
		args.Logger.Warn("lineage_writer.HashCanonicalJSON failed",
			"field", "attributes",
			"run_id", in.NodeRunID.String(),
			"error", uerr.Error())
	}
	parentNodeRunID := ""
	if in.ParentNodeRunID != nil {
		parentNodeRunID = in.ParentNodeRunID.String()
	}
	frameTriggerKind, triggerMessageID := frameTriggerFieldsFor(ctx, args, in.FrameID)
	rec := LeafRunRecord{
		NodeRunID:          in.NodeRunID,
		NodeID:             in.NodeID,
		FrameID:            in.FrameID,
		ChildKey:           in.ChildKey,
		NodeAlias:          in.NodeAlias,
		TemplateNodeAlias:  in.NodeAlias,
		ParentNodeRunID:    parentNodeRunID,
		FrameTriggerKind:   frameTriggerKind,
		TriggerMessageID:   triggerMessageID,
		ExecutorName:       in.ExecutorName,
		TemplateHash:       in.TemplateHash,
		ParamsSnapshotHash: paramsHash,
		AttributesHash:     attributesHash,
		State:              in.State,
		SettlingSignalType: in.SettlingSignalType,
		ErrorClass:         in.ErrorClass,
		Changed:            in.Changed,
		TerminalKind:       in.TerminalKind,
		HeldClaims:         in.HeldClaims,
		SubstitutionRefs:   in.SubstitutionRefs,
	}
	if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return WriteLeafRunLineage(ctx, tx, lt, in.InstanceID, in.FrameID, args.Clock.Now(), rec)
	}); err != nil {
		if args.Logger != nil {
			args.Logger.Warn("EmitLeafRunLineage: write failed",
				"run_id", in.NodeRunID.String(),
				"error", err.Error())
		}
	}
}

func HeldClaimsForLineage(acq *acquisition) []LeafRunHeldClaim {
	if acq == nil {
		return nil
	}
	out := make([]LeafRunHeldClaim, 0, len(acq.Locks)+len(acq.HeldClaims))
	for _, lk := range acq.Locks {
		sp, ok := lk.Spec.(claimproducer.ClaimSpec)
		if !ok {
			continue
		}
		out = append(out, LeafRunHeldClaim{
			ClaimHandleID:      lk.ClaimHandleID.String(),
			Role:               "claim",
			ProducerName:       sp.ProducerName,
			ClaimScopeDataHash: HashBytes(lk.ClaimResult.ClaimScope),
		})
	}
	for alias, cr := range acq.HeldClaims {
		out = append(out, LeafRunHeldClaim{
			ClaimHandleID:      "",
			Role:               "held:" + alias,
			ProducerName:       "",
			ClaimScopeDataHash: HashBytes(cr.ClaimScope),
		})
	}
	return out
}

// @concept: lineage-record
func CollectSubstitutionRefsForEmit(ctx context.Context, args RunArgs, acq *acquisition) []SubstitutionRef {
	if acq == nil || acq.NodeDef == nil {
		return nil
	}
	refs := graphnode.SubstitutionRefsFromAttributes(*acq.NodeDef)
	if len(refs) == 0 {
		return nil
	}
	out := make([]SubstitutionRef, 0, len(refs)*2)
	for _, r := range refs {
		out = append(out, SubstitutionRef{
			SourceKind:        r.TopicKind,
			SourceNodeAlias:   r.SenderNodeType,
			SourceVersionOrID: r.Name,
		})
	}
	if args.Persist == nil {
		return out
	}
	var upstreamNodeIDs map[string]shared.UUID
	if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		rows, err := args.Persist.Nodes().ListByInstance(ctx, acq.InstanceID, tx)
		if err != nil {
			return err
		}
		upstreamNodeIDs = make(map[string]shared.UUID, len(rows))
		for _, row := range rows {
			upstreamNodeIDs[row.NodeType] = row.ID
		}
		return nil
	}); err != nil {
		if args.Logger != nil {
			args.Logger.Warn("CollectSubstitutionRefsForEmit: ListByInstance failed; emitting directive-shape refs only",
				"instance_id", acq.InstanceID.String(),
				"error", err.Error())
		}
		return out
	}
	lt := args.Persist.Lineage()
	if lt == nil {
		return out
	}
	seenSender := map[string]bool{}
	for _, r := range refs {
		if seenSender[r.SenderNodeType] {
			continue
		}
		seenSender[r.SenderNodeType] = true
		upstreamNodeID, ok := upstreamNodeIDs[r.SenderNodeType]
		if !ok {
			continue
		}
		runID := mostRecentRunIDForNode(ctx, lt, acq.InstanceID, upstreamNodeID)
		if runID == (shared.UUID{}) {
			continue
		}
		out = append(out, SubstitutionRef{
			SourceKind:        "run",
			SourceNodeAlias:   r.SenderNodeType,
			SourceVersionOrID: runID.String(),
		})
	}
	return out
}

const mostRecentRunLookupLimit = 1000

// @concept: lineage-record
func mostRecentRunIDForNode(
	ctx context.Context, lt persistence.LineageTable, instanceID, upstreamNodeID shared.UUID,
) shared.UUID {
	page, err := lt.Query(ctx, persistence.LineageQuery{
		Kind:       persistence.LineageRecordKindLeafRun,
		InstanceID: &instanceID,
	}, persistence.ListPagination{Limit: mostRecentRunLookupLimit})
	if err != nil {
		return shared.UUID{}
	}
	for i := 0; i < len(page.Rows); i++ {
		r := page.Rows[i]
		var rec struct {
			NodeID    string `json:"node_id"`
			NodeRunID string `json:"run_id"`
		}
		if err := json.Unmarshal(r.Record, &rec); err != nil {
			continue
		}
		if rec.NodeID == upstreamNodeID.String() && rec.NodeRunID != "" {
			if u, err := uuid.Parse(rec.NodeRunID); err == nil {
				return shared.UUID(u)
			}
		}
	}
	return shared.UUID{}
}
