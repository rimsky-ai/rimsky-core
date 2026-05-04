// Package store is the store-internal logic for the standard
// postgres store-service. Per spec §8.2: regional access via byte-equal
// region match (selector echoed); pick-policy access via FOR UPDATE SKIP
// LOCKED on operator-owned items tables.
//
// Store atomicity is the store's concern: every state mutation
// happens inside the store's own pgx tx. Rimsky's bookkeeping tx
// is decoupled per spec §7.3.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	corestore "github.com/fallguy/rimsky/foundation/locks"
)

// ItemsTableIdentRegex is the strict identifier shape every layer must
// enforce before an items_table value reaches a SQL literal. Lowercase
// only because postgres folds unquoted identifiers to lowercase: a
// mixed-case value would pass an early check then silently mismatch
// the verifyItemsTable schema lookup at runtime.
//
// Shared by:
//   - stores/postgres/cmd/main.go: rejects mixed-case at startup with
//     the same message users see at the store boundary
//   - stores/postgres/server/observability.go: defense-in-depth against
//     items_queue admin-view interpolation
//   - validIdent below: applied inside Store.New
var ItemsTableIdentRegex = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

// Store is the in-process store implementation. Owns its own pgxpool.Pool; lock
// state lives on rimsky's side and is not consulted here.
//
// No in-process claim tracking: the items table's `claim_token`
// column is the canonical record of in-flight pick-policy claims, and
// regional claims have nothing to track. An earlier draft kept a
// claim_id → item_id map but no consumer ever read it; removed to
// eliminate drift.
type Store struct {
	pool           *pgxpool.Pool
	writeSemantics corestore.WriteSemantics
	pickPolicies   map[string]*PickPolicy
	ledger         *ClaimLedger
}

// Ledger returns the in-memory claim ledger.
func (s *Store) Ledger() *ClaimLedger { return s.ledger }

// NewForTest constructs a Store with no pgxpool and only the in-memory
// ledger. Tests use this when they want to exercise the observability
// surface without spinning up postgres.
func NewForTest() *Store {
	return &Store{ledger: NewClaimLedger(1024)}
}

// PickPolicy is one configured pick policy. Store-internal.
//
// Queue-vs-ring behavior is emergent from on_commit_default /
// on_give_up_default (delete = drain, release_to_back = recycle), not
// switched on a discriminator field.
type PickPolicy struct {
	ItemsTable        string
	OnCommitDefault   string
	OnGiveUpDefault   string
	VisibilityTimeout time.Duration
}

// Config is the store's config schema (operator-managed).
type Config struct {
	Connection     string
	WriteSemantics corestore.WriteSemantics
	PickPolicies   map[string]*PickPolicy
}

// New constructs a Store from the supplied config. Opens a pgxpool
// against the connection string; verifies each pick-policy items table
// at construction.
func New(ctx context.Context, cfg Config) (*Store, error) {
	if cfg.Connection == "" {
		return nil, errors.New("postgres store: connection is required")
	}
	pool, err := pgxpool.New(ctx, cfg.Connection)
	if err != nil {
		return nil, fmt.Errorf("postgres store: open pool: %w", err)
	}
	if cfg.WriteSemantics == "" {
		cfg.WriteSemantics = corestore.WriteSemanticsStagedAsync
	}
	for selector, pp := range cfg.PickPolicies {
		if !validIdent(pp.ItemsTable) {
			pool.Close()
			return nil, fmt.Errorf("postgres store: pick_policies[%q]: items_table %q is not a valid identifier (lowercase letters/digits/underscore; not starting with a digit)",
				selector, pp.ItemsTable)
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

// Close releases the pool. Idempotent.
func (s *Store) Close() {
	if s.pool != nil {
		s.pool.Close()
	}
}

// Pool exposes the store's pool for the admin server's items
// insertion.
//
// This accessor is intended ONLY for the store's own admin
// endpoint (`/admin/stores/{name}/pick-policies/{selector}/items`,
// served by the postgres store-service in-process). External callers
// MUST NOT use it: rimsky's control-plane processes (scheduler,
// supervisor, control-api) talk to this store via the gRPC
// StoreService bridge — reaching for the store pool from outside
// the store-service binary indicates a wiring error (e.g. a test or
// scenario harness trying to short-circuit the wire). Treat any new
// caller as a code-review red flag.
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

// Capabilities reports the store's advertised capability struct.
//
// The postgres store declares a singleton envelope containing its
// configured write_semantics. Items-table queue semantics are typically
// staged_async (writes go to a working table, commit publishes the
// effect, abandon discards it without blocking concurrent reads). A
// scoped postgres store running in sync mode declares
// [sync] instead — operator's call.
func (s *Store) Capabilities() corestore.Capabilities {
	return corestore.Capabilities{
		WriteSemanticsEnvelope: []corestore.WriteSemantics{s.writeSemantics},
	}
}

// PickPolicies returns a snapshot of every configured policy. Used by
// the store's own sweep.
func (s *Store) PickPolicies() map[string]PickPolicy {
	out := make(map[string]PickPolicy, len(s.pickPolicies))
	for sel, pp := range s.pickPolicies {
		out[sel] = *pp
	}
	return out
}

// Open performs the store's claim acquisition. For pick-policy
// selectors, runs FOR UPDATE SKIP LOCKED on the items table. For
// scoped selectors (no pick policy match), echoes the selector as
// address + scope.
//
// Runs inside the store's own pgx tx; rimsky's tx is decoupled.
// Returns OpenOutcome{Available: false} when a configured pick policy
// finds no available item; stores that always have a claim to
// give wrap their result in OpenOutcome{Available: true, Result: ...}.
func (s *Store) Open(ctx context.Context, claimID, selector string) (corestore.OpenOutcome, error) {
	if pp, ok := s.pickPolicies[selector]; ok {
		out, err := s.openPickPolicy(ctx, claimID, pp)
		if err == nil && out.Available {
			s.ledger.RecordOpen(claimID, selector, out.Result.Address, out.Result.Scope)
		}
		return out, err
	}
	addr, err := json.Marshal(selector)
	if err != nil {
		return corestore.OpenOutcome{}, fmt.Errorf("postgres store: marshal selector: %w", err)
	}
	s.ledger.RecordOpen(claimID, selector, addr, addr)
	return corestore.OpenOutcome{
		Available: true,
		Result: corestore.ClaimResult{
			Address:                json.RawMessage(addr),
			Scope:                  json.RawMessage(addr),
			RealizedWriteSemantics: s.writeSemantics,
		},
	}, nil
}

func (s *Store) openPickPolicy(ctx context.Context, claimID string, pp *PickPolicy) (corestore.OpenOutcome, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return corestore.OpenOutcome{}, fmt.Errorf("postgres store: begin tx: %w", err)
	}
	// Deferred Rollback tolerates "tx already closed" — Commit returns
	// before the defer runs on the success path.
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
			// Items table empty; signal to rimsky as Unavailable.
			return corestore.OpenOutcome{Available: false}, nil
		}
		return corestore.OpenOutcome{}, fmt.Errorf("postgres store: pick: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return corestore.OpenOutcome{}, fmt.Errorf("postgres store: commit pick tx: %w", err)
	}

	addrBytes, _ := json.Marshal(itemID)
	scopeBytes, _ := json.Marshal(itemID)

	return corestore.OpenOutcome{
		Available: true,
		Result: corestore.ClaimResult{
			Address:                json.RawMessage(addrBytes),
			Payload:                rawJSON,
			Scope:                  json.RawMessage(scopeBytes),
			RealizedWriteSemantics: s.writeSemantics,
		},
	}, nil
}

// Commit applies the configured on_commit_default action for pick-
// policy claims; no-op for regional claims. address is accepted for
// signature uniformity across the three standard stores and
// ignored — postgres looks up the in-flight pick-policy item by
// claim_token (= rimsky claim_id) so that a duplicated terminal RPC
// under a different claim_id is a no-op rather than a double-bump
// (spec §7.8 obligation #3 — terminal verbs idempotent in claim_id).
//
// The ledger records the terminal event only after the store-side
// action succeeds — recording on failure would mislead the dashboard
// into showing a state the store never actually entered. Failures
// surface as a non-terminal `claim_commit_failed` event, leaving the
// claim's recorded state OPEN.
func (s *Store) Commit(ctx context.Context, claimID string, _ []byte, _ []byte) error {
	if err := s.applyPickAction(ctx, claimID, true); err != nil {
		s.ledger.RecordEvent(claimID, "claim_commit_failed", "ERROR", map[string]any{"error": err.Error()})
		return err
	}
	s.ledger.RecordTerminal(claimID, "claim_committed", nil)
	return nil
}

// Abandon applies the configured on_give_up_default action for pick-
// policy claims; degenerate no-op for regional claims (cannot undo
// direct writes). address is accepted for signature uniformity and
// ignored. Lookup is claim_token-based (= rimsky claim_id) per §7.8
// obligation #3. Ledger records terminal only on success; failures
// surface as a non-terminal `claim_abandon_failed` event.
func (s *Store) Abandon(ctx context.Context, claimID string, _ []byte, _ []byte) error {
	if err := s.applyPickAction(ctx, claimID, false); err != nil {
		s.ledger.RecordEvent(claimID, "claim_abandon_failed", "ERROR", map[string]any{"error": err.Error()})
		return err
	}
	s.ledger.RecordTerminal(claimID, "claim_abandoned", nil)
	return nil
}

// Release tears down store-side read state. v3 standard postgres
// registers no read state at Open; always a no-op. region/address are
// accepted for signature uniformity and ignored.
func (s *Store) Release(_ context.Context, claimID string, _ []byte, _ []byte) error {
	s.ledger.RecordTerminal(claimID, "claim_released", nil)
	return nil
}

// applyPickAction looks up the in-flight item by claim_token (=
// rimsky claim_id) and applies the action. claim_token lookup makes
// the terminal verbs idempotent in claim_id (spec §7.8 obligation #3):
// a duplicated terminal RPC after the row has been re-claimed by a
// different supervisor is a no-op (claim_token mismatch — zero rows
// affected) rather than a double-bump that corrupts the new claim's
// state. claim_token also disambiguates multi-policy configurations
// (rimsky-supplied claim_id is unique across all policies) without
// needing per-row policy_selector bookkeeping.
//
// successPath=true uses on_commit_default; false uses
// on_give_up_default. Store-side defaults are the only governing
// input; per the 2026-04-30 cleanup amending v3 §4.5, no rimsky-side
// override is plumbed across the wire. Runs in its own store-side tx.
func (s *Store) applyPickAction(ctx context.Context, claimID string, successPath bool) error {
	if claimID == "" {
		return nil
	}
	pp, found := s.findPolicyForClaim(ctx, claimID)
	if !found {
		// Either the claim was already terminated (claim_token cleared)
		// or it never belonged to any pick-policy items table (regional
		// claim). Both are no-ops at this layer.
		return nil
	}
	var action string
	if successPath {
		action = pp.OnCommitDefault
	} else {
		action = pp.OnGiveUpDefault
	}
	if !validPickAction(action) {
		return fmt.Errorf("postgres store: applyPickAction: invalid action %q", action)
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
	// Every action filters on claim_token = $1 so a duplicated terminal
	// RPC with a stale claim_id (the row was re-claimed by a different
	// supervisor in between) affects zero rows — preserving the live
	// claim's state.
	switch action {
	case "delete":
		if _, err := tx.Exec(ctx,
			fmt.Sprintf(`DELETE FROM %s WHERE claim_token = $1`, pp.ItemsTable), claimID,
		); err != nil {
			return fmt.Errorf("postgres store: delete item: %w", err)
		}
	case "release_to_back":
		if _, err := tx.Exec(ctx,
			fmt.Sprintf(`UPDATE %s
			    SET state = 'available', claim_token = NULL, claimed_at = NULL,
			        sequence = nextval(pg_get_serial_sequence($1, 'sequence'))
			  WHERE claim_token = $2`, pp.ItemsTable),
			pp.ItemsTable, claimID,
		); err != nil {
			return fmt.Errorf("postgres store: release_to_back: %w", err)
		}
	case "release_to_head":
		if _, err := tx.Exec(ctx,
			fmt.Sprintf(`UPDATE %s
			    SET state = 'available', claim_token = NULL, claimed_at = NULL,
			        priority = priority + 1
			  WHERE claim_token = $1`, pp.ItemsTable),
			claimID,
		); err != nil {
			return fmt.Errorf("postgres store: release_to_head: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres store: commit action tx: %w", err)
	}
	committed = true
	return nil
}

// findPolicyForClaim scans configured pick policies and returns the
// first one whose items_table contains an in-flight row whose
// claim_token matches claimID. Lookup by claim_token (rimsky-supplied,
// unique per claim) sidesteps the multi-policy ambiguity that an
// item_id-based lookup would have when two policies share an id space.
func (s *Store) findPolicyForClaim(ctx context.Context, claimID string) (*PickPolicy, bool) {
	for _, pp := range s.pickPolicies {
		var exists bool
		query := fmt.Sprintf(`SELECT EXISTS (SELECT 1 FROM %s WHERE claim_token = $1)`, pp.ItemsTable)
		if err := s.pool.QueryRow(ctx, query, claimID).Scan(&exists); err == nil && exists {
			return pp, true
		}
	}
	return nil, false
}

// InsertItems bulk-inserts payloads into one configured pick-policy's
// items table. Used by the store's own admin endpoint (per spec
// §13). Each row gets a fresh item_id and state='available'.
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

func validPickAction(s string) bool {
	return s == "delete" || s == "release_to_back" || s == "release_to_head"
}

// validIdent accepts a value that satisfies ItemsTableIdentRegex —
// lowercase letters / digits / underscore, not starting with a digit.
// All three layers (cmd/main.go, server/observability.go, here) share
// the same regex so an items_table that passes one passes all three.
func validIdent(s string) bool {
	return ItemsTableIdentRegex.MatchString(s)
}

// expectedColumns lists the store's items-table column requirements.
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
