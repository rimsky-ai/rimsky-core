// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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

const SwapFailedClass = "pg/swap_failed"

const ClaimUnavailableClass = "pg/claim_unavailable"

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

const stagingSchemaPrefix = "rimsky_stg_"

var schemaIdentRegex = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

func (s *Store) stagedScopeBytes(selector string) bool {
	if _, ok := s.pickPolicies[selector]; ok {
		return false
	}
	if s.writeSemantics != "staged_async" {
		return false
	}
	return schemaIdentRegex.MatchString(selector)
}

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

func (s *Store) openStaging(ctx context.Context, claimID, selector string) (json.RawMessage, json.RawMessage, error) {
	if !schemaIdentRegex.MatchString(selector) {
		return nil, nil, fmt.Errorf(
			"postgres store: staged claim selector %q is not a valid schema identifier "+
				"(lowercase letters/digits/underscore; not starting with a digit)", selector)
	}
	if strings.HasPrefix(selector, stagingSchemaPrefix) {
		return nil, nil, fmt.Errorf(
			"postgres store: staged claim selector %q collides with the reserved staging prefix %q",
			selector, stagingSchemaPrefix)
	}
	staging := stagingSchemaName(claimID)
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

func requireStagingSchemaIdent(staging string) error {
	if !schemaIdentRegex.MatchString(staging) || !strings.HasPrefix(staging, stagingSchemaPrefix) {
		return fmt.Errorf(
			"postgres store: refusing to drop or rename schema %q: not a rimsky staging schema "+
				"(must be a valid identifier carrying the %q prefix)", staging, stagingSchemaPrefix)
	}
	return nil
}

func (s *Store) commitStagingSwap(ctx context.Context, canonical, staging string) error {
	swapFailed := func(err error) error { return &ClassedError{Class: SwapFailedClass, Err: err} }
	if !schemaIdentRegex.MatchString(canonical) {
		return swapFailed(fmt.Errorf("postgres store: canonical %q is not a valid schema identifier", canonical))
	}
	if strings.HasPrefix(canonical, stagingSchemaPrefix) {
		return swapFailed(fmt.Errorf(
			"postgres store: refusing swap into %q: canonical schema must not carry the reserved staging prefix %q",
			canonical, stagingSchemaPrefix))
	}
	if err := requireStagingSchemaIdent(staging); err != nil {
		return swapFailed(err)
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

	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", staging); err != nil {
		return swapFailed(fmt.Errorf("postgres store: serialize swap on staging schema %q: %w", staging, err))
	}
	var stagingExists, canonicalExists bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM information_schema.schemata WHERE schema_name = $1),
		        EXISTS (SELECT 1 FROM information_schema.schemata WHERE schema_name = $2)`,
		staging, canonical,
	).Scan(&stagingExists, &canonicalExists); err != nil {
		return swapFailed(fmt.Errorf("postgres store: probe schemas for swap: %w", err))
	}
	if !stagingExists {
		if canonicalExists {
			return nil
		}
		return swapFailed(fmt.Errorf(
			"postgres store: staging schema %q is gone and canonical %q does not exist; swap state is indeterminate",
			staging, canonical))
	}

	canonicalID := pgx.Identifier{canonical}.Sanitize()
	stagingID := pgx.Identifier{staging}.Sanitize()

	if _, err := tx.Exec(ctx, "DROP SCHEMA IF EXISTS "+canonicalID+" CASCADE"); err != nil {
		return swapFailed(fmt.Errorf("postgres store: drop canonical schema %q for swap: %w", canonical, err))
	}
	if _, err := tx.Exec(ctx, "ALTER SCHEMA "+stagingID+" RENAME TO "+canonicalID); err != nil {
		return swapFailed(fmt.Errorf("postgres store: rename staging schema %q into %q: %w", staging, canonical, err))
	}
	if err := tx.Commit(ctx); err != nil {
		return swapFailed(fmt.Errorf("postgres store: commit swap tx: %w", err))
	}
	committed = true
	return nil
}

func (s *Store) dropStaging(ctx context.Context, staging string) error {
	if err := requireStagingSchemaIdent(staging); err != nil {
		return err
	}
	stmt := "DROP SCHEMA IF EXISTS " + pgx.Identifier{staging}.Sanitize() + " CASCADE"
	if _, err := s.pool.Exec(ctx, stmt); err != nil {
		return fmt.Errorf("postgres store: drop staging schema %q: %w", staging, err)
	}
	return nil
}

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
