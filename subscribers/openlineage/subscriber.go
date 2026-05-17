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
// spec §rimsky_lineage / Leaf-run record shape.
type LeafRunRecord struct {
	RunID              string         `json:"run_id"`
	NodeAlias          string         `json:"node_alias"`
	ChildKey           string         `json:"child_key,omitempty"`
	ParentRunID        string         `json:"parent_run_id,omitempty"`
	FrameTriggerKind   string         `json:"frame_trigger_kind,omitempty"`
	TriggerMessageID   string         `json:"trigger_message_id,omitempty"`
	SubstitutionRefs   []any          `json:"substitution_refs,omitempty"`
	HeldClaims         []HeldClaimRef `json:"held_claims,omitempty"`
	ExecutorName       string         `json:"executor_name,omitempty"`
	ExecutorVersion    string         `json:"executor_version,omitempty"`
	TemplateHash       string         `json:"template_hash,omitempty"`
	TemplateNodeAlias  string         `json:"template_node_alias,omitempty"`
	ParamsSnapshotHash string         `json:"params_snapshot_hash,omitempty"`
	UserdataHash       string         `json:"userdata_hash,omitempty"`
	Changed            bool           `json:"changed,omitempty"`
	LastOutcome        string         `json:"last_outcome,omitempty"`
	TerminalKind       string         `json:"terminal_kind,omitempty"`
}

// HeldClaimRef is one entry of LeafRunRecord.HeldClaims.
type HeldClaimRef struct {
	ClaimHandleID string `json:"claim_handle_id"`
	Role          string `json:"role"`
	ProducerName  string `json:"producer_name"`
	ScopeDataHash string `json:"scope_data_hash"`
}

// ClaimTerminalRecord mirrors the `record_kind = 'claim_terminal'` shape
// (Commit + Abandon + force-cancelled). The Outcome field discriminates
// the per-row terminal disposition.
type ClaimTerminalRecord struct {
	ClaimHandleID     string   `json:"claim_handle_id"`
	VersionID         string   `json:"version_id,omitempty"`
	ProducerName      string   `json:"producer_name"`
	ScopeDataHash     string   `json:"scope_data_hash"`
	ParentRunID       string   `json:"parent_run_id,omitempty"`
	FrameID           string   `json:"frame_id,omitempty"`
	SubClaimHandleIDs []string `json:"sub_claim_handle_ids,omitempty"`
	CommittedAt       string   `json:"committed_at,omitempty"`
	Outcome           string   `json:"outcome,omitempty"`
	Cause             string   `json:"cause,omitempty"`
}

// Subscriber owns the polling loop + cursor state. Construct with
// New, drive with Run.
type Subscriber struct {
	cfg      Config
	rimsky   *pgxpool.Pool
	state    *pgxpool.Pool
	emitter  *Emitter
	logger   *slog.Logger
	nowFn    func() time.Time
	cursorAt time.Time // in-memory mirror of `rimsky_openlineage_cursor.last_observed_at`
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
func (s *Subscriber) ensureCursorTable(ctx context.Context) error {
	_, err := s.state.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS rimsky_openlineage_cursor (
		    namespace          TEXT PRIMARY KEY,
		    last_observed_at   TIMESTAMPTZ NOT NULL,
		    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
		)`)
	return err
}

// loadCursor reads the persisted cursor row for the configured namespace
// (or seeds it at epoch zero when missing).
func (s *Subscriber) loadCursor(ctx context.Context) error {
	var at time.Time
	err := s.state.QueryRow(ctx,
		`SELECT last_observed_at FROM rimsky_openlineage_cursor WHERE namespace = $1`,
		s.cfg.Namespace,
	).Scan(&at)
	if err == nil {
		s.cursorAt = at
		return nil
	}
	if err != pgx.ErrNoRows {
		return fmt.Errorf("openlineage: load cursor: %w", err)
	}
	s.cursorAt = time.Unix(0, 0)
	_, err = s.state.Exec(ctx,
		`INSERT INTO rimsky_openlineage_cursor (namespace, last_observed_at)
		 VALUES ($1, $2)
		 ON CONFLICT (namespace) DO NOTHING`,
		s.cfg.Namespace, s.cursorAt,
	)
	return err
}

// persistCursor writes the in-memory cursor back to the state DB.
func (s *Subscriber) persistCursor(ctx context.Context) error {
	_, err := s.state.Exec(ctx,
		`UPDATE rimsky_openlineage_cursor
		    SET last_observed_at = $1, updated_at = now()
		  WHERE namespace = $2`,
		s.cursorAt, s.cfg.Namespace,
	)
	return err
}

// fetchSince reads up to BatchSize lineage rows whose `observed_at` is
// strictly greater than the current cursor, in observed_at ASC order.
func (s *Subscriber) fetchSince(ctx context.Context) ([]LineageRow, error) {
	rows, err := s.rimsky.Query(ctx, `
		SELECT id, record_kind, instance_id, frame_id, observed_at, record
		  FROM rimsky_lineage
		 WHERE observed_at > $1
		 ORDER BY observed_at ASC, id ASC
		 LIMIT $2`,
		s.cursorAt, s.cfg.BatchSize,
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
// Per-row emit failures are logged but do not advance the cursor
// past the failing row; the next tick retries from the same point.
func (s *Subscriber) tick(ctx context.Context) error {
	rows, err := s.fetchSince(ctx)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	var lastSuccessful time.Time
	for _, r := range rows {
		ev, err := s.toEvent(r)
		if err != nil {
			s.logger.Warn("openlineage.decode_failed",
				"id", r.ID.String(),
				"record_kind", r.RecordKind,
				"error", err.Error())
			break
		}
		if err := s.emitter.Send(ctx, ev); err != nil {
			s.logger.Warn("openlineage.emit_failed",
				"id", r.ID.String(),
				"record_kind", r.RecordKind,
				"error", err.Error())
			break
		}
		lastSuccessful = r.ObservedAt
	}
	if !lastSuccessful.IsZero() {
		s.cursorAt = lastSuccessful
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
