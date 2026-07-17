// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: lineage
// @concept: lineage-record
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

type LineageRow struct {
	ID         uuid.UUID
	RecordKind string
	InstanceID uuid.UUID
	FrameID    uuid.UUID
	ObservedAt time.Time
	Record     json.RawMessage
}

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
	ScopeDataHash      string            `json:"claim_scope_data_hash,omitempty"`
	State              string            `json:"state"`
	SettlingSignalType string            `json:"settling_signal_type"`
	Changed            bool              `json:"changed,omitempty"`
	TerminalKind       string            `json:"terminal_kind,omitempty"`
	ErrorClass         string            `json:"error_class,omitempty"`
	SubstitutionRefs   []SubstitutionRef `json:"substitution_refs,omitempty"`
	Extra              map[string]any    `json:"extra,omitempty"`
}

type HeldClaimRef struct {
	ClaimHandleID string `json:"claim_handle_id"`
	Role          string `json:"role"`
	ProducerName  string `json:"producer_name"`
	ScopeDataHash string `json:"claim_scope_data_hash"`
}

type SubstitutionRef struct {
	SourceKind        string `json:"source_kind"`
	SourceNodeAlias   string `json:"source_node_alias,omitempty"`
	SourceVersionOrID string `json:"source_version_or_id,omitempty"`
}

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
	ScopeDataHash       string         `json:"claim_scope_data_hash,omitempty"`
	VersionID           string         `json:"version_id,omitempty"`
	Outcome             string         `json:"outcome"`
	Cause               string         `json:"cause,omitempty"`
	ProducerMetadata    map[string]any `json:"producer_metadata,omitempty"`
}

type Subscriber struct {
	cfg      Config
	rimsky   *pgxpool.Pool
	state    *pgxpool.Pool
	emitter  *Emitter
	logger   *slog.Logger
	nowFn    func() time.Time
	cursorAt time.Time
	cursorID uuid.UUID
}

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
		emitter: NewEmitter(cfg.BackendURL, cfg.BearerToken),
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

func (s *Subscriber) Close() {
	if s.rimsky != nil {
		s.rimsky.Close()
	}
	if s.state != nil && s.state != s.rimsky {
		s.state.Close()
	}
}

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
	_, err := s.state.Exec(ctx, `
		UPDATE rimsky_openlineage_cursor
		   SET last_id = 'ffffffff-ffff-ffff-ffff-ffffffffffff'
		 WHERE last_id = '00000000-0000-0000-0000-000000000000'`)
	return err
}

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

func (s *Subscriber) persistCursor(ctx context.Context) error {
	_, err := s.state.Exec(ctx,
		`UPDATE rimsky_openlineage_cursor
		    SET last_observed_at = $1, last_id = $2, updated_at = now()
		  WHERE namespace = $3`,
		s.cursorAt, s.cursorID, s.cfg.Namespace,
	)
	return err
}

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
