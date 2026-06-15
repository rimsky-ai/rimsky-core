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
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	graphnode "github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

// LeafRunHeldClaim is the per-held-claim entry of LeafRunRecord.HeldClaims.
// Mirrors subscribers/openlineage/subscriber.go::HeldClaimRef.
type LeafRunHeldClaim struct {
	ClaimHandleID      string `json:"claim_handle_id"`
	Role               string `json:"role"`
	ProducerName       string `json:"producer_name"`
	ClaimScopeDataHash string `json:"claim_scope_data_hash"`
}

// SubstitutionRef is one entry of LeafRunRecord.SubstitutionRefs — the
// upstream source the receiving run consumed at dispatch-time via
// `{{nodes.<X>.attribute.<Y>}}` / `{{nodes.<X>.event.<Y>}}` directives.
// The richer object shape (over a bare `[]string`) lets the
// `/lineage/by-source/{kind}/{id}` reverse-lookup discriminate by source
// kind, and lets the ancestor walker resolve the upstream's lineage row
// by RUN id (not just node-type) when one is available.
//
// Field semantics:
//
//   - `SourceKind` ∈ {`attribute`, `event`, `run`, `claim`}. `attribute`
//     and `event` are emitted by the runtime substitution layer;
//     `run` is reserved for run-tree links pre-populated by the dispatch
//     path (when the upstream's leaf-run row is known at acquire-time);
//     `claim` for claim-source linkage (post-v1).
//   - `SourceNodeAlias` is the upstream node-type (template alias) the
//     directive named.
//   - `SourceVersionOrID` is the specific upstream run id (when known) or
//     the upstream attribute/event name (when run id not yet wired).
//
// Aligned with subscribers/openlineage/subscriber.go::SubstitutionRef.
type SubstitutionRef struct {
	SourceKind        string `json:"source_kind"`
	SourceNodeAlias   string `json:"source_node_alias,omitempty"`
	SourceVersionOrID string `json:"source_version_or_id,omitempty"`
}

// LeafRunRecord is the JSON payload of a "leaf_run" lineage row. Field
// names match spec §Content lineage / Leaf-run record shape and align
// byte-for-byte with the consumer-side
// `subscribers/openlineage/subscriber.go::LeafRunRecord`.
//
// Fields the writer cannot source from the current acquisition context
// — `ExecutorVersion` (no executor protocol surface for it yet),
// `FrameTriggerKind`, and `TriggerMessageID` (neither plumbed through
// the dispatch path) — are left empty by callers. The unplumbed set is
// surfaced once per process via the startup INFO log emitted by
// `logMissingFieldsOnce` (see `missingLeafRunFields` below for the
// authoritative list); the subscriber treats empty strings as "not
// available" rather than failing the decode.
type LeafRunRecord struct {
	RunID   shared.UUID `json:"run_id"`
	NodeID  shared.UUID `json:"node_id"`
	FrameID shared.UUID `json:"frame_id"`
	// ChildKey is the lineage record's projection of the partition key
	// (col:rimsky_run_scopes.partition_key). The JSON key stays
	// `child_key` for external-consumer compatibility (lineage rows are
	// externally queryable; renaming the JSON key would break decoders).
	// Under the RunScope-first reshape the value is sourced from the
	// run's RunScope.PartitionKey via resolveAcqScope, not from the
	// retired rimsky_node_runs.child_key column. New consumers should
	// read this field as "partition key (json: child_key)".
	ChildKey           string             `json:"child_key,omitempty"`
	NodeAlias          string             `json:"node_alias,omitempty"`
	ParentRunID        string             `json:"parent_run_id,omitempty"`
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
	// SettlingSignalType is the canonical signal type-path the run
	// settled with (concept:signal). Replaces the retired LastOutcome
	// projection per Pass 5 of spec
	// 2026-05-23-signal-taxonomy-and-policy-decoupling-design.
	SettlingSignalType string            `json:"settling_signal_type"`
	Changed            bool              `json:"changed,omitempty"`
	TerminalKind       string            `json:"terminal_kind,omitempty"`
	ErrorClass         string            `json:"error_class,omitempty"`
	SubstitutionRefs   []SubstitutionRef `json:"substitution_refs,omitempty"`
	Extra              map[string]any    `json:"extra,omitempty"`
}

// ClaimTerminalRecord is the JSON payload of a "claim_terminal" lineage
// row. Fires at every claim-handle terminal — Commit (`outcome:
// committed`), natural Abandon (`outcome: abandoned`), and force-
// cancelled Abandon (`outcome: force_cancelled`). The `Cause` field
// further distinguishes force-cancellation provenance (sibling-cancel
// vs descendant-cancel) on `force_cancelled` rows so post-mortem
// queries can reconstruct the actual cancellation walk.
//
// Aligned with `subscribers/openlineage/subscriber.go::ClaimTerminalRecord`
// — every field the subscriber reads (`open_lineage_run_ref`,
// `sub_claim_handle_ids`, `committed_at`) is emitted here so the
// OpenLineage event downstream has the correct Run.RunID, dataset
// fan-out manifest, and event time. Without `open_lineage_run_ref`
// the emitter falls back to `claim_handle_id`, which produces broken
// lineage graphs at the OpenLineage backend.
//
// Note: `OpenLineageRunRef` is intentionally NOT named ParentRunID —
// it is not the parent in the run-tree sense. It is the run identity
// the OpenLineage emitter keys on (currently a stringification of the
// holding-run's RunID; see `terminal_decision_forensics.go`). The
// distinct name avoids the easy confusion with `runtime.acquisition.
// ParentRunID` (which IS a parent in the run-tree) and with
// `foundation/persistence.NodeRunRow.ParentRunID` (likewise).
type ClaimTerminalRecord struct {
	ClaimHandleID       shared.UUID    `json:"claim_handle_id"`
	RunID               shared.UUID    `json:"run_id"`
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
// JSON extraction. Outcome is REQUIRED — an empty Outcome returns an
// error so an Abandon path that forgets to set it cannot silently
// produce a row marked `committed`. Pre-v1 break-freely (per
// `.claude/rules/rules.md`): no back-compat default for callers that
// don't fill it in.
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

// HashCanonicalJSON returns the sha256-hex hash of a canonical JSON
// representation of v. Used by the lineage writer to produce stable
// hashes for params / attributes / claim_scope_data fields. Spec §Content
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
// lineage writer for claim_scope_data hashing where the bytes already are the
// canonical-encoded form (rimsky doesn't re-canonicalize per
// @blessed-invariant 20).
func HashBytes(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	sum := sha256.Sum256(b)
	return "sha256-" + hex.EncodeToString(sum[:])
}

// LeafRunEmitInput collects the per-run fields the terminal handlers
// populate before emitting a leaf_run lineage row. Grouping in a struct
// (rather than a long positional arg list) keeps the call sites stable
// as new fields are sourced from the dispatch context.
//
// Fields the caller can source from the acquisition / terminal event
// directly:
//   - NodeAlias, TemplateNodeAlias — `acquisition.NodeType`
//   - ExecutorName                 — `acquisition.Executor`
//   - InstanceParams, AttributesMerged — the merged attribute bag the
//     executor actually saw on this dispatch; pass through
//     `acquisition.MergedAttributes` so the hash reflects what
//     shipped, not the pre-merge per-instance override blob.
//   - HeldClaims                   — `acquisition.HeldClaims`, mapped
//     into the per-row shape.
//
// Fields not currently sourced from the acquisition (`ExecutorVersion`,
// `FrameTriggerKind`, `TriggerMessageID`, `TemplateHash`, `ParentRunID`)
// are left empty by callers; the subscriber treats empty strings as
// "not available" and the OpenLineage facet stays empty rather than
// dropping the event. Per the project rule, every callable field is
// either populated or explicitly set to a documented placeholder; new
// sources land here as they become available.
type LeafRunEmitInput struct {
	InstanceID shared.UUID
	FrameID    shared.UUID
	RunID      shared.UUID
	NodeID     shared.UUID
	// ChildKey carries the partition key from the run's RunScope
	// (col:rimsky_run_scopes.partition_key). Named ChildKey for
	// lineage-record wire compatibility (the JSON tag stays
	// `child_key`); callers source it from `resolveAcqScope(...).
	// PartitionKey` under the RunScope-first reshape.
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
	// ParentRunID is sourced from `rimsky_node_runs.parent_run_id` at
	// acquisition time (threaded through `acquisition.ParentRunID`).
	// Nil for root runs (top-level template dispatches); the lineage
	// writer drops the `parent_run_id` JSON key for those rows via
	// `omitempty`. Drives `LineageTable.QueryByParentRunID` (the
	// descendant walker that powers
	// `route:GET /lineage/runs/{run_id}/descendants`).
	ParentRunID *shared.UUID
	// TemplateHash is the content-addressed template id
	// (`rimsky_instances.template_hash`) the run was dispatched
	// against. Loaded during acquisition (`acquisition.TemplateHash`)
	// and threaded into `LeafRunRecord.TemplateHash` so the lineage
	// row records WHICH template version produced the run. Empty for
	// emit sites that don't have an acquisition (none today; future
	// pre-acquisition emit sites should plumb it explicitly).
	TemplateHash string
	// SubstitutionRefs lists upstream sources this run consumed at
	// dispatch-time via `{{nodes.X.attribute.Y}}` /
	// `{{nodes.X.event.Y}}` directives, resolved to upstream run ids
	// where the dispatcher can find them. Sourced from
	// `runtime.collectSubstitutionRefsForEmit` (which inspects
	// `acq.NodeDef.Attributes` and looks up the upstream node's most
	// recent leaf-run lineage row). The ancestor walker
	// (`control/controlapi/lineage.go::walkLineageRuns`) reads the
	// `SourceVersionOrID` field as a UUID and follows the link.
	SubstitutionRefs []SubstitutionRef
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
			"run_id", in.RunID.String(),
			"error", perr.Error())
	}
	attributesHash, uerr := HashCanonicalJSON(in.AttributesMerged)
	if uerr != nil && args.Logger != nil {
		args.Logger.Warn("lineage_writer.HashCanonicalJSON failed",
			"field", "attributes",
			"run_id", in.RunID.String(),
			"error", uerr.Error())
	}
	parentRunID := ""
	if in.ParentRunID != nil {
		parentRunID = in.ParentRunID.String()
	}
	rec := LeafRunRecord{
		RunID:              in.RunID,
		NodeID:             in.NodeID,
		FrameID:            in.FrameID,
		ChildKey:           in.ChildKey,
		NodeAlias:          in.NodeAlias,
		TemplateNodeAlias:  in.NodeAlias,
		ParentRunID:        parentRunID,
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
	logMissingFieldsOnce(args.Logger, rec)
	if err := args.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return WriteLeafRunLineage(ctx, tx, lt, in.InstanceID, in.FrameID, args.Clock.Now(), rec)
	}); err != nil {
		if args.Logger != nil {
			args.Logger.Warn("EmitLeafRunLineage: write failed",
				"run_id", in.RunID.String(),
				"error", err.Error())
		}
	}
}

// HeldClaimsForLineage walks the acquisition's per-claim locks and
// returns a best-effort LeafRunHeldClaim slice for the lineage row.
// Each entry's `Role` is "claim" (the alias was acquired by this run
// directly) or "held:<alias>" (the alias was co-held via `holds:`).
// Per @blessed-invariant 20 the bytes are inert;
// only the hash + producer name + alias travel in the lineage row.
func HeldClaimsForLineage(acq *acquisition) []LeafRunHeldClaim {
	if acq == nil {
		return nil
	}
	out := make([]LeafRunHeldClaim, 0, len(acq.Locks)+len(acq.HeldClaims))
	for _, lk := range acq.Locks {
		sp, ok := lk.Spec.(claimproducer.ClaimSpec)
		if !ok {
			// @constraint: NamedLockSpec rows carry no producer, so they
			// cannot contribute to the producer-keyed lineage projection.
			continue
		}
		out = append(out, LeafRunHeldClaim{
			ClaimHandleID:      lk.ClaimHandleID.String(),
			Role:               "claim",
			ProducerName:       sp.ProducerName,
			ClaimScopeDataHash: HashBytes(lk.ClaimResult.ClaimScope),
		})
	}
	// @deliberate: HeldClaims map carries co-held / inherited aliases
	// whose claim_handle_id and producer_name live on the row, not the
	// in-memory result, so the per-alias entry is emitted with empty
	// ClaimHandleID + ProducerName; downstream tools join against
	// rimsky_claim_handles by alias if they need the row id.
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

// missingLeafRunFields lists per-row LeafRunRecord fields the writer
// could not source from the current dispatch context. Fields here are
// "not yet plumbed" — distinct from "legitimately empty for this row"
// (e.g. ParentRunID on root runs). Drop a field from this list once
// it's wired through `acquisition` so the startup INFO line tightens
// automatically.
//
// As of 2026-05-17 the remaining gaps are:
//
//   - ExecutorVersion — requires plumbing the executor's
//     `Capabilities.Version` (currently not advertised by the executor
//     protocol; v1-defer).
//   - FrameTriggerKind — requires reading the frame-arrival path's
//     trigger discriminator off the frame row.
//   - TriggerMessageID — requires the per-frame `rimsky_messages` join
//     to surface the message id that fired the frame.
//
// TemplateHash retired from this list on 2026-05-17 — it's now sourced
// via `acquisition.TemplateHash`.
func missingLeafRunFields(rec LeafRunRecord) []string {
	var out []string
	if rec.ExecutorVersion == "" {
		out = append(out, "executor_version")
	}
	if rec.FrameTriggerKind == "" {
		out = append(out, "frame_trigger_kind")
	}
	if rec.TriggerMessageID == "" {
		out = append(out, "trigger_message_id")
	}
	return out
}

var logMissingFieldsOnceState sync.Once

// logMissingFieldsOnce emits a single startup-time INFO listing the
// LeafRunRecord fields the build doesn't yet plumb. Subsequent emits
// in the same process are silent — the gap is observable at boot
// without per-row log noise.
//
// The `sync.Once` is package-scoped because the gap is a build-time
// property (which fields are plumbed at all), not a per-call
// condition. Tests that exercise EmitLeafRunLineage don't need to
// reset it: every test inherits the same plumbing surface; the log
// fires at most once per process regardless of test count.
func logMissingFieldsOnce(logger shared.Logger, rec LeafRunRecord) {
	if logger == nil {
		return
	}
	logMissingFieldsOnceState.Do(func() {
		if missing := missingLeafRunFields(rec); len(missing) > 0 {
			logger.Info("EmitLeafRunLineage: fields unavailable at this build level",
				"missing", missing)
		}
	})
}

// CollectSubstitutionRefsForEmit builds the per-run SubstitutionRef
// slice from the acquisition context. For each `{{nodes.X.attribute.Y}}`
// / `{{nodes.X.event.Y}}` directive in the receiver's attribute schema,
// the helper:
//
//  1. Records the directive shape as `(SourceKind, SourceNodeAlias,
//     SourceVersionOrID=<attribute-or-event-name>)`.
//  2. Looks up the upstream node's most recent leaf-run lineage row in
//     the same instance and adds a second `SourceKind="run"` entry
//     keyed by the upstream run id. The ancestor walker
//     (`control/controlapi/lineage.go::walkLineageRuns`) reads the
//     `SourceVersionOrID` field as a UUID and follows the link to the
//     upstream lineage row.
//
// The two-entry-per-directive shape gives operators both pieces of
// information: which directive caused the read (attribute/event +
// name) and which specific upstream run produced the value. When the
// upstream's lineage row isn't yet available (cold start, retention
// sweep removed it, etc.), only the directive-shape entry is emitted —
// the ancestor walker silently skips entries whose
// `SourceVersionOrID` isn't a UUID.
//
// Best-effort: failures (no template, no upstream node) return nil
// rather than failing the emit, since the lineage row itself is
// observability metadata.
//
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
		runID := mostRecentRunIDForNode(ctx, lt, upstreamNodeID)
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

// mostRecentRunIDForNode walks the lineage projection looking for the
// most recent leaf-run row whose `node_id` matches the upstream node id.
// Returns the zero UUID when no row is found. Uses `Query` with the
// instance filter omitted (the lineage table doesn't carry a per-node
// secondary index pre-v1; the per-instance projection is small enough
// for an in-memory scan over the page-size cap). Pre-v1: the helper
// returns the first match found scanning the most-recent leaf-run page;
// a sharper post-v1 implementation would push a `node_id` predicate
// into the persistence layer.
func mostRecentRunIDForNode(ctx context.Context, lt persistence.LineageTable, upstreamNodeID shared.UUID) shared.UUID {
	page, err := lt.Query(ctx, persistence.LineageQuery{
		Kind: persistence.LineageRecordKindLeafRun,
	}, persistence.ListPagination{Limit: 200})
	if err != nil {
		return shared.UUID{}
	}
	// @constraint: ancestor walker needs observed_at DESC for
	// most-recent semantics; the persistence layer's Query sorts
	// ascending, so this loop scans back-to-front to invert the order.
	for i := len(page.Rows) - 1; i >= 0; i-- {
		r := page.Rows[i]
		var rec struct {
			NodeID string `json:"node_id"`
			RunID  string `json:"run_id"`
		}
		if err := json.Unmarshal(r.Record, &rec); err != nil {
			continue
		}
		if rec.NodeID == upstreamNodeID.String() && rec.RunID != "" {
			if u, err := uuid.Parse(rec.RunID); err == nil {
				return shared.UUID(u)
			}
		}
	}
	return shared.UUID{}
}
