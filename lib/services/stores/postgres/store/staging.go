// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// staging.go implements the postgres store's atomic-staging substrate
// for staged-write (staged_async) scope-bytes claims, per
// concept:atomic-staging ("Postgres schema swap: atomic via
// transaction") and spec story S-pgstore-atomic-staging-substrate.
//
// Lifecycle for a staged scope-bytes claim:
//
//   - Open reserves a PER-CLAIM staging schema (CREATE SCHEMA) and hands
//     its identity back as the claim Address; the executor writes its
//     produced rows into that schema over the data path. ClaimScope
//     stays the canonical selector bytes so claim-scope byte-equality /
//     conflict detection is unaffected (two claims on the same selector
//     conflict on the same ClaimScope while writing into DISTINCT staging
//     schemas).
//   - Commit performs the atomic swap inside a single store-side tx:
//     drop the canonical schema, then rename staging into the canonical
//     name. The transaction makes the cutover all-or-nothing.
//   - Abandon drops the staging schema (DROP SCHEMA ... CASCADE),
//     discarding the staged work and leaving the canonical untouched.
//   - Release drops any residual staging schema for a claim that was
//     opened but never reached Commit/Abandon, so an interrupted claim
//     does not leak a reserved schema.
//
// @concept: atomic-staging

package store

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
)

// SwapFailedClass is the error class the store names when an atomic
// schema swap cannot complete (a populated/depended-upon canonical
// blocks the cutover). It is one of the executor's declaredErrorClasses
// so an operator's `error_types:` config can range-check and route it;
// the swap-collision path producing this class at the store boundary is
// what gives the declared class a real emit site (it was zero-emit-site
// before atomic staging shipped). Kept as an exported constant so the
// server/executor role and any test can reference the exact string.
const SwapFailedClass = "pg/swap_failed"

// ClaimUnavailableClass is the error class the store names when a
// pick-policy Open finds no claimable item (the items table is empty /
// every row is in-flight). It is one of the executor's
// declaredErrorClasses so an operator's `error_types:` config can
// range-check and route it. Carried on OpenOutcome.UnavailableClass so
// rimsky's acquisition-failure routing keys the operator's chain on this
// producer-declared leaf rather than only the synthetic
// "acquire/unavailable". Naming the class here (rather than letting the
// bare Unavailable surface anonymously) is what gives the declared class
// a real signal at the subscriber surface — it was zero-emit-site before.
const ClaimUnavailableClass = "pg/claim_unavailable"

// ClassedError carries a rimsky error_class alongside a store-side
// failure so the gRPC server boundary can translate it into a
// google.rpc.ErrorInfo detail (Reason = Class) WITHOUT string-matching
// the message. rimsky's claim-producer client recovers the class via the
// ErrorInfo and routes it through the operator's `error_types:` chain.
// The Error() string still names the class so the existing store-package
// tests (and any human reading a log) see it inline.
type ClassedError struct {
	Class string
	Err   error
}

func (e *ClassedError) Error() string {
	if e.Err == nil {
		return e.Class
	}
	return fmt.Sprintf("%s: %v", e.Class, e.Err)
}

func (e *ClassedError) Unwrap() error { return e.Err }

// stagingSchemaPrefix namespaces every reserved staging schema so they
// are visibly distinct from operator-authored canonical schemas and
// trivially sweepable.
const stagingSchemaPrefix = "rimsky_stg_"

// schemaIdentRegex is the strict identifier shape a schema name must
// satisfy before it is interpolated into DDL. Postgres folds unquoted
// identifiers to lowercase, so the canonical selector — operator-
// supplied via the claim selector — is held to the same lowercase-only
// shape the items-table identifier already enforces, rather than
// trusting quoting alone. The staging name we generate is constructed to
// satisfy this by construction.
var schemaIdentRegex = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

// stagedScopeBytes reports whether Open for this store should reserve an
// atomic-staging schema for the given selector. The atomic-staging
// lifecycle engages ONLY when ALL of:
//
//   - the store realizes staged_async write-semantics,
//   - the selector is not a configured pick-policy selector, AND
//   - the selector is a valid Postgres SCHEMA identifier
//     (schemaIdentRegex) — i.e. it actually names a schema the swap can
//     target.
//
// A path-shaped or otherwise non-schema selector (e.g. an opaque
// scope-bytes conflict key like `tenant/a/x`) is NOT a schema-swap
// target: those claims keep the verbatim selector-echo at Open and the
// no-op Commit/Abandon, exactly as before. Gating on the selector shape
// (rather than rejecting it) is what lets a staged_async store still
// serve opaque scope-bytes claims — the conformance suite and any
// non-schema producer rely on this.
func (s *Store) stagedScopeBytes(selector string) bool {
	if _, ok := s.pickPolicies[selector]; ok {
		return false
	}
	if s.writeSemantics != "staged_async" {
		return false
	}
	return schemaIdentRegex.MatchString(selector)
}

// stagingSchemaName derives a deterministic, DDL-safe staging schema
// name for a claim. The claim_id is a UUID (hyphens, lowercase hex);
// hyphens are mapped to underscores so the result is a valid unquoted
// identifier and stable across the Open/Commit/Abandon calls for one
// claim. Deterministic naming means a duplicated Open under the same
// claim_id reuses the same schema rather than leaking a second one.
func stagingSchemaName(claimID string) string {
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			return r
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		default:
			return '_'
		}
	}, claimID)
	return stagingSchemaPrefix + safe
}

// openStaging reserves a per-claim staging schema for a staged_async
// scope-bytes claim and returns the OpenOutcome the executor uses. The
// Address names the staging schema (where the executor writes); the
// ClaimScope is the canonical selector (the conflict key), preserving
// byte-equality semantics.
func (s *Store) openStaging(ctx context.Context, claimID, selector string) (json.RawMessage, json.RawMessage, error) {
	if !schemaIdentRegex.MatchString(selector) {
		return nil, nil, fmt.Errorf(
			"postgres store: staged claim selector %q is not a valid schema identifier "+
				"(lowercase letters/digits/underscore; not starting with a digit)", selector)
	}
	staging := stagingSchemaName(claimID)
	// @deliberate: IF NOT EXISTS makes a duplicated Open under the same
	// claim_id idempotent (deterministic schema name) rather than erroring
	// on the second reservation.
	stmt := "CREATE SCHEMA IF NOT EXISTS " + pgx.Identifier{staging}.Sanitize()
	if _, err := s.pool.Exec(ctx, stmt); err != nil {
		return nil, nil, fmt.Errorf("postgres store: reserve staging schema %q: %w", staging, err)
	}
	addr, err := json.Marshal(staging)
	if err != nil {
		return nil, nil, fmt.Errorf("postgres store: marshal staging address: %w", err)
	}
	scope, err := json.Marshal(selector)
	if err != nil {
		return nil, nil, fmt.Errorf("postgres store: marshal claim scope: %w", err)
	}
	return json.RawMessage(addr), json.RawMessage(scope), nil
}

// commitStagingSwap performs the atomic schema swap inside one tx:
// drop the canonical schema, then rename staging into its place. The
// transaction is the atomicity envelope — either the canonical reflects
// the staged rows and staging is consumed, or nothing changes.
//
// The canonical drop is RESTRICT (the default, no CASCADE) on purpose:
// a no-surprise-data-loss property. CASCADE would silently destroy
// objects in OTHER schemas that depend on the canonical, and would let
// the swap clobber a populated canonical without the operator's
// knowledge. Refusing the cutover when the canonical is non-empty or has
// external dependents — surfacing it as pg/swap_failed instead — keeps
// the substrate from quietly destroying data the operator did not
// stage. The swap's contract is therefore: the canonical is empty (or
// externally torn down) at cutover time; a populated/depended-upon
// canonical yields pg/swap_failed with the staging left intact.
func (s *Store) commitStagingSwap(ctx context.Context, canonical, staging string) error {
	// @deliberate: every failure below is a swap collision the store
	// classes `pg/swap_failed`. Returning a *ClassedError (rather than a
	// bare fmt.Errorf) lets the gRPC Commit boundary recover the class
	// without string-matching the message and stamp it into a
	// google.rpc.ErrorInfo detail so rimsky's claim-producer client routes
	// it through the holder's `error_types:` chain.
	swapFailed := func(err error) error { return &ClassedError{Class: SwapFailedClass, Err: err} }
	if !schemaIdentRegex.MatchString(canonical) {
		return swapFailed(fmt.Errorf("postgres store: canonical %q is not a valid schema identifier", canonical))
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return swapFailed(fmt.Errorf("postgres store: begin swap tx: %w", err))
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()

	canonicalID := pgx.Identifier{canonical}.Sanitize()
	stagingID := pgx.Identifier{staging}.Sanitize()

	// @deliberate: drop the old canonical with RESTRICT (the default — no
	// CASCADE). Fails — leaving the staging intact via rollback — if the
	// canonical is non-empty or has external dependents, which is exactly
	// the collision the swap must refuse.
	if _, err := tx.Exec(ctx, "DROP SCHEMA IF EXISTS "+canonicalID); err != nil {
		return swapFailed(fmt.Errorf("postgres store: drop canonical schema %q for swap: %w", canonical, err))
	}
	// @deliberate: rename staging into the canonical name inside the same
	// tx as the drop above. With the old canonical dropped, this is the
	// cutover; committing the tx makes both steps visible atomically.
	if _, err := tx.Exec(ctx, "ALTER SCHEMA "+stagingID+" RENAME TO "+canonicalID); err != nil {
		return swapFailed(fmt.Errorf("postgres store: rename staging schema %q into %q: %w", staging, canonical, err))
	}
	if err := tx.Commit(ctx); err != nil {
		return swapFailed(fmt.Errorf("postgres store: commit swap tx: %w", err))
	}
	committed = true
	return nil
}

// dropStaging discards a staging schema (DROP SCHEMA ... CASCADE).
// CASCADE here is safe and intended: the staging schema is private to
// the claim, holds only the executor's discardable staged work, and
// nothing legitimate depends on it. Used by Abandon (explicit discard)
// and Release (residual cleanup of an interrupted claim).
func (s *Store) dropStaging(ctx context.Context, staging string) error {
	if staging == "" {
		return nil
	}
	if !schemaIdentRegex.MatchString(staging) {
		return fmt.Errorf("postgres store: drop staging: %q is not a valid schema identifier", staging)
	}
	stmt := "DROP SCHEMA IF EXISTS " + pgx.Identifier{staging}.Sanitize() + " CASCADE"
	if _, err := s.pool.Exec(ctx, stmt); err != nil {
		return fmt.Errorf("postgres store: drop staging schema %q: %w", staging, err)
	}
	return nil
}

// decodeSchemaName decodes a JSON-string Address/ClaimScope (as produced
// by openStaging / the scope-bytes Open branch) into its schema name.
// Empty input decodes to the empty string (a claim that carried no
// reserved schema), which the callers treat as "no staging to act on".
func decodeSchemaName(raw []byte) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	var name string
	if err := json.Unmarshal(raw, &name); err != nil {
		return "", fmt.Errorf("postgres store: decode schema name %q: %w", string(raw), err)
	}
	return name, nil
}
