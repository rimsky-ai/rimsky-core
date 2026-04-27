// Package postgres implements the postgres-backed store described in
// spec §11.1. Renamed from claimstorepg per spec §J1: pick policies are
// substrate-side; one uniform protocol for regional and pick-policy
// access. The store recognizes special-form selectors as triggers for
// configured pick policies (recommended convention: "@policy-name").
//
// Implements the five-verb store.Store interface (spec §11.5):
// Open / Commit / Abandon / Delete / Release.
//
// Imports: core/store/, pgx/v5, pgxpool, stdlib.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/fallguy/rimsky/core/store"
)

// Store is the postgres-backed store. State held by Store itself is
// immutable after construction (name + pool reference + write_semantics
// + pick_policies map). Lock state lives in rimsky_lock_holders;
// per-pick-policy item availability lives in operator-owned items
// tables; this struct caches neither.
//
// Thread-safety: all methods are safe for concurrent use. Writes use
// the caller-supplied tx (via store.TxFromContext) when one is
// attached; the pool fallback is reserved for read-only paths that do
// not run inside the supervisor's atomic acquisition transaction.
type Store struct {
	name           string
	pool           *pgxpool.Pool
	writeSemantics store.WriteSemantics
	// pickPolicies is keyed by recognized selector form (e.g. "@queue").
	// Open looks up the configured policy by selector; for non-policy
	// selectors the store treats the selector as opaque region text.
	pickPolicies map[string]*pickPolicy
}

// pickPolicy holds one named pick policy's configuration. The schema
// inside is substrate-defined; the supervisor passes through opaque.
type pickPolicy struct {
	policyType        string // "queue" | "ring" | etc. (substrate-defined)
	itemsTable        string
	onCommitDefault   string // "delete" | "release_to_back" | "release_to_head"
	onGiveUpDefault   string
	visibilityTimeout time.Duration
}

// Compile-time interface check.
var _ store.Store = (*Store)(nil)

// Name returns the operator-configured store name.
func (s *Store) Name() string { return s.name }

// Kind returns the canonical store kind, "postgres". Renamed from the
// prior "claim_store" kind name.
func (s *Store) Kind() string { return "postgres" }

// Capabilities reports the store's write_semantics. v1 supports only
// "direct" — staged_blocking / staged_async are protocol-supported but
// no v1 implementation exercises them.
func (s *Store) Capabilities() store.Capabilities {
	return store.Capabilities{WriteSemantics: s.writeSemantics}
}

// PickPolicySnapshot exposes per-policy config to callers (the
// scheduler's visibility-timeout sweep, control-api admin endpoints).
// Read-only; does not aliase store-internal state.
type PickPolicySnapshot struct {
	Type              string
	ItemsTable        string
	OnCommitDefault   string
	OnGiveUpDefault   string
	VisibilityTimeout time.Duration
}

// PickPolicyConfig returns a snapshot of one configured pick policy by
// selector key, plus a presence bool. Test/operator-tooling helper.
func (s *Store) PickPolicyConfig(selector string) (PickPolicySnapshot, bool) {
	p, ok := s.pickPolicies[selector]
	if !ok {
		return PickPolicySnapshot{}, false
	}
	return PickPolicySnapshot{
		Type:              p.policyType,
		ItemsTable:        p.itemsTable,
		OnCommitDefault:   p.onCommitDefault,
		OnGiveUpDefault:   p.onGiveUpDefault,
		VisibilityTimeout: p.visibilityTimeout,
	}, true
}

// PickPolicies returns a snapshot of every configured pick policy keyed
// by selector. Used by the scheduler's visibility-timeout sweep.
func (s *Store) PickPolicies() map[string]PickPolicySnapshot {
	out := make(map[string]PickPolicySnapshot, len(s.pickPolicies))
	for sel, p := range s.pickPolicies {
		out[sel] = PickPolicySnapshot{
			Type:              p.policyType,
			ItemsTable:        p.itemsTable,
			OnCommitDefault:   p.onCommitDefault,
			OnGiveUpDefault:   p.onGiveUpDefault,
			VisibilityTimeout: p.visibilityTimeout,
		}
	}
	return out
}

// Pool returns the underlying connection pool. Used by the scheduler's
// visibility-timeout sweep; not exposed to executors.
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

// Close releases the underlying *pgxpool.Pool. Every postgres store
// owns its own pool (opened by the factory from the required
// `connection:` config), so Close is unconditional. Safe to call more
// than once: pgxpool.Pool.Close handles idempotency.
//
// Wired into Registry.Close so cmd binaries can release per-store pools
// at shutdown without tracking ownership themselves.
func (s *Store) Close() {
	if s.pool == nil {
		return
	}
	s.pool.Close()
}

// RegionsConflict reports whether two regions overlap. The grammar is
// substrate-specific; v1 postgres treats two regions as conflicting iff
// they decode to identical canonical bytes.
//
// @blessed-invariant 14: pure; no I/O; deterministic on inputs.
func (s *Store) RegionsConflict(a, b []byte) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	ca, errA := canonicalJSON(a)
	cb, errB := canonicalJSON(b)
	if errA != nil || errB != nil {
		return true
	}
	return string(ca) == string(cb)
}

// UnmarshalRegion canonicalises region bytes (re-marshals through JSON)
// for use with RegionsConflict.
//
// @blessed-invariant 14: pure.
func (s *Store) UnmarshalRegion(raw []byte) ([]byte, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	c, err := canonicalJSON(raw)
	if err != nil {
		return nil, fmt.Errorf("postgres store %q: unmarshal region: %w", s.name, err)
	}
	return c, nil
}

// Open produces a substrate-native address. For pick-policy selectors,
// performs FOR UPDATE SKIP LOCKED on the configured items_table to pick
// an item, flips state to 'in_progress', and returns the picked item's
// id as Region and Address with the row's payload.
//
// For non-policy selectors (regional access), echoes the selector as
// both Address and Region — direct mode for v1; no staging area.
//
// Uses store.TxFromContext to participate in the supervisor's
// acquisition tx (invariant 10/15).
func (s *Store) Open(ctx context.Context, spec store.ClaimSpec) (store.ClaimResult, error) {
	pp, isPicker := s.pickPolicies[spec.Selector]
	if isPicker {
		return s.openPickPolicy(ctx, spec, pp)
	}
	addr, err := json.Marshal(spec.Selector)
	if err != nil {
		return store.ClaimResult{}, fmt.Errorf("postgres store %q: Open: marshal selector: %w", s.name, err)
	}
	return store.ClaimResult{
		Address: json.RawMessage(addr),
		Region:  json.RawMessage(addr),
	}, nil
}

// openPickPolicy runs the FOR UPDATE SKIP LOCKED pick + items-table
// flip inside the supervisor's transaction. Returns a zero ClaimResult
// (no error) when the queue is empty — the supervisor treats that as
// "ineligible at the last moment" and rolls back the outer tx.
func (s *Store) openPickPolicy(ctx context.Context, _ store.ClaimSpec, pp *pickPolicy) (store.ClaimResult, error) {
	tx, ok := store.TxFromContext(ctx)
	if !ok {
		return store.ClaimResult{}, fmt.Errorf("postgres store %q: Open(pick): requires open pgx.Tx via store.WithTx", s.name)
	}
	token, err := uuid.NewRandom()
	if err != nil {
		return store.ClaimResult{}, fmt.Errorf("postgres store %q: generate claim_token: %w", s.name, err)
	}
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
		pp.itemsTable, pp.itemsTable,
	)
	row := tx.QueryRow(ctx, q, token.String())
	var (
		itemID  string
		rawJSON []byte
	)
	if err := row.Scan(&itemID, &rawJSON); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return store.ClaimResult{}, nil
		}
		return store.ClaimResult{}, fmt.Errorf("postgres store %q: Open pick: %w", s.name, err)
	}
	addrBytes, _ := json.Marshal(itemID)
	regionBytes, _ := json.Marshal(itemID)
	return store.ClaimResult{
		Address: json.RawMessage(addrBytes),
		Payload: rawJSON,
		Region:  json.RawMessage(regionBytes),
	}, nil
}

// Commit applies the on_commit policy to a pick-policy claim, or is a
// substrate no-op for regional direct-mode claims. policyOverride
// (non-empty) takes precedence over the policy's on_commit_default.
func (s *Store) Commit(ctx context.Context, region []byte, _ []byte, policyOverride string) error {
	return s.applyPickAction(ctx, region, policyOverride, true)
}

// Abandon applies the on_give_up policy to a pick-policy claim, or is
// a degenerate no-op for regional direct-mode claims (cannot undo
// direct writes per spec §6.2). policyOverride (non-empty) takes
// precedence over the policy's on_give_up_default.
func (s *Store) Abandon(ctx context.Context, region []byte, _ []byte, policyOverride string) error {
	return s.applyPickAction(ctx, region, policyOverride, false)
}

// Delete removes the live region. Regional only — pick-policy claims
// express deletion via Commit + policyOverride="delete". For regional
// claims, the postgres store has no v1 "delete a region" operation
// (regions in v1 postgres are user-defined; the substrate doesn't own
// the data layout); returns nil to allow the supervisor to proceed.
func (s *Store) Delete(_ context.Context, _ []byte) error {
	return nil
}

// Release tears down substrate-side read state. v1 postgres registers
// no read-side state at Open (direct mode); always a no-op.
func (s *Store) Release(_ context.Context, _ []byte, _ []byte) error {
	return nil
}

// applyPickAction looks up the in-flight item by item_id (decoded from
// region bytes) and applies the action. successPath=true uses
// on_commit_default; false uses on_give_up_default. Empty policyOverride
// falls through to the configured default.
func (s *Store) applyPickAction(ctx context.Context, region []byte, policyOverride string, successPath bool) error {
	itemID, ok := decodeItemID(region)
	if !ok {
		return nil
	}
	pp, found := s.findPolicyForItem(ctx, itemID)
	if !found {
		return nil
	}
	tx, ok := store.TxFromContext(ctx)
	if !ok {
		return fmt.Errorf("postgres store %q: applyPickAction: requires open pgx.Tx via store.WithTx", s.name)
	}
	action := policyOverride
	if action == "" {
		if successPath {
			action = pp.onCommitDefault
		} else {
			action = pp.onGiveUpDefault
		}
	}
	if !validPickAction(action) {
		return fmt.Errorf("postgres store %q: applyPickAction: invalid action %q", s.name, action)
	}
	switch action {
	case "delete":
		_, err := tx.Exec(ctx, fmt.Sprintf(`DELETE FROM %s WHERE item_id = $1`, pp.itemsTable), itemID)
		if err != nil {
			return fmt.Errorf("postgres store %q: delete item: %w", s.name, err)
		}
	case "release_to_back":
		_, err := tx.Exec(ctx,
			fmt.Sprintf(`UPDATE %s
			    SET state = 'available', claim_token = NULL, claimed_at = NULL,
			        sequence = nextval(pg_get_serial_sequence($1, 'sequence'))
			  WHERE item_id = $2`, pp.itemsTable),
			pp.itemsTable, itemID,
		)
		if err != nil {
			return fmt.Errorf("postgres store %q: release_to_back: %w", s.name, err)
		}
	case "release_to_head":
		_, err := tx.Exec(ctx,
			fmt.Sprintf(`UPDATE %s
			    SET state = 'available', claim_token = NULL, claimed_at = NULL,
			        priority = priority + 1
			  WHERE item_id = $1`, pp.itemsTable),
			itemID,
		)
		if err != nil {
			return fmt.Errorf("postgres store %q: release_to_head: %w", s.name, err)
		}
	}
	return nil
}

// findPolicyForItem scans configured pick policies and returns the
// first one whose items_table contains a row with the given item_id.
// Reads via the caller's tx if present, else via the pool.
func (s *Store) findPolicyForItem(ctx context.Context, itemID string) (*pickPolicy, bool) {
	for _, pp := range s.pickPolicies {
		var exists bool
		query := fmt.Sprintf(`SELECT EXISTS (SELECT 1 FROM %s WHERE item_id = $1)`, pp.itemsTable)
		var err error
		if tx, ok := store.TxFromContext(ctx); ok {
			err = tx.QueryRow(ctx, query, itemID).Scan(&exists)
		} else {
			err = s.pool.QueryRow(ctx, query, itemID).Scan(&exists)
		}
		if err == nil && exists {
			return pp, true
		}
	}
	return nil, false
}

// validPickAction reports whether s is one of the three known pick-
// policy actions.
func validPickAction(s string) bool {
	return s == "delete" || s == "release_to_back" || s == "release_to_head"
}

// decodeItemID extracts a string item id from region bytes if they
// encode a JSON string. Returns ("", false) on any other shape.
func decodeItemID(region []byte) (string, bool) {
	if len(region) == 0 {
		return "", false
	}
	var s string
	if err := json.Unmarshal(region, &s); err != nil {
		return "", false
	}
	if s == "" {
		return "", false
	}
	return s, true
}

// canonicalJSON re-marshals input bytes through Go's encoder so the
// output is byte-stable regardless of whitespace in the input.
func canonicalJSON(b []byte) ([]byte, error) {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, err
	}
	return json.Marshal(v)
}

// validIdent reports whether s is a safe SQL identifier we can
// interpolate into a query. Used by the factory for items_table names.
func validIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
			if i == 0 {
				return false
			}
		case r == '_':
		default:
			return false
		}
	}
	return true
}
