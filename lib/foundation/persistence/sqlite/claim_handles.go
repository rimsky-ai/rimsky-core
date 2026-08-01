// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

const claimHandleCols = `
  id, lock_kind, lock_name, producer_name, claim_scope_data, address, payload, intent,
  realized_write_semantics,
  holder_supervisor_id, holder_node_id,
  claimed_at, expires_at, frame_id,
  node_run_id, is_held,
  parent_claim_handle_id, lifetime, version_id,
  producer_candidate_handle, producer_lease_token,
  aggregation_policy, expected_children_count,
  committed_children_count, abandoned_children_count,
  state, resolved_at
`

const claimantGuardClause = `holder_supervisor_id = ?`

func (s *claimHandlesImpl) Insert(ctx context.Context, in persistence.ClaimHandleInsertInput, tx persistence.Tx) error {
	now := nowUTC()
	if !in.ClaimedAtOverride.IsZero() {
		now = formatTime(in.ClaimedAtOverride)
	}
	var rws *string
	if in.RealizedWriteSemantics != "" {
		v := in.RealizedWriteSemantics
		rws = &v
	}
	isHeldInt := 0
	if in.IsHeld {
		isHeldInt = 1
	}
	lifetime := in.Lifetime
	if lifetime == "" {
		lifetime = spec.ClaimLifetimeSubgraph
	}
	var candidateHandle any
	if len(in.ProducerCandidateHandle) > 0 {
		candidateHandle = in.ProducerCandidateHandle
	}
	var aggPolicy any
	if len(in.AggregationPolicy) > 0 {
		aggPolicy = string(in.AggregationPolicy)
	}
	_, err := s.q(tx).ExecContext(ctx,
		`INSERT INTO rimsky_claim_handles (
		   id, lock_kind, lock_name, producer_name, claim_scope_data, address, payload, intent,
		   realized_write_semantics,
		   holder_supervisor_id, holder_node_id,
		   claimed_at, expires_at, frame_id,
		   node_run_id, is_held,
		   parent_claim_handle_id, lifetime, producer_candidate_handle, producer_lease_token,
		   aggregation_policy
		 ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		in.ID.String(), string(in.LockKind),
		in.LockName, in.ProducerName,
		nullableJSONB(in.ClaimScopeData), nullableJSONB(in.Address), nullableJSONB(in.Payload),
		in.Intent,
		rws,
		in.HolderSupervisorID, in.HolderNodeID.String(),
		now, formatTime(in.ExpiresAt), nullableUUID(in.FrameID),
		nullableUUID(in.NodeRunID), isHeldInt,
		nullableUUID(in.ParentClaimHandleID), string(lifetime), candidateHandle, in.ProducerLeaseToken,
		aggPolicy,
	)
	if err != nil {
		return fmt.Errorf("claimhandles.Insert: %w", err)
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
	res, err := s.q(tx).ExecContext(ctx,
		`UPDATE rimsky_claim_handles
		    SET realized_write_semantics = ?
		  WHERE id = ? AND `+claimantGuardClause,
		v, id.String(), supervisorID,
	)
	if err != nil {
		return fmt.Errorf("claimhandles.UpdateRealizedWriteSemantics: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("claimhandles.UpdateRealizedWriteSemantics: rows-affected: %w", err)
	}
	if n == 0 {
		return spec.ErrIllegalClaimHandleTransition
	}
	return nil
}

func (s *claimHandlesImpl) UpdateAddress(
	ctx context.Context, id shared.UUID, supervisorID string, address json.RawMessage, tx persistence.Tx,
) error {
	res, err := s.q(tx).ExecContext(ctx,
		`UPDATE rimsky_claim_handles
		    SET address = ?
		  WHERE id = ? AND `+claimantGuardClause,
		nullableJSONB(address), id.String(), supervisorID,
	)
	if err != nil {
		return fmt.Errorf("claimhandles.UpdateAddress: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("claimhandles.UpdateAddress: rows-affected: %w", err)
	}
	if n == 0 {
		return spec.ErrIllegalClaimHandleTransition
	}
	return nil
}

func (s *claimHandlesImpl) UpdatePayload(
	ctx context.Context, id shared.UUID, supervisorID string, payload json.RawMessage, tx persistence.Tx,
) error {
	res, err := s.q(tx).ExecContext(ctx,
		`UPDATE rimsky_claim_handles
		    SET payload = ?
		  WHERE id = ? AND `+claimantGuardClause,
		nullableJSONB(payload), id.String(), supervisorID,
	)
	if err != nil {
		return fmt.Errorf("claimhandles.UpdatePayload: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("claimhandles.UpdatePayload: rows-affected: %w", err)
	}
	if n == 0 {
		return spec.ErrIllegalClaimHandleTransition
	}
	return nil
}

func (s *claimHandlesImpl) UpdateClaimScope(
	ctx context.Context, id shared.UUID, supervisorID string, scope json.RawMessage, tx persistence.Tx,
) error {
	res, err := s.q(tx).ExecContext(ctx,
		`UPDATE rimsky_claim_handles
		    SET claim_scope_data = ?
		  WHERE id = ? AND `+claimantGuardClause,
		nullableJSONB(scope), id.String(), supervisorID,
	)
	if err != nil {
		return fmt.Errorf("claimhandles.UpdateClaimScope: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("claimhandles.UpdateClaimScope: rows-affected: %w", err)
	}
	if n == 0 {
		return spec.ErrIllegalClaimHandleTransition
	}
	return nil
}

func (s *claimHandlesImpl) UpdateNodeRunID(
	ctx context.Context, id shared.UUID, nodeRunID shared.UUID, supervisorID string, tx persistence.Tx,
) error {
	res, err := s.q(tx).ExecContext(ctx,
		`UPDATE rimsky_claim_handles
		    SET node_run_id = ?
		  WHERE id = ?
		    AND state = 'active'
		    AND `+claimantGuardClause,
		nodeRunID.String(), id.String(), supervisorID,
	)
	if err != nil {
		return fmt.Errorf("claimhandles.UpdateNodeRunID: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("claimhandles.UpdateNodeRunID: rows-affected: %w", err)
	}
	if n == 0 {
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
	res, err := s.q(tx).ExecContext(ctx,
		`UPDATE rimsky_claim_handles
		    SET holder_supervisor_id = ?
		  WHERE id = ?
		    AND state = 'active'
		    AND `+claimantGuardClause,
		toSupervisorID, id.String(), fromSupervisorID,
	)
	if err != nil {
		return fmt.Errorf("claimhandles.ReassignHolderSupervisor: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("claimhandles.ReassignHolderSupervisor: rows-affected: %w", err)
	}
	if n == 0 {
		return spec.ErrIllegalClaimHandleTransition
	}
	return nil
}

func (s *claimHandlesImpl) Get(ctx context.Context, id shared.UUID, tx persistence.Tx) (*persistence.ClaimHandleRow, error) {
	row := s.q(tx).QueryRowContext(ctx,
		`SELECT `+claimHandleCols+` FROM rimsky_claim_handles WHERE id = ?`, id.String(),
	)
	out, err := scanClaimHandle(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &out, nil
}

func (s *claimHandlesImpl) LockForUpdate(ctx context.Context, id shared.UUID, tx persistence.Tx) (*persistence.ClaimHandleRow, error) {
	row := s.q(tx).QueryRowContext(ctx,
		`SELECT `+claimHandleCols+` FROM rimsky_claim_handles WHERE id = ?`, id.String(),
	)
	out, err := scanClaimHandle(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("claimhandles.LockForUpdate: %w", err)
	}
	return &out, nil
}

func (s *claimHandlesImpl) ListByHolderNode(ctx context.Context, holderNodeID shared.UUID, tx persistence.Tx) ([]persistence.ClaimHandleRow, error) {
	rows, err := s.q(tx).QueryContext(ctx,
		`SELECT `+claimHandleCols+` FROM rimsky_claim_handles
		 WHERE holder_node_id = ?
		 ORDER BY claimed_at ASC`, holderNodeID.String(),
	)
	if err != nil {
		return nil, fmt.Errorf("claimhandles.ListByHolderNode: %w", err)
	}
	defer rows.Close()
	return collectClaimHandles(rows)
}

func (s *claimHandlesImpl) ListByNodeRun(ctx context.Context, nodeRunID shared.UUID, tx persistence.Tx) ([]persistence.ClaimHandleRow, error) {
	rows, err := s.q(tx).QueryContext(ctx,
		`SELECT `+claimHandleCols+` FROM rimsky_claim_handles
		 WHERE node_run_id = ?
		 ORDER BY claimed_at ASC`, nodeRunID.String(),
	)
	if err != nil {
		return nil, fmt.Errorf("claimhandles.ListByNodeRun: %w", err)
	}
	defer rows.Close()
	return collectClaimHandles(rows)
}

func (s *claimHandlesImpl) GetByFrameAndNode(ctx context.Context, nodeID shared.UUID, frameID shared.UUID, tx persistence.Tx) (*persistence.ClaimHandleRow, error) {
	row := s.q(tx).QueryRowContext(ctx,
		`SELECT `+claimHandleCols+` FROM rimsky_claim_handles
		 WHERE holder_node_id = ? AND frame_id = ?
		 LIMIT 1`,
		nodeID.String(), frameID.String(),
	)
	out, err := scanClaimHandle(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("claimhandles.GetByFrameAndNode: %w", err)
	}
	return &out, nil
}

func (s *claimHandlesImpl) ListChildClaimHandles(ctx context.Context, parentID shared.UUID, tx persistence.Tx) ([]persistence.ClaimHandleRow, error) {
	rows, err := s.q(tx).QueryContext(ctx,
		`SELECT `+claimHandleCols+` FROM rimsky_claim_handles
		 WHERE parent_claim_handle_id = ?`,
		parentID.String())
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
	res, err := (*tablesImpl)(s).db.ExecContext(ctx,
		`DELETE FROM rimsky_claim_handles
		  WHERE state IN ('committed', 'abandoned')
		    AND (state = 'abandoned' OR lifetime = 'subgraph')
		    AND resolved_at < ?
		    AND holder_supervisor_id IS NULL`,
		formatTime(cutoff))
	if err != nil {
		return 0, fmt.Errorf("sqlite.ClaimHandles.DeleteResolvedOlderThan: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("sqlite.ClaimHandles.DeleteResolvedOlderThan: rows-affected: %w", err)
	}
	return int(n), nil
}

// @concept: claim-handle
func (s *claimHandlesImpl) DeleteResolved(
	ctx context.Context, id shared.UUID, tx persistence.Tx,
) error {
	res, err := s.q(tx).ExecContext(ctx,
		`DELETE FROM rimsky_claim_handles
		  WHERE id = ?
		    AND state IN ('committed', 'abandoned')
		    AND holder_supervisor_id IS NULL`, id.String())
	if err != nil {
		return fmt.Errorf("claimhandles.DeleteResolved: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("claimhandles.DeleteResolved: rows-affected: %w", err)
	}
	if n == 0 {
		return spec.ErrIllegalClaimHandleTransition
	}
	return nil
}

// @concept: claim-handle
// @concept: claim-co-holdership
func (s *claimHandlesImpl) DeleteResolvedIfNoActiveHolders(
	ctx context.Context, id shared.UUID, tx persistence.Tx,
) (bool, error) {
	res, err := s.q(tx).ExecContext(ctx,
		`DELETE FROM rimsky_claim_handles
		  WHERE id = ?
		    AND state IN ('committed', 'abandoned')
		    AND holder_supervisor_id IS NULL
		    AND NOT EXISTS (
		      SELECT 1 FROM rimsky_claim_holders
		       WHERE rimsky_claim_holders.claim_handle_id = rimsky_claim_handles.id
		         AND rimsky_claim_holders.state = 'active'
		    )`, id.String())
	if err != nil {
		return false, fmt.Errorf("claimhandles.DeleteResolvedIfNoActiveHolders: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("claimhandles.DeleteResolvedIfNoActiveHolders: rows-affected: %w", err)
	}
	return n > 0, nil
}

// @concept: claim-handle
func (s *claimHandlesImpl) Promote(
	ctx context.Context, id shared.UUID, supervisorID string,
	newState spec.ClaimHandleState, tx persistence.Tx,
) error {
	res, err := s.q(tx).ExecContext(ctx,
		`UPDATE rimsky_claim_handles
		    SET state = ?,
		        holder_supervisor_id = NULL,
		        resolved_at = ?
		  WHERE id = ?
		    AND state = 'active'
		    AND `+claimantGuardClause,
		string(newState), nowUTC(), id.String(), supervisorID)
	if err != nil {
		return fmt.Errorf("claimhandles.Promote: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("claimhandles.Promote: rows-affected: %w", err)
	}
	if n == 0 {
		return spec.ErrIllegalClaimHandleTransition
	}
	return nil
}

func (s *claimHandlesImpl) ListByState(
	ctx context.Context, state spec.ClaimHandleState, tx persistence.Tx,
) ([]persistence.ClaimHandleRow, error) {
	rows, err := s.q(tx).QueryContext(ctx,
		`SELECT `+claimHandleCols+` FROM rimsky_claim_handles
		 WHERE state = ?
		 ORDER BY claimed_at ASC`, string(state))
	if err != nil {
		return nil, fmt.Errorf("claimhandles.ListByState: %w", err)
	}
	defer rows.Close()
	return collectClaimHandles(rows)
}

func (s *claimHandlesImpl) ListByInstanceAndState(
	ctx context.Context, instanceID shared.UUID,
	state spec.ClaimHandleState, lifetime spec.ClaimLifetime, tx persistence.Tx,
) ([]persistence.ClaimHandleRow, error) {
	rows, err := s.q(tx).QueryContext(ctx,
		`SELECT `+qualifiedClaimHandleCols("ch")+`
		   FROM rimsky_claim_handles ch
		   JOIN rimsky_nodes n ON n.id = ch.holder_node_id
		  WHERE n.instance_id = ?
		    AND ch.state = ?
		    AND ch.lifetime = ?`,
		instanceID.String(), string(state), string(lifetime))
	if err != nil {
		return nil, fmt.Errorf("claimhandles.ListByInstanceAndState: %w", err)
	}
	defer rows.Close()
	return collectClaimHandles(rows)
}

func (s *claimHandlesImpl) SetVersionID(
	ctx context.Context, id shared.UUID, supervisorID string, versionID string, tx persistence.Tx,
) error {
	var v any
	if versionID != "" {
		v = versionID
	}
	res, err := s.q(tx).ExecContext(ctx,
		`UPDATE rimsky_claim_handles SET version_id = ?
		 WHERE id = ? AND (`+claimantGuardClause+`
		    OR (holder_supervisor_id IS NULL AND state <> 'active'))`,
		v, id.String(), supervisorID)
	if err != nil {
		return fmt.Errorf("claimhandles.SetVersionID: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("claimhandles.SetVersionID: rows-affected: %w", err)
	}
	if n == 0 {
		return spec.ErrIllegalClaimHandleTransition
	}
	return nil
}

func (s *claimHandlesImpl) SetAggregationPolicy(
	ctx context.Context, id shared.UUID, supervisorID string, policy json.RawMessage, tx persistence.Tx,
) error {
	var v any
	if len(policy) > 0 {
		v = string(policy)
	}
	res, err := s.q(tx).ExecContext(ctx,
		`UPDATE rimsky_claim_handles
		    SET aggregation_policy = ?
		  WHERE id = ? AND `+claimantGuardClause,
		v, id.String(), supervisorID,
	)
	if err != nil {
		return fmt.Errorf("claimhandles.SetAggregationPolicy: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("claimhandles.SetAggregationPolicy: rows-affected: %w", err)
	}
	if n == 0 {
		return spec.ErrIllegalClaimHandleTransition
	}
	return nil
}

func (s *claimHandlesImpl) BumpExpectedChildrenCount(
	ctx context.Context, id shared.UUID, supervisorID string, delta int, tx persistence.Tx,
) error {
	res, err := s.q(tx).ExecContext(ctx,
		`UPDATE rimsky_claim_handles
		    SET expected_children_count = expected_children_count + ?
		  WHERE id = ? AND `+claimantGuardClause,
		delta, id.String(), supervisorID,
	)
	if err != nil {
		return fmt.Errorf("claimhandles.BumpExpectedChildrenCount: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("claimhandles.BumpExpectedChildrenCount: rows-affected: %w", err)
	}
	if n == 0 {
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
	res, err := s.q(tx).ExecContext(ctx,
		`UPDATE rimsky_claim_handles
		    SET `+column+` = `+column+` + ?
		  WHERE id = ? AND `+claimantGuardClause,
		delta, id.String(), supervisorID,
	)
	if err != nil {
		return fmt.Errorf("claimhandles.BumpChildOutcomeCount: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("claimhandles.BumpChildOutcomeCount: rows-affected: %w", err)
	}
	if n == 0 {
		return spec.ErrIllegalClaimHandleTransition
	}
	return nil
}

func qualifiedClaimHandleCols(alias string) string {
	return alias + `.id, ` + alias + `.lock_kind, ` + alias + `.lock_name, ` +
		alias + `.producer_name, ` + alias + `.claim_scope_data, ` + alias + `.address, ` +
		alias + `.payload, ` +
		alias + `.intent, ` + alias + `.realized_write_semantics, ` +
		alias + `.holder_supervisor_id, ` + alias + `.holder_node_id, ` +
		alias + `.claimed_at, ` + alias + `.expires_at, ` +
		alias + `.frame_id, ` + alias + `.node_run_id, ` + alias + `.is_held, ` +
		alias + `.parent_claim_handle_id, ` + alias + `.lifetime, ` +
		alias + `.version_id, ` +
		alias + `.producer_candidate_handle, ` + alias + `.producer_lease_token, ` +
		alias + `.aggregation_policy, ` + alias + `.expected_children_count, ` +
		alias + `.committed_children_count, ` + alias + `.abandoned_children_count, ` +
		alias + `.state, ` + alias + `.resolved_at`
}

// @concept: orphan-reaper
// @concept: parked-state
func (s *claimHandlesImpl) ListExpired(ctx context.Context, tx persistence.Tx) ([]persistence.ClaimHandleRow, error) {
	rows, err := s.q(tx).QueryContext(ctx,
		`SELECT `+claimHandleCols+` FROM rimsky_claim_handles
		 WHERE state = 'active' AND expires_at < ?
		   AND NOT EXISTS (
		     SELECT 1 FROM rimsky_node_runs nr
		     WHERE nr.id = rimsky_claim_handles.node_run_id
		       AND nr.state IN ('parked', 'held'))
		 ORDER BY expires_at ASC`, nowUTC(),
	)
	if err != nil {
		return nil, fmt.Errorf("claimhandles.ListExpired: %w", err)
	}
	defer rows.Close()
	return collectClaimHandles(rows)
}

// @concept: orphan-reaper
func (s *claimHandlesImpl) RenewExpiryForHolderRun(ctx context.Context, nodeRunID shared.UUID, newExpiry time.Time, tx persistence.Tx) error {
	_, err := s.q(tx).ExecContext(ctx,
		`UPDATE rimsky_claim_handles
		 SET expires_at = ?
		 WHERE node_run_id = ? AND state = 'active'`,
		formatTime(newExpiry), nodeRunID.String(),
	)
	if err != nil {
		return fmt.Errorf("claimhandles.RenewExpiryForHolderRun: %w", err)
	}
	return nil
}

func (s *claimHandlesImpl) Delete(ctx context.Context, id shared.UUID, expectedSupervisorID string, tx persistence.Tx) error {
	res, err := s.q(tx).ExecContext(ctx,
		`DELETE FROM rimsky_claim_handles
		 WHERE id = ? AND `+claimantGuardClause,
		id.String(), expectedSupervisorID,
	)
	if err != nil {
		return fmt.Errorf("claimhandles.Delete: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("claimhandles.Delete: rows-affected: %w", err)
	}
	if n == 0 {
		return spec.ErrIllegalClaimHandleTransition
	}
	return nil
}

func (s *claimHandlesImpl) CountByNamedLock(ctx context.Context, lockName string, tx persistence.Tx) (int, error) {
	var n int
	err := s.q(tx).QueryRowContext(ctx,
		`SELECT count(*) FROM rimsky_claim_handles
		 WHERE lock_kind = 'named' AND lock_name = ?
		   AND state = 'active'`,
		lockName,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("claimhandles.CountByNamedLock: %w", err)
	}
	return n, nil
}

func (s *claimHandlesImpl) ListByProducerClaimScope(ctx context.Context, producerName string, tx persistence.Tx) ([]persistence.ClaimHandleRow, error) {
	rows, err := s.q(tx).QueryContext(ctx,
		`SELECT `+claimHandleCols+` FROM rimsky_claim_handles
		 WHERE lock_kind = 'claim_scope' AND producer_name = ?
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

func (s *claimHandlesImpl) DeleteIfExpired(ctx context.Context, id shared.UUID, supervisorID string, tx persistence.Tx) (bool, error) {
	res, err := s.q(tx).ExecContext(ctx,
		`DELETE FROM rimsky_claim_handles
		 WHERE id = ?
		   AND `+claimantGuardClause+`
		   AND expires_at < ?
		   AND state = 'active'
		   AND NOT EXISTS (
		     SELECT 1 FROM rimsky_node_runs nr
		     WHERE nr.id = rimsky_claim_handles.node_run_id
		       AND nr.state IN ('parked', 'held'))`,
		id.String(), supervisorID, nowUTC(),
	)
	if err != nil {
		return false, fmt.Errorf("claimhandles.DeleteIfExpired: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("claimhandles.DeleteIfExpired: rows-affected: %w", err)
	}
	return n > 0, nil
}

func (s *claimHandlesImpl) ListForObservability(ctx context.Context, filter persistence.ClaimHandleListFilter, pag persistence.ListPagination, tx persistence.Tx) (persistence.PaginatedListResult[persistence.ClaimHandleRow], error) {
	limit := pag.Limit
	if limit <= 0 {
		limit = 50
	}
	var producerArg, supArg, nodeArg, instArg, nodeTypeArg any
	if filter.ProducerName != "" {
		producerArg = filter.ProducerName
	}
	if filter.HolderSupervisor != "" {
		supArg = filter.HolderSupervisor
	}
	if filter.HolderNodeID != nil {
		nodeArg = filter.HolderNodeID.String()
	}
	if filter.InstanceID != nil {
		instArg = filter.InstanceID.String()
	}
	if filter.NodeType != "" {
		nodeTypeArg = filter.NodeType
	}
	var cursorClaimed, cursorID any
	if pag.Cursor != "" {
		c, id, err := persistence.DecodeClaimHandleCursor(pag.Cursor)
		if err != nil {
			return persistence.PaginatedListResult[persistence.ClaimHandleRow]{}, fmt.Errorf("claimhandles.list: bad cursor: %w", err)
		}
		cursorClaimed = formatTime(c)
		cursorID = id.String()
	}
	rows, err := s.q(tx).QueryContext(ctx,
		`SELECT `+claimHandleCols+`
		   FROM rimsky_claim_handles lh
		  WHERE (? IS NULL OR lh.producer_name = ?)
		    AND (? IS NULL OR lh.holder_supervisor_id = ?)
		    AND (? IS NULL OR lh.holder_node_id = ?)
		    AND (
		         ? IS NULL OR EXISTS (
		           SELECT 1 FROM rimsky_nodes n
		            WHERE n.id = lh.holder_node_id AND n.instance_id = ?
		         )
		    )
		    AND (
		         ? IS NULL OR EXISTS (
		           SELECT 1 FROM rimsky_nodes n
		            WHERE n.id = lh.holder_node_id AND n.node_type = ?
		         )
		    )
		    AND (? IS NULL OR (lh.claimed_at, lh.id) < (?, ?))
		  ORDER BY lh.claimed_at DESC, lh.id DESC
		  LIMIT ?`,
		producerArg, producerArg, supArg, supArg, nodeArg, nodeArg,
		instArg, instArg, nodeTypeArg, nodeTypeArg,
		cursorClaimed, cursorClaimed, cursorID, limit,
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

func scanClaimHandle(sc scannable) (persistence.ClaimHandleRow, error) {
	var (
		r                  persistence.ClaimHandleRow
		idStr              string
		kind               string
		lockName           sql.NullString
		producerName       sql.NullString
		scopeData          sql.NullString
		address            sql.NullString
		payload            sql.NullString
		intent             sql.NullString
		rws                sql.NullString
		holderSupervisorID sql.NullString
		holderNodeIDStr    string
		claimedAtStr       string
		expiresAtStr       string
		frameIDStr         sql.NullString
		nodeRunIDStr       sql.NullString
		isHeldInt          int
		parentClaimIDStr   sql.NullString
		lifetime           sql.NullString
		versionID          sql.NullString
		candidateHandle    []byte
		leaseToken         string
		aggregation        sql.NullString
		expectedChildren   int
		committed          int
		abandoned          int
		stateStr           string
		resolvedAtStr      sql.NullString
	)
	if err := sc.Scan(
		&idStr, &kind,
		&lockName, &producerName, &scopeData, &address, &payload, &intent,
		&rws,
		&holderSupervisorID, &holderNodeIDStr,
		&claimedAtStr, &expiresAtStr, &frameIDStr,
		&nodeRunIDStr, &isHeldInt,
		&parentClaimIDStr, &lifetime, &versionID,
		&candidateHandle, &leaseToken,
		&aggregation, &expectedChildren,
		&committed, &abandoned,
		&stateStr, &resolvedAtStr,
	); err != nil {
		return persistence.ClaimHandleRow{}, err
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return persistence.ClaimHandleRow{}, err
	}
	holderNodeID, err := uuid.Parse(holderNodeIDStr)
	if err != nil {
		return persistence.ClaimHandleRow{}, err
	}
	r.ID = id
	r.LockKind = persistence.LockKind(kind)
	if holderSupervisorID.Valid {
		v := holderSupervisorID.String
		r.HolderSupervisorID = &v
	}
	r.HolderNodeID = holderNodeID
	if lockName.Valid {
		v := lockName.String
		r.LockName = &v
	}
	if producerName.Valid {
		v := producerName.String
		r.ProducerName = &v
	}
	if scopeData.Valid {
		r.ClaimScopeData = json.RawMessage(scopeData.String)
	}
	if address.Valid {
		r.Address = json.RawMessage(address.String)
	}
	if payload.Valid {
		r.Payload = json.RawMessage(payload.String)
	}
	if intent.Valid {
		v := intent.String
		r.Intent = &v
	}
	if rws.Valid {
		r.RealizedWriteSemantics = rws.String
	}
	frameID, err := scanNullableUUID(frameIDStr)
	if err != nil {
		return persistence.ClaimHandleRow{}, err
	}
	r.FrameID = frameID
	nodeRunID, err := scanNullableUUID(nodeRunIDStr)
	if err != nil {
		return persistence.ClaimHandleRow{}, err
	}
	r.NodeRunID = nodeRunID
	r.IsHeld = isHeldInt != 0
	parentClaimID, err := scanNullableUUID(parentClaimIDStr)
	if err != nil {
		return persistence.ClaimHandleRow{}, err
	}
	r.ParentClaimHandleID = parentClaimID
	if lifetime.Valid {
		r.Lifetime = spec.ClaimLifetime(lifetime.String)
	}
	if versionID.Valid {
		r.VersionID = versionID.String
	}
	r.ProducerCandidateHandle = candidateHandle
	r.ProducerLeaseToken = leaseToken
	if aggregation.Valid {
		r.AggregationPolicy = json.RawMessage(aggregation.String)
	}
	r.ExpectedChildrenCount = expectedChildren
	r.CommittedChildrenCount = committed
	r.AbandonedChildrenCount = abandoned
	r.State = spec.ClaimHandleState(stateStr)
	if resolvedAtStr.Valid {
		t, perr := parseTime(resolvedAtStr.String)
		if perr != nil {
			return persistence.ClaimHandleRow{}, perr
		}
		r.ResolvedAt = &t
	}
	if r.ClaimedAt, err = parseTime(claimedAtStr); err != nil {
		return persistence.ClaimHandleRow{}, err
	}
	if r.ExpiresAt, err = parseTime(expiresAtStr); err != nil {
		return persistence.ClaimHandleRow{}, err
	}
	return r, nil
}

func collectClaimHandles(rows *sql.Rows) ([]persistence.ClaimHandleRow, error) {
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
