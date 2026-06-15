// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Package store is the store-internal logic for the standard
// postgres store-service. Per spec §8.2: scope-bytes access via byte-equal
// scope match (selector echoed); pick-policy access via FOR UPDATE SKIP
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
// scope-bytes claims have nothing to track. An earlier draft kept a
// claim_id → item_id map but no consumer ever read it; removed to
// eliminate drift.
type Store struct {
	pool           *pgxpool.Pool
	writeSemantics claimproducer.WriteSemantics
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
// Queue-vs-ring behavior is emergent from OnCommit / OnGiveUp action
// choice (pop = drain, recycle = ring). The pg-store supports only
// `pop` and `recycle`; `pop_and_move` and `pop_and_delete` are
// rejected at config-load (no separate folder concept).
type PickPolicy struct {
	ItemsTable        string
	OnCommit          action.Action
	OnGiveUp          action.Action
	VisibilityTimeout time.Duration
}

// Config is the store's config schema (operator-managed).
type Config struct {
	Connection     string
	WriteSemantics claimproducer.WriteSemantics
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
func (s *Store) Capabilities() claimproducer.Capabilities {
	return claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{s.writeSemantics},
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
func (s *Store) Open(ctx context.Context, claimID, selector string) (claimproducer.OpenOutcome, error) {
	if pp, ok := s.pickPolicies[selector]; ok {
		out, err := s.openPickPolicy(ctx, claimID, pp)
		if err == nil && out.Available {
			s.ledger.RecordOpen(claimID, selector, out.Result.Address, out.Result.ClaimScope)
		}
		return out, err
	}
	// @deliberate: staged_async scope-bytes claim reserves a per-claim
	// staging schema (atomic-staging substrate). Address names the
	// staging schema the executor writes into; ClaimScope stays the
	// canonical selector so byte-equality / conflict detection is
	// unchanged. See staging.go.
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
	// @constraint: deferred Rollback tolerates "tx already closed" —
	// Commit returns before the defer runs on the success path.
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
			// @deliberate: items table empty (or every row in-flight).
			// Signal to rimsky as Unavailable, but name the producer-
			// declared class so rimsky's acquisition-failure routing keys
			// the operator's `error_types:` chain on
			// `pg/claim_unavailable` rather than only the synthetic
			// `acquire/unavailable`. The Available=false wire shape is
			// unchanged; the class is an out-of-band routing hint.
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

// Commit applies the configured OnCommit action for pick-
// policy claims; no-op for scope-bytes claims. address is accepted for
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
func (s *Store) Commit(ctx context.Context, claimID string, claimScope []byte, address []byte) error {
	swap, canonical, staging, err := s.stagedSwapTarget(claimScope, address)
	if err != nil {
		s.ledger.RecordEvent(claimID, "claim_commit_failed", "ERROR", map[string]any{"error": err.Error()})
		return err
	}
	if swap {
		// @deliberate: atomic-staging cutover swaps the staging schema
		// into the canonical view in one tx. A collision (populated /
		// depended-upon canonical) surfaces a pg/swap_failed-classed
		// error and leaves the staging intact, so the claim's recorded
		// state stays OPEN.
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

// Abandon applies the configured OnGiveUp action for pick-
// policy claims; degenerate no-op for scope-bytes claims (cannot undo
// direct writes). address is accepted for signature uniformity and
// ignored. Lookup is claim_token-based (= rimsky claim_id) per §7.8
// obligation #3. Ledger records terminal only on success; failures
// surface as a non-terminal `claim_abandon_failed` event.
func (s *Store) Abandon(ctx context.Context, claimID string, claimScope []byte, address []byte) error {
	swap, _, staging, err := s.stagedSwapTarget(claimScope, address)
	if err != nil {
		s.ledger.RecordEvent(claimID, "claim_abandon_failed", "ERROR", map[string]any{"error": err.Error()})
		return err
	}
	if swap {
		// @deliberate: atomic-staging discard drops the staging schema,
		// leaving the canonical untouched. Ledger records terminal only
		// on success.
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

// Release tears down store-side read state. For pick-policy and sync
// scope-bytes claims the v3 standard postgres registers no read state at
// Open, so Release is a no-op. For a staged_async scope-bytes claim it
// drops any RESIDUAL staging schema — a claim that was Open'd but never
// reached Commit/Abandon (e.g. an interrupted run) would otherwise leak a
// reserved schema. DROP ... IF EXISTS makes this safe to call after a
// Commit/Abandon that already consumed/dropped the staging.
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

// stagedSwapTarget decides whether a terminal verb should take the
// atomic-staging branch and, if so, returns the canonical and staging
// schema names decoded from the claim's ClaimScope (canonical selector)
// and Address (per-claim staging schema). The branch fires only for a
// staged_async store on a scope-bytes claim whose Address differs from
// its ClaimScope — i.e. a claim for which Open actually reserved a
// distinct staging schema. Pick-policy claims and sync/read_only
// scope-bytes claims (Address == ClaimScope) take the unchanged
// pick-action / no-op path.
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
	// @deliberate: a staged scope-bytes claim reserved a staging schema
	// distinct from the canonical selector. Equal (or either empty) means
	// no staging was reserved — a pick-policy or sync claim — so do not
	// swap.
	if canonical == "" || staging == "" || canonical == staging {
		return false, "", "", nil
	}
	// @constraint: canonical must not itself be a pick-policy selector;
	// pick-policy terminal handling owns those.
	if _, ok := s.pickPolicies[canonical]; ok {
		return false, "", "", nil
	}
	// @constraint: mirror Open's gate — only a schema-shaped canonical
	// is a swap target. A non-schema (path-shaped) ClaimScope is an
	// opaque scope-bytes claim Open never reserved a staging schema for,
	// so its terminal is a no-op — never a swap.
	if !schemaIdentRegex.MatchString(canonical) {
		return false, "", "", nil
	}
	return true, canonical, staging, nil
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
// successPath=true uses OnCommit; false uses OnGiveUp. Store-side
// defaults are the only governing input; per the 2026-04-30 cleanup
// amending v3 §4.5, no rimsky-side override is plumbed across the
// wire. Runs in its own store-side tx.
//
// Action support (per spec §3.2): pop and recycle only. pop_and_move
// and pop_and_delete are rejected at config-load; the defensive
// branch below handles any value that slips past validator.
func (s *Store) applyPickAction(ctx context.Context, claimID string, successPath bool) error {
	if claimID == "" {
		return nil
	}
	pp, found, err := s.findPolicyForClaim(ctx, claimID)
	if err != nil {
		return fmt.Errorf("postgres store: locate policy for claim: %w", err)
	}
	if !found {
		// @deliberate: either the claim was already terminated
		// (claim_token cleared) or it never belonged to any pick-policy
		// items table (scope-bytes claim). Both are no-ops at this layer.
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
	// @constraint: every action filters on claim_token = $1 so a
	// duplicated terminal RPC with a stale claim_id (the row was
	// re-claimed by a different supervisor in between) affects zero
	// rows — preserving the live claim's state.
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

// findPolicyForClaim scans configured pick policies and returns the
// first one whose items_table contains an in-flight row whose
// claim_token matches claimID. Lookup by claim_token (rimsky-supplied,
// unique per claim) sidesteps the multi-policy ambiguity that an
// item_id-based lookup would have when two policies share an id space.
//
// Returns (nil, false, nil) when no policy matches (a real "not
// found"). Returns (nil, false, err) when a SQL error occurs — the
// caller MUST surface this to the supervisor so a transient pool
// hiccup does not silently degrade Commit/Abandon to a no-op while
// reporting success to the ledger (issue 8: pre-existing bug).
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

func validPickAction(k action.Kind) bool {
	return k == action.Pop || k == action.Recycle
}

// validIdent accepts a value that satisfies ItemsTableIdentRegex —
// lowercase letters / digits / underscore, not starting with a digit.
// All three layers (cmd/main.go, server/observability.go, here) share
// the same regex so an items_table that passes one passes all three.
func validIdent(s string) bool {
	return ItemsTableIdentRegex.MatchString(s)
}

// validatePickPolicy validates one operator-supplied pick policy and
// returns a ValidationResult. Per spec §6.1, §6.1b: returns the full
// set of errors (so operators see every problem in one pass). Pre-v1
// break-cleanly: old field names and old action values are rejected.
//
// pg-store-specific rules: `pop_and_move` and `pop_and_delete` are
// rejected as not supported by the postgres store (no separate folder
// concept; pg's row IS the resource).
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

	// @deliberate: emit only ONE error per slot for an unsupported
	// pg-store action. The pg-rejection check runs first; if it fires
	// (e.g. an operator wrote `on_commit: pop_and_move`), we skip the
	// per-action `Validate()` call so the operator doesn't see two
	// errors stacked up for the same root-cause mistake (the missing-
	// target one + the not-supported-by-pg one). A zero Kind (yaml.v3
	// silently dropping null on a struct value, or an operator missing
	// the field entirely) produces the hard-to-decode `unknown action
	// ""` from Validate(); surface a clearer message ahead of
	// Validate(). Zero-Kind is also not a PopAndMove/PopAndDelete so
	// pgRejectAction does not fire for it.
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

// pgZeroOrRejected handles the two cases where a per-slot action
// should NOT also be passed to Action.Validate():
//
//   - Zero Kind (yaml.v3 dropped null on the struct value, or the
//     field was absent): emit a clear "required" error instead of the
//     opaque `unknown action ""` produced by Validate().
//   - Pg-store-rejected (PopAndMove / PopAndDelete): emit the §6.1b
//     not-supported message; suppress Validate() so a second
//     "missing target" error doesn't stack up for the same root cause.
//
// Returns true if the slot was handled here (so the caller skips
// Action.Validate() for this slot).
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
