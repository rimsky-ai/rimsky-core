// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/action"
	claimproducer "github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

type PickPolicy struct {
	ItemsTable        string
	OnCommit          action.Action
	OnGiveUp          action.Action
	VisibilityTimeout time.Duration
}

func (s *Store) openPickPolicy(ctx context.Context, claimID string, pp *PickPolicy) (claimproducer.OpenOutcome, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return claimproducer.OpenOutcome{}, fmt.Errorf("postgres store: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := fmt.Sprintf(`UPDATE %s
		   SET state = 'in_progress', claim_token = $1, claimed_at = now()
		 WHERE item_id = (
		       SELECT item_id FROM %s
		        WHERE state = 'available'
		        ORDER BY priority DESC, sequence ASC
		          FOR UPDATE SKIP LOCKED
		        LIMIT 1
		       )
		 RETURNING item_id, payload`,
		pp.ItemsTable, pp.ItemsTable,
	)
	row := tx.QueryRow(ctx, q, claimID)
	var (
		itemID  string
		rawJSON []byte
	)
	if err := row.Scan(&itemID, &rawJSON); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return claimproducer.OpenOutcome{Available: false, UnavailableClass: ClaimUnavailableClass}, nil
		}
		return claimproducer.OpenOutcome{}, fmt.Errorf("postgres store: pick: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return claimproducer.OpenOutcome{}, fmt.Errorf("postgres store: commit pick tx: %w", err)
	}

	addrBytes, _ := json.Marshal(itemID)
	scopeBytes, _ := json.Marshal(itemID)

	return claimproducer.OpenOutcome{
		Available: true,
		Result: claimproducer.ClaimResult{
			Address:                json.RawMessage(addrBytes),
			Payload:                rawJSON,
			ClaimScope:             json.RawMessage(scopeBytes),
			RealizedWriteSemantics: s.writeSemantics,
		},
	}, nil
}

func (s *Store) applyPickAction(ctx context.Context, claimID string, successPath bool) error {
	if claimID == "" {
		return nil
	}
	pp, found, err := s.locatePickPolicyForClaim(ctx, claimID)
	if err != nil {
		return fmt.Errorf("postgres store: locate policy for claim: %w", err)
	}
	if !found {
		return nil
	}
	var act action.Action
	if successPath {
		act = pp.OnCommit
	} else {
		act = pp.OnGiveUp
	}
	if !validPickAction(act.Kind) {
		return fmt.Errorf("postgres store: applyPickAction: invalid action %q", act.Kind)
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("postgres store: begin action tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()
	switch act.Kind {
	case action.Pop:
		if _, err := tx.Exec(ctx,
			fmt.Sprintf(`DELETE FROM %s WHERE claim_token = $1`, pp.ItemsTable), claimID,
		); err != nil {
			return fmt.Errorf("postgres store: pop item: %w", err)
		}
	case action.Recycle:
		if _, err := tx.Exec(ctx,
			fmt.Sprintf(`UPDATE %s
			    SET state = 'available', claim_token = NULL, claimed_at = NULL,
			        sequence = nextval(pg_get_serial_sequence($1, 'sequence'))
			  WHERE claim_token = $2`, pp.ItemsTable),
			pp.ItemsTable, claimID,
		); err != nil {
			return fmt.Errorf("postgres store: recycle: %w", err)
		}
	default:
		return fmt.Errorf("postgres store: applyPickAction: action %q not supported by postgres store", act.Kind)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres store: commit action tx: %w", err)
	}
	committed = true
	return nil
}

func (s *Store) locatePickPolicyForClaim(ctx context.Context, claimID string) (*PickPolicy, bool, error) {
	if rec, ok := s.ledger.Get(claimID); ok {
		pp, ok := s.pickPolicies[rec.Selector]
		return pp, ok, nil
	}
	return s.findPolicyForClaim(ctx, claimID)
}

func (s *Store) findPolicyForClaim(ctx context.Context, claimID string) (*PickPolicy, bool, error) {
	for _, pp := range s.pickPolicies {
		var exists bool
		query := fmt.Sprintf(`SELECT EXISTS (SELECT 1 FROM %s WHERE claim_token = $1)`, pp.ItemsTable)
		if err := s.pool.QueryRow(ctx, query, claimID).Scan(&exists); err != nil {
			return nil, false, fmt.Errorf("query items_table %q: %w", pp.ItemsTable, err)
		}
		if exists {
			return pp, true, nil
		}
	}
	return nil, false, nil
}

func validPickAction(k action.Kind) bool {
	return k == action.Pop || k == action.Recycle
}

func validatePickPolicy(selector string, pp *PickPolicy) action.ValidationResult {
	var res action.ValidationResult
	addErr := func(err error) { res.Errors = append(res.Errors, err) }

	if pp == nil {
		addErr(errors.New("policy is nil"))
		return res
	}

	if !validIdent(pp.ItemsTable) {
		addErr(fmt.Errorf("items_table %q is not a valid identifier (lowercase letters/digits/underscore; not starting with a digit)", pp.ItemsTable))
	}

	commitHandled := pgZeroOrRejected("on_commit", pp.OnCommit, addErr)
	giveUpHandled := pgZeroOrRejected("on_give_up", pp.OnGiveUp, addErr)

	if !commitHandled {
		if err := pp.OnCommit.Validate(); err != nil {
			addErr(fmt.Errorf("on_commit: %w", err))
		}
	}
	if !giveUpHandled {
		if err := pp.OnGiveUp.Validate(); err != nil {
			addErr(fmt.Errorf("on_give_up: %w", err))
		}
	}

	if pp.VisibilityTimeout <= 0 {
		addErr(errors.New("visibility_timeout_seconds: must be > 0"))
	}

	_ = selector
	return res
}

func pgZeroOrRejected(slot string, a action.Action, addErr func(error)) bool {
	if a.Kind == "" {
		addErr(fmt.Errorf("%s: required (got null or missing)", slot))
		return true
	}
	switch a.Kind {
	case action.PopAndMove:
		addErr(fmt.Errorf("%s: action %q not supported by postgres store; supported actions are pop and recycle", slot, a.Kind))
		return true
	case action.PopAndDelete:
		addErr(fmt.Errorf("%s: action %q not supported by postgres store (semantically equivalent to pop; use pop)", slot, a.Kind))
		return true
	}
	return false
}
