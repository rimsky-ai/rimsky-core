// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

var claimHandleColNames = []string{
	"id", "lock_kind", "lock_name", "producer_name", "claim_scope_data", "address", "payload", "intent",
	"realized_write_semantics",
	"holder_supervisor_id", "holder_node_id",
	"claimed_at", "expires_at", "frame_id",
	"node_run_id", "is_held",
	"parent_claim_handle_id", "lifetime", "version_id",
	"producer_candidate_handle", "producer_lease_token",
	"aggregation_policy", "expected_children_count",
	"committed_children_count", "abandoned_children_count",
	"state", "resolved_at",
}

var claimHandleCols = strings.Join(claimHandleColNames, ", ")

func claimantGuard(n int) string {
	return fmt.Sprintf("holder_supervisor_id = $%d", n)
}

func (s *claimHandlesImpl) Insert(ctx context.Context, in persistence.ClaimHandleInsertInput, tx persistence.Tx) error {
	now := time.Now().UTC()
	if !in.ClaimedAtOverride.IsZero() {
		now = in.ClaimedAtOverride
	}
	var rws *string
	if in.RealizedWriteSemantics != "" {
		v := in.RealizedWriteSemantics
		rws = &v
	}
	lifetime := in.Lifetime
	if lifetime == "" {
		lifetime = spec.ClaimLifetimeSubgraph
	}
	var candidateHandle any
	if len(in.ProducerCandidateHandle) > 0 {
		candidateHandle = in.ProducerCandidateHandle
	}
	_, err := s.q(tx).Exec(ctx,
		`INSERT INTO rimsky_claim_handles (
		   id, lock_kind, lock_name, producer_name, claim_scope_data, address, payload, intent,
		   realized_write_semantics,
		   holder_supervisor_id, holder_node_id,
		   claimed_at, expires_at, frame_id,
		   node_run_id, is_held,
		   parent_claim_handle_id, lifetime, producer_candidate_handle, producer_lease_token,
		   aggregation_policy
		 ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21)`,
		in.ID, string(in.LockKind),
		in.LockName, in.ProducerName,
		nullableJSONB(in.ClaimScopeData), nullableJSONB(in.Address), nullableJSONB(in.Payload),
		in.Intent,
		rws,
		in.HolderSupervisorID, in.HolderNodeID,
		now, in.ExpiresAt, in.FrameID,
		in.NodeRunID, in.IsHeld,
		in.ParentClaimHandleID, string(lifetime), candidateHandle, in.ProducerLeaseToken,
		nullableJSONB(in.AggregationPolicy),
	)
	if err != nil {
		return fmt.Errorf("claimhandles.Insert: %w", err)
	}
	return nil
}

func (s *claimHandlesImpl) UpdateAddress(
	ctx context.Context, id shared.UUID, supervisorID string, address json.RawMessage, tx persistence.Tx,
) error {
	cmd, err := s.q(tx).Exec(ctx,
		`UPDATE rimsky_claim_handles
		    SET address = $1
		  WHERE id = $2 AND `+claimantGuard(3),
		nullableJSONB(address), id, supervisorID,
	)
	if err != nil {
		return fmt.Errorf("claimhandles.UpdateAddress: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		return spec.ErrIllegalClaimHandleTransition
	}
	return nil
}

func (s *claimHandlesImpl) UpdatePayload(
	ctx context.Context, id shared.UUID, supervisorID string, payload json.RawMessage, tx persistence.Tx,
) error {
	cmd, err := s.q(tx).Exec(ctx,
		`UPDATE rimsky_claim_handles
		    SET payload = $1
		  WHERE id = $2 AND `+claimantGuard(3),
		nullableJSONB(payload), id, supervisorID,
	)
	if err != nil {
		return fmt.Errorf("claimhandles.UpdatePayload: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		return spec.ErrIllegalClaimHandleTransition
	}
	return nil
}

func (s *claimHandlesImpl) UpdateRealizedWriteSemantics(
	ctx context.Context, id shared.UUID, supervisorID string, ws string, tx persistence.Tx,
) error {
	var v *string
	if ws != "" {
		s := ws
		v = &s
	}
	cmd, err := s.q(tx).Exec(ctx,
		`UPDATE rimsky_claim_handles
		    SET realized_write_semantics = $1
		  WHERE id = $2 AND `+claimantGuard(3),
		v, id, supervisorID,
	)
	if err != nil {
		return fmt.Errorf("claimhandles.UpdateRealizedWriteSemantics: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		return spec.ErrIllegalClaimHandleTransition
	}
	return nil
}

func (s *claimHandlesImpl) UpdateClaimScope(
	ctx context.Context, id shared.UUID, supervisorID string, scope json.RawMessage, tx persistence.Tx,
) error {
	cmd, err := s.q(tx).Exec(ctx,
		`UPDATE rimsky_claim_handles
		    SET claim_scope_data = $1
		  WHERE id = $2 AND `+claimantGuard(3),
		nullableJSONB(scope), id, supervisorID,
	)
	if err != nil {
		return fmt.Errorf("claimhandles.UpdateClaimScope: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		return spec.ErrIllegalClaimHandleTransition
	}
	return nil
}

func (s *claimHandlesImpl) UpdateNodeRunID(
	ctx context.Context, id shared.UUID, nodeRunID shared.UUID, supervisorID string, tx persistence.Tx,
) error {
	cmd, err := s.q(tx).Exec(ctx,
		`UPDATE rimsky_claim_handles
		    SET node_run_id = $1
		  WHERE id = $2
		    AND state = 'active'
		    AND `+claimantGuard(3),
		nodeRunID, id, supervisorID,
	)
	if err != nil {
		return fmt.Errorf("claimhandles.UpdateNodeRunID: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		return spec.ErrIllegalClaimHandleTransition
	}
	return nil
}

func (s *claimHandlesImpl) ReassignHolderSupervisor(
	ctx context.Context, id shared.UUID, fromSupervisorID, toSupervisorID string, tx persistence.Tx,
) error {
	if toSupervisorID == "" {
		return fmt.Errorf("claimhandles.ReassignHolderSupervisor: empty toSupervisorID")
	}
	cmd, err := s.q(tx).Exec(ctx,
		`UPDATE rimsky_claim_handles
		    SET holder_supervisor_id = $1
		  WHERE id = $2
		    AND state = 'active'
		    AND `+claimantGuard(3),
		toSupervisorID, id, fromSupervisorID,
	)
	if err != nil {
		return fmt.Errorf("claimhandles.ReassignHolderSupervisor: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		return spec.ErrIllegalClaimHandleTransition
	}
	return nil
}

func (s *claimHandlesImpl) Get(ctx context.Context, id shared.UUID, tx persistence.Tx) (*persistence.ClaimHandleRow, error) {
	row := s.q(tx).QueryRow(ctx,
		`SELECT `+claimHandleCols+` FROM rimsky_claim_handles WHERE id = $1`, id,
	)
	out, err := scanClaimHandle(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &out, nil
}

func (s *claimHandlesImpl) LockForUpdate(ctx context.Context, id shared.UUID, tx persistence.Tx) (*persistence.ClaimHandleRow, error) {
	row := s.q(tx).QueryRow(ctx,
		`SELECT `+claimHandleCols+` FROM rimsky_claim_handles WHERE id = $1 FOR UPDATE`, id,
	)
	out, err := scanClaimHandle(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("claimhandles.LockForUpdate: %w", err)
	}
	return &out, nil
}

func (s *claimHandlesImpl) ListByHolderNode(ctx context.Context, holderNodeID shared.UUID, tx persistence.Tx) ([]persistence.ClaimHandleRow, error) {
	rows, err := s.q(tx).Query(ctx,
		`SELECT `+claimHandleCols+` FROM rimsky_claim_handles
		 WHERE holder_node_id = $1
		 ORDER BY claimed_at ASC`, holderNodeID,
	)
	if err != nil {
		return nil, fmt.Errorf("claimhandles.ListByHolderNode: %w", err)
	}
	defer rows.Close()
	return collectClaimHandles(rows)
}

func (s *claimHandlesImpl) ListByNodeRun(ctx context.Context, nodeRunID shared.UUID, tx persistence.Tx) ([]persistence.ClaimHandleRow, error) {
	rows, err := s.q(tx).Query(ctx,
		`SELECT `+claimHandleCols+` FROM rimsky_claim_handles
		 WHERE node_run_id = $1
		 ORDER BY claimed_at ASC`, nodeRunID,
	)
	if err != nil {
		return nil, fmt.Errorf("claimhandles.ListByNodeRun: %w", err)
	}
	defer rows.Close()
	return collectClaimHandles(rows)
}

func (s *claimHandlesImpl) GetByFrameAndNode(ctx context.Context, nodeID shared.UUID, frameID shared.UUID, tx persistence.Tx) (*persistence.ClaimHandleRow, error) {
	row := s.q(tx).QueryRow(ctx,
		`SELECT `+claimHandleCols+` FROM rimsky_claim_handles
		 WHERE holder_node_id = $1 AND frame_id = $2
		 LIMIT 1`,
		nodeID, frameID,
	)
	out, err := scanClaimHandle(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("claimhandles.GetByFrameAndNode: %w", err)
	}
	return &out, nil
}

func (s *claimHandlesImpl) ListChildClaimHandles(ctx context.Context, parentID shared.UUID, tx persistence.Tx) ([]persistence.ClaimHandleRow, error) {
	rows, err := s.q(tx).Query(ctx,
		`SELECT `+claimHandleCols+` FROM rimsky_claim_handles
		 WHERE parent_claim_handle_id = $1`,
		parentID)
	if err != nil {
		return nil, fmt.Errorf("claimhandles.ListChildClaimHandles: %w", err)
	}
	defer rows.Close()
	return collectClaimHandles(rows)
}

// @concept: claim-handle
// @concept: claim-lifetime
func (s *claimHandlesImpl) DeleteResolvedOlderThan(
	ctx context.Context, cutoff time.Time,
) (int, error) {
	tag, err := (*tablesImpl)(s).pool.Exec(ctx,
		`DELETE FROM rimsky_claim_handles
		  WHERE state IN ('committed', 'abandoned')
		    AND (state = 'abandoned' OR lifetime = 'subgraph')
		    AND resolved_at < $1
		    AND holder_supervisor_id IS NULL`,
		cutoff)
	if err != nil {
		return 0, fmt.Errorf("postgres.ClaimHandles.DeleteResolvedOlderThan: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// @concept: claim-handle
func (s *claimHandlesImpl) DeleteResolved(
	ctx context.Context, id shared.UUID, tx persistence.Tx,
) error {
	cmd, err := s.q(tx).Exec(ctx,
		`DELETE FROM rimsky_claim_handles
		  WHERE id = $1
		    AND state IN ('committed', 'abandoned')
		    AND holder_supervisor_id IS NULL`, id)
	if err != nil {
		return fmt.Errorf("claimhandles.DeleteResolved: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		return spec.ErrIllegalClaimHandleTransition
	}
	return nil
}

// @concept: claim-handle
// @concept: claim-co-holdership
func (s *claimHandlesImpl) DeleteResolvedIfNoActiveHolders(
	ctx context.Context, id shared.UUID, tx persistence.Tx,
) (bool, error) {
	cmd, err := s.q(tx).Exec(ctx,
		`DELETE FROM rimsky_claim_handles
		  WHERE id = $1
		    AND state IN ('committed', 'abandoned')
		    AND holder_supervisor_id IS NULL
		    AND NOT EXISTS (
		      SELECT 1 FROM rimsky_claim_holders
		       WHERE rimsky_claim_holders.claim_handle_id = rimsky_claim_handles.id
		         AND rimsky_claim_holders.state = 'active'
		    )`, id)
	if err != nil {
		return false, fmt.Errorf("claimhandles.DeleteResolvedIfNoActiveHolders: %w", err)
	}
	return cmd.RowsAffected() > 0, nil
}

// @concept: claim-handle
func (s *claimHandlesImpl) Promote(
	ctx context.Context, id shared.UUID, supervisorID string,
	newState spec.ClaimHandleState, tx persistence.Tx,
) error {
	cmd, err := s.q(tx).Exec(ctx,
		`UPDATE rimsky_claim_handles
		    SET state = $3,
		        holder_supervisor_id = NULL,
		        resolved_at = now()
		  WHERE id = $1
		    AND state = 'active'
		    AND `+claimantGuard(2),
		id, supervisorID, string(newState))
	if err != nil {
		return fmt.Errorf("claimhandles.Promote: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		return spec.ErrIllegalClaimHandleTransition
	}
	return nil
}

func (s *claimHandlesImpl) ListByInstanceAndState(
	ctx context.Context, instanceID shared.UUID,
	state spec.ClaimHandleState, lifetime spec.ClaimLifetime, tx persistence.Tx,
) ([]persistence.ClaimHandleRow, error) {
	rows, err := s.q(tx).Query(ctx,
		`SELECT `+qualifiedClaimHandleCols("ch")+`
		   FROM rimsky_claim_handles ch
		   JOIN rimsky_nodes n ON n.id = ch.holder_node_id
		  WHERE n.instance_id = $1
		    AND ch.state = $2
		    AND ch.lifetime = $3`,
		instanceID, string(state), string(lifetime))
	if err != nil {
		return nil, fmt.Errorf("claimhandles.ListByInstanceAndState: %w", err)
	}
	defer rows.Close()
	return collectClaimHandles(rows)
}

func (s *claimHandlesImpl) SetVersionID(
	ctx context.Context, id shared.UUID, supervisorID string, versionID string, tx persistence.Tx,
) error {
	var v *string
	if versionID != "" {
		v = &versionID
	}
	cmd, err := s.q(tx).Exec(ctx,
		`UPDATE rimsky_claim_handles SET version_id = $1
		 WHERE id = $2 AND (`+claimantGuard(3)+`
		    OR (holder_supervisor_id IS NULL AND state <> 'active'))`,
		v, id, supervisorID)
	if err != nil {
		return fmt.Errorf("claimhandles.SetVersionID: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		return spec.ErrIllegalClaimHandleTransition
	}
	return nil
}

func qualifiedClaimHandleCols(alias string) string {
	qualified := make([]string, len(claimHandleColNames))
	for i, c := range claimHandleColNames {
		qualified[i] = alias + "." + c
	}
	return strings.Join(qualified, ", ")
}

// @concept: orphan-reaper
// @concept: parked-state
func (s *claimHandlesImpl) ListExpired(ctx context.Context, tx persistence.Tx) ([]persistence.ClaimHandleRow, error) {
	rows, err := s.q(tx).Query(ctx,
		`SELECT `+claimHandleCols+` FROM rimsky_claim_handles
		 WHERE state = 'active' AND expires_at < now()
		   AND NOT EXISTS (
		     SELECT 1 FROM rimsky_node_runs nr
		     WHERE nr.id = rimsky_claim_handles.node_run_id
		       AND nr.state IN ('parked', 'held'))
		 ORDER BY expires_at ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("claimhandles.ListExpired: %w", err)
	}
	defer rows.Close()
	return collectClaimHandles(rows)
}

// @concept: orphan-reaper
func (s *claimHandlesImpl) RenewExpiryForHolderRun(ctx context.Context, nodeRunID shared.UUID, expectedSupervisorID string, newExpiry time.Time, tx persistence.Tx) error {
	_, err := s.q(tx).Exec(ctx,
		`UPDATE rimsky_claim_handles
		 SET expires_at = $2
		 WHERE node_run_id = $1 AND state = 'active' AND `+claimantGuard(3),
		nodeRunID, newExpiry, expectedSupervisorID,
	)
	if err != nil {
		return fmt.Errorf("claimhandles.RenewExpiryForHolderRun: %w", err)
	}
	return nil
}

func (s *claimHandlesImpl) Delete(ctx context.Context, id shared.UUID, expectedSupervisorID string, tx persistence.Tx) error {
	cmd, err := s.q(tx).Exec(ctx,
		`DELETE FROM rimsky_claim_handles
		 WHERE id = $1 AND `+claimantGuard(2),
		id, expectedSupervisorID,
	)
	if err != nil {
		return fmt.Errorf("claimhandles.Delete: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		return spec.ErrIllegalClaimHandleTransition
	}
	return nil
}

func (s *claimHandlesImpl) CountByNamedLock(ctx context.Context, lockName string, tx persistence.Tx) (int, error) {
	var n int
	err := s.q(tx).QueryRow(ctx,
		`SELECT count(*) FROM rimsky_claim_handles
		 WHERE lock_kind = 'named' AND lock_name = $1
		   AND state = 'active'`,
		lockName,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("claimhandles.CountByNamedLock: %w", err)
	}
	return n, nil
}

// @concept: claim-handle
// @concept: asset
func (s *claimHandlesImpl) ListByProducerClaimScope(ctx context.Context, producerName string, tx persistence.Tx) ([]persistence.ClaimHandleRow, error) {
	rows, err := s.q(tx).Query(ctx,
		`SELECT `+claimHandleCols+` FROM rimsky_claim_handles
		 WHERE lock_kind = 'claim_scope' AND producer_name = $1
		   AND (state = 'active' OR (state = 'committed' AND lifetime = 'durable'))
		 ORDER BY claimed_at ASC`,
		producerName,
	)
	if err != nil {
		return nil, fmt.Errorf("claimhandles.ListByProducerClaimScope: %w", err)
	}
	defer rows.Close()
	return collectClaimHandles(rows)
}

// @concept: orphan-reaper
func (s *claimHandlesImpl) DeleteIfExpired(ctx context.Context, id shared.UUID, supervisorID string, tx persistence.Tx) (bool, error) {
	tag, err := s.q(tx).Exec(ctx,
		`DELETE FROM rimsky_claim_handles
		 WHERE id = $1
		   AND `+claimantGuard(2)+`
		   AND expires_at < now()
		   AND state = 'active'
		   AND NOT EXISTS (
		     SELECT 1 FROM rimsky_node_runs nr
		     WHERE nr.id = rimsky_claim_handles.node_run_id
		       AND nr.state IN ('parked', 'held'))`,
		id, supervisorID,
	)
	if err != nil {
		return false, fmt.Errorf("claimhandles.DeleteIfExpired: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

func (s *claimHandlesImpl) ListForObservability(ctx context.Context, filter persistence.ClaimHandleListFilter, pag persistence.ListPagination, tx persistence.Tx) (persistence.PaginatedListResult[persistence.ClaimHandleRow], error) {
	limit := pag.Limit
	if limit <= 0 {
		limit = 50
	}
	var producerArg, supArg, nodeTypeArg any
	var nodeArg, instArg any
	if filter.ProducerName != "" {
		producerArg = filter.ProducerName
	}
	if filter.HolderSupervisor != "" {
		supArg = filter.HolderSupervisor
	}
	if filter.HolderNodeID != nil {
		nodeArg = *filter.HolderNodeID
	}
	if filter.InstanceID != nil {
		instArg = *filter.InstanceID
	}
	if filter.NodeType != "" {
		nodeTypeArg = filter.NodeType
	}
	var cursorClaimed *time.Time
	var cursorID *shared.UUID
	if pag.Cursor != "" {
		c, id, err := persistence.DecodeClaimHandleCursor(pag.Cursor)
		if err != nil {
			return persistence.PaginatedListResult[persistence.ClaimHandleRow]{}, persistence.ErrInvalidCursor
		}
		cursorClaimed = &c
		cursorID = &id
	}
	var cArg, cIDArg any
	if cursorClaimed != nil {
		cArg = *cursorClaimed
		cIDArg = *cursorID
	}
	rows, err := s.q(tx).Query(ctx,
		`SELECT `+claimHandleCols+`
		   FROM rimsky_claim_handles lh
		  WHERE ($1::text IS NULL OR lh.producer_name = $1)
		    AND ($2::text IS NULL OR lh.holder_supervisor_id = $2)
		    AND ($3::uuid IS NULL OR lh.holder_node_id = $3)
		    AND (
		         $4::uuid IS NULL OR EXISTS (
		           SELECT 1 FROM rimsky_nodes n
		            WHERE n.id = lh.holder_node_id
		              AND n.instance_id = $4
		         )
		    )
		    AND (
		         $5::text IS NULL OR EXISTS (
		           SELECT 1 FROM rimsky_nodes n
		            WHERE n.id = lh.holder_node_id
		              AND n.node_type = $5
		         )
		    )
		    AND ($6::timestamptz IS NULL OR (lh.claimed_at, lh.id) < ($6, $7))
		  ORDER BY lh.claimed_at DESC, lh.id DESC
		  LIMIT $8`,
		producerArg, supArg, nodeArg, instArg, nodeTypeArg, cArg, cIDArg, limit,
	)
	if err != nil {
		return persistence.PaginatedListResult[persistence.ClaimHandleRow]{}, fmt.Errorf("claimhandles.list: %w", err)
	}
	defer rows.Close()
	out, err := collectClaimHandles(rows)
	if err != nil {
		return persistence.PaginatedListResult[persistence.ClaimHandleRow]{}, err
	}
	var nextCursor string
	if len(out) == limit && len(out) > 0 {
		last := out[len(out)-1]
		nextCursor = persistence.EncodeClaimHandleCursor(last.ClaimedAt, last.ID)
	}
	return persistence.PaginatedListResult[persistence.ClaimHandleRow]{Rows: out, NextCursor: nextCursor}, nil
}

func (s *claimHandlesImpl) SetAggregationPolicy(
	ctx context.Context, id shared.UUID, supervisorID string, policy json.RawMessage, tx persistence.Tx,
) error {
	cmd, err := s.q(tx).Exec(ctx,
		`UPDATE rimsky_claim_handles
		    SET aggregation_policy = $1
		  WHERE id = $2 AND `+claimantGuard(3),
		nullableJSONB(policy), id, supervisorID,
	)
	if err != nil {
		return fmt.Errorf("claimhandles.SetAggregationPolicy: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		return spec.ErrIllegalClaimHandleTransition
	}
	return nil
}

func (s *claimHandlesImpl) BumpExpectedChildrenCount(
	ctx context.Context, id shared.UUID, supervisorID string, delta int, tx persistence.Tx,
) error {
	cmd, err := s.q(tx).Exec(ctx,
		`UPDATE rimsky_claim_handles
		    SET expected_children_count = expected_children_count + $1
		  WHERE id = $2 AND `+claimantGuard(3),
		delta, id, supervisorID,
	)
	if err != nil {
		return fmt.Errorf("claimhandles.BumpExpectedChildrenCount: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		return spec.ErrIllegalClaimHandleTransition
	}
	return nil
}

func (s *claimHandlesImpl) BumpChildOutcomeCount(
	ctx context.Context, id shared.UUID, supervisorID string, outcome string, delta int, tx persistence.Tx,
) error {
	var column string
	switch outcome {
	case "commit":
		column = "committed_children_count"
	case "abandon":
		column = "abandoned_children_count"
	default:
		return fmt.Errorf("claimhandles.BumpChildOutcomeCount: unknown outcome %q (want commit|abandon)", outcome)
	}
	cmd, err := s.q(tx).Exec(ctx,
		`UPDATE rimsky_claim_handles
		    SET `+column+` = `+column+` + $1
		  WHERE id = $2 AND `+claimantGuard(3),
		delta, id, supervisorID,
	)
	if err != nil {
		return fmt.Errorf("claimhandles.BumpChildOutcomeCount: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		return spec.ErrIllegalClaimHandleTransition
	}
	return nil
}

func scanClaimHandle(sc scannable) (persistence.ClaimHandleRow, error) {
	var (
		r                  persistence.ClaimHandleRow
		kind               string
		lockName           *string
		producerName       *string
		scopeData          []byte
		address            []byte
		payload            []byte
		intent             *string
		rws                *string
		holderSupervisorID *string
		frameID            *shared.UUID
		nodeRunID          *shared.UUID
		isHeld             bool
		parentClaimID      *shared.UUID
		lifetime           *string
		versionID          *string
		candidateHandle    []byte
		leaseToken         string
		aggregation        []byte
		expectedChildren   int
		committed          int
		abandoned          int
		stateStr           string
		resolvedAt         *time.Time
	)
	if err := sc.Scan(
		&r.ID, &kind,
		&lockName, &producerName, &scopeData, &address, &payload, &intent,
		&rws,
		&holderSupervisorID, &r.HolderNodeID,
		&r.ClaimedAt, &r.ExpiresAt, &frameID,
		&nodeRunID, &isHeld,
		&parentClaimID, &lifetime, &versionID,
		&candidateHandle, &leaseToken,
		&aggregation, &expectedChildren,
		&committed, &abandoned,
		&stateStr, &resolvedAt,
	); err != nil {
		return persistence.ClaimHandleRow{}, err
	}
	r.LockKind = persistence.LockKind(kind)
	r.LockName = lockName
	r.ProducerName = producerName
	r.ClaimScopeData = scopeData
	r.Address = address
	r.Payload = payload
	r.Intent = intent
	if rws != nil {
		r.RealizedWriteSemantics = *rws
	}
	r.HolderSupervisorID = holderSupervisorID
	r.FrameID = frameID
	r.NodeRunID = nodeRunID
	r.IsHeld = isHeld
	r.ParentClaimHandleID = parentClaimID
	if lifetime != nil {
		r.Lifetime = spec.ClaimLifetime(*lifetime)
	}
	if versionID != nil {
		r.VersionID = *versionID
	}
	r.ProducerCandidateHandle = candidateHandle
	r.ProducerLeaseToken = leaseToken
	r.AggregationPolicy = aggregation
	r.ExpectedChildrenCount = expectedChildren
	r.CommittedChildrenCount = committed
	r.AbandonedChildrenCount = abandoned
	r.State = spec.ClaimHandleState(stateStr)
	r.ResolvedAt = resolvedAt
	return r, nil
}

func collectClaimHandles(rows pgx.Rows) ([]persistence.ClaimHandleRow, error) {
	var out []persistence.ClaimHandleRow
	for rows.Next() {
		r, err := scanClaimHandle(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func nullableJSONB(b json.RawMessage) any {
	if len(b) == 0 {
		return nil
	}
	return []byte(b)
}

func nullableBytes(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}
