// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/action"
	claimproducer "github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

var ItemsTableIdentRegex = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

type Store struct {
	pool           *pgxpool.Pool
	writeSemantics claimproducer.WriteSemantics
	pickPolicies   map[string]*PickPolicy
	ledger         *ClaimLedger
}

func (s *Store) Ledger() *ClaimLedger { return s.ledger }

func NewForTest() *Store {
	return &Store{ledger: NewClaimLedger(1024)}
}

type PickPolicy struct {
	ItemsTable        string
	OnCommit          action.Action
	OnGiveUp          action.Action
	VisibilityTimeout time.Duration
}

type Config struct {
	Connection     string
	WriteSemantics claimproducer.WriteSemantics
	PickPolicies   map[string]*PickPolicy
}

func New(ctx context.Context, cfg Config) (*Store, error) {
	if cfg.Connection == "" {
		return nil, errors.New("postgres store: connection is required")
	}
	pool, err := pgxpool.New(ctx, cfg.Connection)
	if err != nil {
		return nil, fmt.Errorf("postgres store: open pool: %w", err)
	}
	if cfg.WriteSemantics == "" {
		cfg.WriteSemantics = claimproducer.WriteSemanticsStagedAsync
	}
	for selector, pp := range cfg.PickPolicies {
		res := validatePickPolicy(selector, pp)
		if !res.OK() {
			pool.Close()
			msgs := make([]string, 0, len(res.Errors))
			for _, e := range res.Errors {
				msgs = append(msgs, e.Error())
			}
			return nil, fmt.Errorf("postgres store: pick_policies[%q]: %s",
				selector, strings.Join(msgs, "; "))
		}
		for _, w := range res.Warnings {
			slog.Warn(w)
		}
		if err := verifyItemsTable(ctx, pool, pp.ItemsTable); err != nil {
			pool.Close()
			return nil, fmt.Errorf("postgres store: pick_policies[%q]: items table %q: %w",
				selector, pp.ItemsTable, err)
		}
	}
	return &Store{
		pool:           pool,
		writeSemantics: cfg.WriteSemantics,
		pickPolicies:   cfg.PickPolicies,
		ledger:         NewClaimLedger(1024),
	}, nil
}

func (s *Store) Close() {
	if s.pool != nil {
		s.pool.Close()
	}
}

func (s *Store) Pool() *pgxpool.Pool { return s.pool }

func (s *Store) Capabilities() claimproducer.Capabilities {
	return claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{s.writeSemantics},
	}
}

func (s *Store) PickPolicies() map[string]PickPolicy {
	out := make(map[string]PickPolicy, len(s.pickPolicies))
	for sel, pp := range s.pickPolicies {
		out[sel] = *pp
	}
	return out
}

func (s *Store) Open(ctx context.Context, claimID, selector string) (claimproducer.OpenOutcome, error) {
	if pp, ok := s.pickPolicies[selector]; ok {
		out, err := s.openPickPolicy(ctx, claimID, pp)
		if err == nil && out.Available {
			s.ledger.RecordOpen(claimID, selector, out.Result.Address, out.Result.ClaimScope)
		}
		return out, err
	}
	if s.stagedScopeBytes(selector) {
		addr, scope, err := s.openStaging(ctx, claimID, selector)
		if err != nil {
			return claimproducer.OpenOutcome{}, err
		}
		s.ledger.RecordOpen(claimID, selector, addr, scope)
		return claimproducer.OpenOutcome{
			Available: true,
			Result: claimproducer.ClaimResult{
				Address:                addr,
				ClaimScope:             scope,
				RealizedWriteSemantics: s.writeSemantics,
			},
		}, nil
	}

	addr, err := json.Marshal(selector)
	if err != nil {
		return claimproducer.OpenOutcome{}, fmt.Errorf("postgres store: marshal selector: %w", err)
	}
	s.ledger.RecordOpen(claimID, selector, addr, addr)
	return claimproducer.OpenOutcome{
		Available: true,
		Result: claimproducer.ClaimResult{
			Address:                json.RawMessage(addr),
			ClaimScope:             json.RawMessage(addr),
			RealizedWriteSemantics: s.writeSemantics,
		},
	}, nil
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

func (s *Store) Commit(ctx context.Context, claimID string, claimScope []byte, address []byte) error {
	swap, canonical, staging, err := s.stagedSwapTarget(claimScope, address)
	if err != nil {
		s.ledger.RecordEvent(claimID, "claim_commit_failed", "ERROR", map[string]any{"error": err.Error()})
		return err
	}
	if swap {
		if err := s.commitStagingSwap(ctx, canonical, staging); err != nil {
			s.ledger.RecordEvent(claimID, "claim_commit_failed", "ERROR", map[string]any{"error": err.Error()})
			return err
		}
		s.ledger.RecordTerminal(claimID, "claim_committed", nil)
		return nil
	}
	if err := s.applyPickAction(ctx, claimID, true); err != nil {
		s.ledger.RecordEvent(claimID, "claim_commit_failed", "ERROR", map[string]any{"error": err.Error()})
		return err
	}
	s.ledger.RecordTerminal(claimID, "claim_committed", nil)
	return nil
}

func (s *Store) Abandon(ctx context.Context, claimID string, claimScope []byte, address []byte) error {
	swap, _, staging, err := s.stagedSwapTarget(claimScope, address)
	if err != nil {
		s.ledger.RecordEvent(claimID, "claim_abandon_failed", "ERROR", map[string]any{"error": err.Error()})
		return err
	}
	if swap {
		if err := s.dropStaging(ctx, staging); err != nil {
			s.ledger.RecordEvent(claimID, "claim_abandon_failed", "ERROR", map[string]any{"error": err.Error()})
			return err
		}
		s.ledger.RecordTerminal(claimID, "claim_abandoned", nil)
		return nil
	}
	if err := s.applyPickAction(ctx, claimID, false); err != nil {
		s.ledger.RecordEvent(claimID, "claim_abandon_failed", "ERROR", map[string]any{"error": err.Error()})
		return err
	}
	s.ledger.RecordTerminal(claimID, "claim_abandoned", nil)
	return nil
}

func (s *Store) Release(ctx context.Context, claimID string, claimScope []byte, address []byte) error {
	swap, _, staging, err := s.stagedSwapTarget(claimScope, address)
	if err != nil {
		s.ledger.RecordEvent(claimID, "claim_release_failed", "ERROR", map[string]any{"error": err.Error()})
		return err
	}
	if swap {
		if err := s.dropStaging(ctx, staging); err != nil {
			s.ledger.RecordEvent(claimID, "claim_release_failed", "ERROR", map[string]any{"error": err.Error()})
			return err
		}
	}
	s.ledger.RecordTerminal(claimID, "claim_released", nil)
	return nil
}

func (s *Store) stagedSwapTarget(claimScope, address []byte) (swap bool, canonical, staging string, err error) {
	if s.writeSemantics != claimproducer.WriteSemanticsStagedAsync {
		return false, "", "", nil
	}
	canonical, err = decodeSchemaName(claimScope)
	if err != nil {
		return false, "", "", err
	}
	staging, err = decodeSchemaName(address)
	if err != nil {
		return false, "", "", err
	}
	if canonical == "" || staging == "" || canonical == staging {
		return false, "", "", nil
	}
	if _, ok := s.pickPolicies[canonical]; ok {
		return false, "", "", nil
	}
	if !schemaIdentRegex.MatchString(canonical) {
		return false, "", "", nil
	}
	return true, canonical, staging, nil
}

func (s *Store) applyPickAction(ctx context.Context, claimID string, successPath bool) error {
	if claimID == "" {
		return nil
	}
	pp, found, err := s.findPolicyForClaim(ctx, claimID)
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

func (s *Store) InsertItems(ctx context.Context, selector string, payloads []json.RawMessage) error {
	pp, ok := s.pickPolicies[selector]
	if !ok {
		return fmt.Errorf("postgres store: InsertItems: no pick policy for selector %q", selector)
	}
	if len(payloads) == 0 {
		return nil
	}
	stmt := fmt.Sprintf(
		`INSERT INTO %s (item_id, payload, state) VALUES ($1, $2::jsonb, 'available')`,
		pp.ItemsTable,
	)
	for i, p := range payloads {
		if len(p) == 0 {
			return fmt.Errorf("postgres store: InsertItems: payload at index %d is empty", i)
		}
		if !json.Valid(p) {
			return fmt.Errorf("postgres store: InsertItems: payload at index %d is not valid JSON", i)
		}
		if _, err := s.pool.Exec(ctx, stmt, uuid.NewString(), []byte(p)); err != nil {
			return fmt.Errorf("postgres store: InsertItems: row %d: %w", i, err)
		}
	}
	return nil
}

func validPickAction(k action.Kind) bool {
	return k == action.Pop || k == action.Recycle
}

func validIdent(s string) bool {
	return ItemsTableIdentRegex.MatchString(s)
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

var expectedColumns = []struct {
	name     string
	dataType string
}{
	{"item_id", "text"},
	{"payload", "jsonb"},
	{"state", "text"},
	{"claim_token", "text"},
	{"claimed_at", "timestamp with time zone"},
	{"enqueued_at", "timestamp with time zone"},
	{"priority", "integer"},
	{"sequence", "bigint"},
}

func verifyItemsTable(ctx context.Context, pool *pgxpool.Pool, table string) error {
	rows, err := pool.Query(ctx,
		`SELECT column_name, data_type
		   FROM information_schema.columns
		  WHERE table_schema = current_schema()
		    AND table_name = $1`,
		table,
	)
	if err != nil {
		return fmt.Errorf("query information_schema.columns: %w", err)
	}
	defer rows.Close()

	got := make(map[string]string)
	for rows.Next() {
		var col, typ string
		if err := rows.Scan(&col, &typ); err != nil {
			return fmt.Errorf("scan column row: %w", err)
		}
		got[col] = typ
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate column rows: %w", err)
	}
	if len(got) == 0 {
		return fmt.Errorf("table not found in current schema (or has zero columns)")
	}
	for _, want := range expectedColumns {
		gotType, ok := got[want.name]
		if !ok {
			return fmt.Errorf("missing column %q (expected type %q)", want.name, want.dataType)
		}
		if gotType != want.dataType {
			return fmt.Errorf("column %q has type %q, expected %q", want.name, gotType, want.dataType)
		}
	}
	return nil
}
