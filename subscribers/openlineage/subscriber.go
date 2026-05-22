// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

// Package main — openlineage subscriber. Polls `table:rimsky_lineage`
// for new rows since a stored cursor and emits OpenLineage 1.x JSON
// events to a configured backend.
//
// Spec .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
// §OpenLineage emitter.
//
//	@concept: lineage
//	@concept: lineage-record
//
// Polling (not LifecycleSubscriber events) is the V1 transport per the
// plan's pre-resolved decisions: it treats the subscriber as a passive
// reader of the projection, decoupled from the live lifecycle path. The
// cursor is the most-recent `observed_at` already emitted; new rows
// are read with `observed_at > $cursor` ordered by `observed_at`.
//
// Pgx is allowed under `subscribers/` per `.golangci.yml`'s
// `pgx-isolation` allowlist (extended in this dispatch). The subscriber
// is a standalone binary; it never imports `runtime/` or the
// persistence Tables interfaces — both layers live behind the
// service-protocol boundary, and the subscriber reads only the
// lineage projection.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// LineageRow mirrors the columns the subscriber reads from
// `table:rimsky_lineage`. The `Record` field is decoded into either
// LeafRunRecord or ClaimTerminalRecord based on `RecordKind`.
type LineageRow struct {
	ID         uuid.UUID
	RecordKind string
	InstanceID uuid.UUID
	FrameID    uuid.UUID
	ObservedAt time.Time
	Record     json.RawMessage
}

// LeafRunRecord mirrors the `record_kind = 'leaf_run'` shape from
// spec §rimsky_lineage / Leaf-run record shape. Field-by-field
// alignment with `runtime/lineage_writer.go::LeafRunRecord` is
// load-bearing — the wire-contract test in `subscriber_test.go` pins
// the json-tag discipline (required vs `omitempty`) AND the field
// order (writer-side is canonical).
//
// Required (no `omitempty`) on both sides: `run_id`, `node_id`,
// `frame_id`, `state`, `last_outcome` — every leaf-run lineage row
// must carry the run/node/frame identity plus the state-machine
// verdict, otherwise the row is meaningless for the OpenLineage
// emitter and the ancestor walker.
type LeafRunRecord struct {
	RunID              string            `json:"run_id"`
	NodeID             string            `json:"node_id"`
	FrameID            string            `json:"frame_id"`
	ChildKey           string            `json:"child_key,omitempty"`
	NodeAlias          string            `json:"node_alias,omitempty"`
	ParentRunID        string            `json:"parent_run_id,omitempty"`
	FrameTriggerKind   string            `json:"frame_trigger_kind,omitempty"`
	TriggerMessageID   string            `json:"trigger_message_id,omitempty"`
	HeldClaims         []HeldClaimRef    `json:"held_claims,omitempty"`
	ExecutorName       string            `json:"executor_name,omitempty"`
	ExecutorVersion    string            `json:"executor_version,omitempty"`
	TemplateHash       string            `json:"template_hash,omitempty"`
	TemplateNodeAlias  string            `json:"template_node_alias,omitempty"`
	ParamsSnapshotHash string            `json:"params_snapshot_hash,omitempty"`
	AttributesHash     string            `json:"attributes_hash,omitempty"`
	ScopeDataHash      string            `json:"scope_data_hash,omitempty"`
	State              string            `json:"state"`
	LastOutcome        string            `json:"last_outcome"`
	Changed            bool              `json:"changed,omitempty"`
	TerminalKind       string            `json:"terminal_kind,omitempty"`
	ErrorClass         string            `json:"error_class,omitempty"`
	SubstitutionRefs   []SubstitutionRef `json:"substitution_refs,omitempty"`
	Extra              map[string]any    `json:"extra,omitempty"`
}

// HeldClaimRef is one entry of LeafRunRecord.HeldClaims.
type HeldClaimRef struct {
	ClaimHandleID string `json:"claim_handle_id"`
	Role          string `json:"role"`
	ProducerName  string `json:"producer_name"`
	ScopeDataHash string `json:"scope_data_hash"`
}

// SubstitutionRef is one entry of LeafRunRecord.SubstitutionRefs.
// Mirrors runtime/lineage_writer.go::SubstitutionRef — the field shapes
// must stay byte-for-byte aligned. The richer object shape (vs a bare
// `[]string`) lets `/lineage/by-source/{kind}/{id}` discriminate by
// source kind, and lets the ancestor walker resolve upstream lineage by
// RUN id rather than node-type.
type SubstitutionRef struct {
	SourceKind        string `json:"source_kind"`
	SourceNodeAlias   string `json:"source_node_alias,omitempty"`
	SourceVersionOrID string `json:"source_version_or_id,omitempty"`
}

// ClaimTerminalRecord mirrors the `record_kind = 'claim_terminal'` shape
// (Commit + Abandon + force-cancelled). The Outcome field discriminates
// the per-row terminal disposition.
//
// `OpenLineageRunRef` is the run identity the emitter keys on for the
// emitted OL `Run.RunID`. It is NOT a parent-run reference in the
// run-tree sense — see the writer-side struct comment in
// `runtime/lineage_writer.go::ClaimTerminalRecord` for the rationale.
//
// Field-by-field alignment with `runtime/lineage_writer.go::ClaimTerminalRecord`
// is load-bearing — the wire-contract test in `subscriber_test.go` pins
// the json-tag discipline (required vs `omitempty`) AND the field
// order (writer-side is canonical).
//
// Required (no `omitempty`) on both sides: `claim_handle_id`, `run_id`,
// `node_id`, `frame_id`, `outcome` — every claim-terminal row must
// carry the claim/run/node/frame identity plus the terminal disposition
// (`committed | abandoned | force_cancelled`), otherwise the row
// cannot be reconstructed against `rimsky_claim_handles` and the
// emitted OpenLineage event would mislabel the lineage graph.
type ClaimTerminalRecord struct {
	ClaimHandleID       string         `json:"claim_handle_id"`
	RunID               string         `json:"run_id"`
	NodeID              string         `json:"node_id"`
	FrameID             string         `json:"frame_id"`
	ParentClaimHandleID string         `json:"parent_claim_handle_id,omitempty"`
	OpenLineageRunRef   string         `json:"open_lineage_run_ref,omitempty"`
	SubClaimHandleIDs   []string       `json:"sub_claim_handle_ids,omitempty"`
	CommittedAt         string         `json:"committed_at,omitempty"`
	ProducerName        string         `json:"producer_name,omitempty"`
	ScopeDataHash       string         `json:"scope_data_hash,omitempty"`
	VersionID           string         `json:"version_id,omitempty"`
	Outcome             string         `json:"outcome"`
	Cause               string         `json:"cause,omitempty"`
	ProducerMetadata    map[string]any `json:"producer_metadata,omitempty"`
}

// Subscriber owns the polling loop + cursor state. Construct with
// New, drive with Run.
//
// Cursor shape is `(observed_at, id)` so two rows sharing the same
// `observed_at` (no UNIQUE constraint on the column) are not skipped
// across ticks. The fetch predicate uses the lexicographic tuple
// comparison (`(observed_at, id) > ($1, $2)`); the cursor advances to
// the LAST successfully-emitted row's tuple after each tick.
type Subscriber struct {
	cfg      Config
	rimsky   *pgxpool.Pool
	state    *pgxpool.Pool
	emitter  *Emitter
	logger   *slog.Logger
	nowFn    func() time.Time
	cursorAt time.Time // in-memory mirror of `rimsky_openlineage_cursor.last_observed_at`
	cursorID uuid.UUID // in-memory mirror of `rimsky_openlineage_cursor.last_id`
}

// New constructs a Subscriber against the two configured DSNs +
// emitter. Returns an error if either connection pool fails to open.
func New(ctx context.Context, cfg Config, logger *slog.Logger) (*Subscriber, error) {
	rimsky, err := pgxpool.New(ctx, cfg.RimskyDSN)
	if err != nil {
		return nil, fmt.Errorf("openlineage: open rimsky DSN: %w", err)
	}
	state := rimsky
	if cfg.StateDSN != cfg.RimskyDSN {
		state, err = pgxpool.New(ctx, cfg.StateDSN)
		if err != nil {
			rimsky.Close()
			return nil, fmt.Errorf("openlineage: open state DSN: %w", err)
		}
	}
	s := &Subscriber{
		cfg:     cfg,
		rimsky:  rimsky,
		state:   state,
		emitter: NewEmitter(cfg.BackendURL),
		logger:  logger,
		nowFn:   time.Now,
	}
	if err := s.ensureCursorTable(ctx); err != nil {
		s.Close()
		return nil, err
	}
	if err := s.loadCursor(ctx); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the connection pools.
func (s *Subscriber) Close() {
	if s.rimsky != nil {
		s.rimsky.Close()
	}
	if s.state != nil && s.state != s.rimsky {
		s.state.Close()
	}
}

// ensureCursorTable creates `rimsky_openlineage_cursor` on the state DB
// if missing. Singleton row keyed on the configured Namespace so one
// state DB can host cursors for multiple namespaced subscribers.
//
// Schema carries `(last_observed_at, last_id)` so the per-tick cursor
// can survive rows that share the same `observed_at` (no UNIQUE
// constraint on `rimsky_lineage.observed_at` — two rows can land in
// the same microsecond on busy systems).
//
// Forward-compat migration: pre-cycle-2 cursor rows lack `last_id`.
// `ADD COLUMN IF NOT EXISTS` backfills missing rows with the zero
// UUID by default, but the zero UUID would corrupt the
// `(observed_at, id) > ($1, $2)` tuple comparison: any non-zero UUID
// is strictly greater than the zero UUID, so the very first poll on
// a backfilled cursor would re-emit every row already delivered at
// the cursor's `observed_at` (the predicate matches any row at the
// same `observed_at` whose id ≠ zero). Bump backfilled rows to the
// canonical max UUID (`ff..ff`) so the post-migration semantics match
// "I've emitted everything ≤ this observed_at": every row at the
// cursor's `observed_at` falls on the `≤` side until the cursor
// advances on the next emit.
func (s *Subscriber) ensureCursorTable(ctx context.Context) error {
	if _, err := s.state.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS rimsky_openlineage_cursor (
		    namespace          TEXT PRIMARY KEY,
		    last_observed_at   TIMESTAMPTZ NOT NULL,
		    last_id            UUID NOT NULL DEFAULT '00000000-0000-0000-0000-000000000000',
		    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
		)`); err != nil {
		return err
	}
	if _, err := s.state.Exec(ctx, `
		ALTER TABLE rimsky_openlineage_cursor
		    ADD COLUMN IF NOT EXISTS last_id UUID NOT NULL DEFAULT '00000000-0000-0000-0000-000000000000'`); err != nil {
		return err
	}
	// Repair pre-cycle-2 cursors that came through the ADD COLUMN
	// default path. Idempotent: rows whose `last_id` was set by a
	// prior persistCursor (non-zero) are left alone.
	_, err := s.state.Exec(ctx, `
		UPDATE rimsky_openlineage_cursor
		   SET last_id = 'ffffffff-ffff-ffff-ffff-ffffffffffff'
		 WHERE last_id = '00000000-0000-0000-0000-000000000000'`)
	return err
}

// loadCursor reads the persisted cursor row for the configured namespace
// (or seeds it at epoch zero when missing).
func (s *Subscriber) loadCursor(ctx context.Context) error {
	var (
		at time.Time
		id uuid.UUID
	)
	err := s.state.QueryRow(ctx,
		`SELECT last_observed_at, last_id FROM rimsky_openlineage_cursor WHERE namespace = $1`,
		s.cfg.Namespace,
	).Scan(&at, &id)
	if err == nil {
		s.cursorAt = at
		s.cursorID = id
		return nil
	}
	if err != pgx.ErrNoRows {
		return fmt.Errorf("openlineage: load cursor: %w", err)
	}
	s.cursorAt = time.Unix(0, 0)
	s.cursorID = uuid.Nil
	_, err = s.state.Exec(ctx,
		`INSERT INTO rimsky_openlineage_cursor (namespace, last_observed_at, last_id)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (namespace) DO NOTHING`,
		s.cfg.Namespace, s.cursorAt, s.cursorID,
	)
	return err
}

// persistCursor writes the in-memory cursor back to the state DB.
func (s *Subscriber) persistCursor(ctx context.Context) error {
	_, err := s.state.Exec(ctx,
		`UPDATE rimsky_openlineage_cursor
		    SET last_observed_at = $1, last_id = $2, updated_at = now()
		  WHERE namespace = $3`,
		s.cursorAt, s.cursorID, s.cfg.Namespace,
	)
	return err
}

// fetchSince reads up to BatchSize lineage rows whose `(observed_at, id)`
// tuple is strictly greater than the current cursor, in (observed_at,
// id) ASC order. The tuple comparison ensures rows that share the same
// `observed_at` as the cursor row are not skipped — only rows with the
// same `observed_at` but a `<=` id are excluded.
func (s *Subscriber) fetchSince(ctx context.Context) ([]LineageRow, error) {
	rows, err := s.rimsky.Query(ctx, `
		SELECT id, record_kind, instance_id, frame_id, observed_at, record
		  FROM rimsky_lineage
		 WHERE (observed_at, id) > ($1, $2)
		 ORDER BY observed_at ASC, id ASC
		 LIMIT $3`,
		s.cursorAt, s.cursorID, s.cfg.BatchSize,
	)
	if err != nil {
		return nil, fmt.Errorf("openlineage: fetch: %w", err)
	}
	defer rows.Close()
	out := make([]LineageRow, 0, s.cfg.BatchSize)
	for rows.Next() {
		var r LineageRow
		if err := rows.Scan(&r.ID, &r.RecordKind, &r.InstanceID, &r.FrameID, &r.ObservedAt, &r.Record); err != nil {
			return nil, fmt.Errorf("openlineage: scan: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// tick runs one poll iteration: fetch, emit, advance + persist cursor.
// Decode failures (`toEvent` returns an error) advance the cursor past
// the bad row — the row will never become decodable, so blocking the
// loop would stall the subscriber forever on one malformed payload.
// Emit failures (transient backend errors) DO halt the loop without
// advancing the cursor; the next tick retries from the same point.
//
// The cursor advances to `(row.ObservedAt, row.ID)` of the last
// row processed (emitted or decode-failed-and-skipped). The tuple
// shape protects against same-`observed_at` rows being skipped across
// ticks (no UNIQUE on `rimsky_lineage.observed_at`).
func (s *Subscriber) tick(ctx context.Context) error {
	rows, err := s.fetchSince(ctx)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	var (
		lastAt     time.Time
		lastID     uuid.UUID
		progressed bool
	)
	for _, r := range rows {
		ev, err := s.toEvent(r)
		if err != nil {
			s.logger.Warn("openlineage.decode_failed",
				"id", r.ID.String(),
				"record_kind", r.RecordKind,
				"error", err.Error())
			// Decode failures are permanent. Advance past the
			// undecodable row so the cursor doesn't stall here.
			lastAt = r.ObservedAt
			lastID = r.ID
			progressed = true
			continue
		}
		if err := s.emitter.Send(ctx, ev); err != nil {
			s.logger.Warn("openlineage.emit_failed",
				"id", r.ID.String(),
				"record_kind", r.RecordKind,
				"error", err.Error())
			break
		}
		lastAt = r.ObservedAt
		lastID = r.ID
		progressed = true
	}
	if progressed {
		s.cursorAt = lastAt
		s.cursorID = lastID
		if err := s.persistCursor(ctx); err != nil {
			return fmt.Errorf("openlineage: persist cursor: %w", err)
		}
	}
	return nil
}

// toEvent decodes the per-kind record JSON and dispatches to the
// matching MakeXxx mapper.
func (s *Subscriber) toEvent(r LineageRow) (Event, error) {
	switch r.RecordKind {
	case "leaf_run":
		var rec LeafRunRecord
		if err := json.Unmarshal(r.Record, &rec); err != nil {
			return Event{}, fmt.Errorf("decode leaf_run: %w", err)
		}
		return MakeLeafRunEvent(rec, r.ObservedAt, r.InstanceID.String(), s.cfg.Namespace), nil
	case "claim_terminal":
		var rec ClaimTerminalRecord
		if err := json.Unmarshal(r.Record, &rec); err != nil {
			return Event{}, fmt.Errorf("decode claim_terminal: %w", err)
		}
		return MakeClaimTerminalEvent(rec, r.ObservedAt, s.cfg.Namespace), nil
	default:
		return Event{}, fmt.Errorf("unknown record_kind %q", r.RecordKind)
	}
}

// Run loops on PollInterval until ctx is cancelled. Per-tick errors are
// logged at WARN; the loop continues.
func (s *Subscriber) Run(ctx context.Context) {
	t := time.NewTicker(s.cfg.PollInterval)
	defer t.Stop()
	s.logger.Info("openlineage.starting",
		"namespace", s.cfg.Namespace,
		"backend_url", s.cfg.BackendURL,
		"poll_interval", s.cfg.PollInterval.String(),
		"cursor", s.cursorAt.Format(time.RFC3339Nano),
	)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := s.tick(ctx); err != nil {
				s.logger.Warn("openlineage.tick_failed", "error", err.Error())
			}
		}
	}
}
