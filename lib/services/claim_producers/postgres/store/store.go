// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	claimproducer "github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

var ItemsTableIdentRegex = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

type Store struct {
	pool              *pgxpool.Pool
	writeSemantics    claimproducer.WriteSemantics
	pickPolicies      map[string]*PickPolicy
	partitionPolicies map[string]*PartitionPolicy
	ledger            *ClaimLedger
}

func (s *Store) Ledger() *ClaimLedger { return s.ledger }

func NewForTest() *Store {
	return &Store{ledger: NewClaimLedger(1024)}
}

func (s *Store) SetPoolForTest(pool *pgxpool.Pool) {
	s.pool = pool
}

func (s *Store) SetPartitionPoliciesForTest(policies map[string]*PartitionPolicy) {
	s.partitionPolicies = policies
}

func (s *Store) SetPickPoliciesForTest(policies map[string]*PickPolicy) {
	s.pickPolicies = policies
}

type Config struct {
	Connection        string
	WriteSemantics    claimproducer.WriteSemantics
	PickPolicies      map[string]*PickPolicy
	PartitionPolicies map[string]*PartitionPolicy
	LedgerMaxRecords  int
}

const defaultLedgerMaxRecords = 1024

func New(ctx context.Context, cfg Config) (*Store, error) {
	if cfg.Connection == "" {
		return nil, errors.New("postgres store: connection is required")
	}
	pool, err := pgxpool.New(ctx, cfg.Connection)
	if err != nil {
		return nil, fmt.Errorf("postgres store: open pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres store: ping pool: %w", err)
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
			slog.Warn("POSTGRESSTORE.PICKPOLICY.WARNED", "selector", selector, "detail", w)
		}
		if err := verifyItemsTable(ctx, pool, pp.ItemsTable); err != nil {
			pool.Close()
			return nil, fmt.Errorf("postgres store: pick_policies[%q]: items table %q: %w",
				selector, pp.ItemsTable, err)
		}
	}
	for name, pp := range cfg.PartitionPolicies {
		if err := validatePartitionPolicy(name, pp); err != nil {
			pool.Close()
			return nil, err
		}
		if err := verifyTableExists(ctx, pool, pp.ItemsTable); err != nil {
			pool.Close()
			return nil, fmt.Errorf("postgres store: partition_policies[%q]: items table %q: %w",
				name, pp.ItemsTable, err)
		}
	}
	ledgerMax := cfg.LedgerMaxRecords
	if ledgerMax <= 0 {
		ledgerMax = defaultLedgerMaxRecords
	}
	st := &Store{
		pool:              pool,
		writeSemantics:    cfg.WriteSemantics,
		pickPolicies:      cfg.PickPolicies,
		partitionPolicies: cfg.PartitionPolicies,
		ledger:            NewClaimLedger(ledgerMax),
	}
	if err := st.ensureStagingReservationsTable(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return st, nil
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

func (s *Store) PartitionPolicies() map[string]PartitionPolicy {
	out := make(map[string]PartitionPolicy, len(s.partitionPolicies))
	for name, pp := range s.partitionPolicies {
		out[name] = *pp
	}
	return out
}

func (s *Store) PartitionPolicy(name string) (*PartitionPolicy, bool) {
	pp, ok := s.partitionPolicies[name]
	if !ok {
		return nil, false
	}
	return pp, true
}

type ClaimLookup struct {
	ClaimID  string
	Selector string
	Address  []byte
	Scope    []byte
	IsOpen   bool
}

func (s *Store) LookupClaim(claimID string) (ClaimLookup, bool) {
	rec, ok := s.ledger.Get(claimID)
	if !ok {
		return ClaimLookup{}, false
	}
	return ClaimLookup{
		ClaimID:  rec.ClaimID,
		Selector: rec.Selector,
		Address:  rec.Address,
		Scope:    rec.Scope,
		IsOpen:   rec.State == ClaimStateOpen,
	}, true
}

func (s *Store) Open(ctx context.Context, claimID, selector string, intent claimproducer.Intent) (claimproducer.OpenOutcome, error) {
	if pp, ok := s.pickPolicies[selector]; ok {
		out, err := s.openPickPolicy(ctx, claimID, pp)
		if err == nil && out.Available {
			s.ledger.RecordOpen(claimID, selector, out.Result.Address, out.Result.ClaimScope)
		}
		return out, err
	}
	if s.writeSemantics == claimproducer.WriteSemanticsStagedAsync && intent != claimproducer.IntentRead {
		if !ItemsTableIdentRegex.MatchString(selector) {
			return claimproducer.OpenOutcome{}, fmt.Errorf(
				"postgres store: staged_async write on selector %q requires a valid schema identifier "+
					"(lowercase letters/digits/underscore; not starting with a digit); rejecting rather than "+
					"silently realizing an in-place write while reporting staged_async coexistence", selector)
		}
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
	if !isJSONStringShape(claimScope) || !isJSONStringShape(address) {
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
	if !ItemsTableIdentRegex.MatchString(canonical) {
		return false, "", "", nil
	}
	return true, canonical, staging, nil
}

func isJSONStringShape(raw []byte) bool {
	for _, b := range raw {
		switch b {
		case ' ', '\t', '\n', '\r':
			continue
		case '"':
			return true
		default:
			return false
		}
	}
	return false
}

func (s *Store) InsertItems(ctx context.Context, selector string, payloads []json.RawMessage) error {
	pp, ok := s.pickPolicies[selector]
	if !ok {
		return fmt.Errorf("postgres store: InsertItems: no pick policy for selector %q", selector)
	}
	if len(payloads) == 0 {
		return nil
	}
	for i, p := range payloads {
		if len(p) == 0 {
			return fmt.Errorf("postgres store: InsertItems: payload at index %d is empty", i)
		}
		if !json.Valid(p) {
			return fmt.Errorf("postgres store: InsertItems: payload at index %d is not valid JSON", i)
		}
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("postgres store: InsertItems: begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()
	stmt := fmt.Sprintf(
		`INSERT INTO %s (item_id, payload, state) VALUES ($1, $2::jsonb, 'available')`,
		pp.ItemsTable,
	)
	for i, p := range payloads {
		if _, err := tx.Exec(ctx, stmt, uuid.NewString(), []byte(p)); err != nil {
			return fmt.Errorf("postgres store: InsertItems: row %d: %w", i, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres store: InsertItems: commit tx: %w", err)
	}
	committed = true
	return nil
}

func validIdent(s string) bool {
	return ItemsTableIdentRegex.MatchString(s)
}
