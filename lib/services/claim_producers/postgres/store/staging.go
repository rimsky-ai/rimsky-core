// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: atomic-staging

package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5"
)

const SwapFailedClass = "pg/swap_failed"

const ClaimUnavailableClass = "pg/claim_unavailable"

const NotReplaceableClass = "pg/not_atomically_replaceable"

const PartitionPolicyInvalidRequestClass = "pg/partition_policy_invalid_request"

type rowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

const externalDependentQuery = `
SELECT dep_schema, dep_object FROM (
	SELECT depns.nspname AS dep_schema, dep.relname AS dep_object
	  FROM pg_depend d
	  JOIN pg_rewrite rw ON d.classid = 'pg_rewrite'::regclass AND d.objid = rw.oid
	  JOIN pg_class dep ON rw.ev_class = dep.oid
	  JOIN pg_namespace depns ON dep.relnamespace = depns.oid
	  JOIN pg_class ref ON d.refobjid = ref.oid
	  JOIN pg_namespace refns ON ref.relnamespace = refns.oid
	 WHERE refns.nspname = $1 AND depns.nspname <> $1
	UNION
	SELECT ns.nspname AS dep_schema, t.relname AS dep_object
	  FROM pg_constraint con
	  JOIN pg_class ft ON con.confrelid = ft.oid
	  JOIN pg_namespace fns ON ft.relnamespace = fns.oid
	  JOIN pg_class t ON con.conrelid = t.oid
	  JOIN pg_namespace ns ON t.relnamespace = ns.oid
	 WHERE con.contype = 'f' AND fns.nspname = $1 AND ns.nspname <> $1
) ext
LIMIT 1`

func externalDependent(ctx context.Context, q rowQuerier, canonical string) (schema, object string, found bool, err error) {
	err = q.QueryRow(ctx, externalDependentQuery, canonical).Scan(&schema, &object)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, fmt.Errorf("postgres store: probe external dependents of schema %q: %w", canonical, err)
	}
	return schema, object, true, nil
}

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

const stagingSchemaHashHexLen = 48

func stagingSchemaName(claimID string) string {
	sum := sha256.Sum256([]byte(claimID))
	return stagingSchemaPrefix + hex.EncodeToString(sum[:])[:stagingSchemaHashHexLen]
}

const stagingReservationsTable = "rimsky_staging_reservations"

func (s *Store) ensureStagingReservationsTable(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS `+stagingReservationsTable+` (
		schema_name text PRIMARY KEY,
		claim_id    text NOT NULL,
		reserved_at timestamptz NOT NULL DEFAULT now()
	)`)
	if err != nil {
		return fmt.Errorf("postgres store: ensure staging reservations table: %w", err)
	}
	return nil
}

func (s *Store) recordStagingReservation(ctx context.Context, staging, claimID string) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO `+stagingReservationsTable+` (schema_name, claim_id, reserved_at) VALUES ($1, $2, now())
		 ON CONFLICT (schema_name) DO UPDATE SET claim_id = EXCLUDED.claim_id, reserved_at = now()`,
		staging, claimID)
	if err != nil {
		return fmt.Errorf("postgres store: record staging reservation for %q: %w", staging, err)
	}
	return nil
}

func (s *Store) clearStagingReservation(ctx context.Context, staging string) {
	if _, err := s.pool.Exec(ctx, `DELETE FROM `+stagingReservationsTable+` WHERE schema_name = $1`, staging); err != nil {
		slog.Warn("postgres store: clear staging reservation", "staging", staging, "error", err.Error())
	}
}

func (s *Store) openStaging(ctx context.Context, claimID, selector string) (json.RawMessage, json.RawMessage, error) {
	if !ItemsTableIdentRegex.MatchString(selector) {
		return nil, nil, fmt.Errorf(
			"postgres store: staged claim selector %q is not a valid schema identifier "+
				"(lowercase letters/digits/underscore; not starting with a digit)", selector)
	}
	if strings.HasPrefix(selector, stagingSchemaPrefix) {
		return nil, nil, fmt.Errorf(
			"postgres store: staged claim selector %q collides with the reserved staging prefix %q",
			selector, stagingSchemaPrefix)
	}
	if depSchema, depObject, found, err := externalDependent(ctx, s.pool, selector); err != nil {
		return nil, nil, err
	} else if found {
		return nil, nil, &ClassedError{Class: NotReplaceableClass, Err: fmt.Errorf(
			"postgres store: schema %q is not atomically replaceable: object %q.%q outside it depends on objects inside it; "+
				"a staged_async write claim would cascade-destroy the external dependent on swap "+
				"(remove the cross-schema reference or publish through the depending schema instead)",
			selector, depSchema, depObject)}
	}
	staging := stagingSchemaName(claimID)
	stmt := "CREATE SCHEMA IF NOT EXISTS " + pgx.Identifier{staging}.Sanitize()
	if _, err := s.pool.Exec(ctx, stmt); err != nil {
		return nil, nil, fmt.Errorf("postgres store: reserve staging schema %q: %w", staging, err)
	}
	if err := s.recordStagingReservation(ctx, staging, claimID); err != nil {
		return nil, nil, err
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
	if !ItemsTableIdentRegex.MatchString(staging) || !strings.HasPrefix(staging, stagingSchemaPrefix) {
		return fmt.Errorf(
			"postgres store: refusing to drop or rename schema %q: not a rimsky staging schema "+
				"(must be a valid identifier carrying the %q prefix)", staging, stagingSchemaPrefix)
	}
	return nil
}

func (s *Store) commitStagingSwap(ctx context.Context, canonical, staging string) error {
	swapFailed := func(err error) error { return &ClassedError{Class: SwapFailedClass, Err: err} }
	if !ItemsTableIdentRegex.MatchString(canonical) {
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

	if depSchema, depObject, found, err := externalDependent(ctx, tx, canonical); err != nil {
		return swapFailed(err)
	} else if found {
		return swapFailed(fmt.Errorf(
			"postgres store: canonical %q gained an external dependent %q.%q after Open; "+
				"refusing the swap that would cascade-destroy it",
			canonical, depSchema, depObject))
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
	s.clearStagingReservation(ctx, staging)
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
	s.clearStagingReservation(ctx, staging)
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
