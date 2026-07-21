// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: lineage
// @concept: lineage-record
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rimsky-ai/rimsky-core/lib/services/subscribers/openlineage/wire"
)

type LineageRow struct {
	ID         uuid.UUID
	RecordKind string
	InstanceID uuid.UUID
	FrameID    uuid.UUID
	ObservedAt time.Time
	Record     json.RawMessage
}

type LeafRunRecord = wire.LeafRunRecord

type HeldClaimRef = wire.HeldClaimRef

type SubstitutionRef = wire.SubstitutionRef

type ClaimTerminalRecord = wire.ClaimTerminalRecord

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
	if err := s.ensureDeadLetterTable(ctx); err != nil {
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
	_, err := s.state.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS rimsky_openlineage_cursor (
		    namespace          TEXT PRIMARY KEY,
		    last_observed_at   TIMESTAMPTZ NOT NULL,
		    last_id            UUID NOT NULL DEFAULT '00000000-0000-0000-0000-000000000000',
		    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
		)`)
	return err
}

func (s *Subscriber) ensureDeadLetterTable(ctx context.Context) error {
	_, err := s.state.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS rimsky_openlineage_dead_letter (
		    id               UUID NOT NULL,
		    namespace        TEXT NOT NULL,
		    record_kind      TEXT NOT NULL,
		    instance_id      UUID NOT NULL,
		    frame_id         UUID NOT NULL,
		    observed_at      TIMESTAMPTZ NOT NULL,
		    record           JSONB NOT NULL,
		    status_code      INT NOT NULL,
		    reason           TEXT NOT NULL,
		    dead_lettered_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		    PRIMARY KEY (namespace, id)
		)`)
	return err
}

func (s *Subscriber) persistDeadLetter(ctx context.Context, r LineageRow, statusCode int, reason string) error {
	_, err := s.state.Exec(ctx, `
		INSERT INTO rimsky_openlineage_dead_letter
		    (id, namespace, record_kind, instance_id, frame_id, observed_at, record, status_code, reason)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (namespace, id) DO NOTHING`,
		r.ID, s.cfg.Namespace, r.RecordKind, r.InstanceID, r.FrameID, r.ObservedAt, r.Record, statusCode, reason,
	)
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
		  WHERE namespace = $3
		    AND (last_observed_at, last_id) < ($1, $2)`,
		s.cursorAt, s.cursorID, s.cfg.Namespace,
	)
	return err
}

func (s *Subscriber) fetchSince(ctx context.Context) ([]LineageRow, error) {
	safeUntil := s.nowFn().Add(-s.cfg.LagWindow)
	rows, err := s.rimsky.Query(ctx, `
		SELECT id, record_kind, instance_id, frame_id, observed_at, record
		  FROM rimsky_lineage
		 WHERE (observed_at, id) > ($1, $2)
		   AND observed_at < $3
		 ORDER BY observed_at ASC, id ASC
		 LIMIT $4`,
		s.cursorAt, s.cursorID, safeUntil, s.cfg.BatchSize,
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
	for _, r := range rows {
		ev, err := s.toEvent(r)
		if err != nil {
			s.logger.Error("openlineage.decode_failed_dead_letter",
				"id", r.ID.String(),
				"record_kind", r.RecordKind,
				"error", err.Error())
			if dlErr := s.persistDeadLetter(ctx, r, 0, "decode_failed: "+err.Error()); dlErr != nil {
				s.logger.Warn("openlineage.dead_letter_persist_failed",
					"id", r.ID.String(), "error", dlErr.Error())
			}
			if err := s.advanceCursor(ctx, r); err != nil {
				return err
			}
			continue
		}
		if err := s.emitter.Send(ctx, ev); err != nil {
			var sendErr *SendError
			if errors.As(err, &sendErr) && sendErr.Permanent() {
				s.logger.Error("openlineage.emit_rejected_dead_letter",
					"id", r.ID.String(),
					"record_kind", r.RecordKind,
					"status_code", sendErr.StatusCode,
					"error", err.Error())
				if dlErr := s.persistDeadLetter(ctx, r, sendErr.StatusCode, err.Error()); dlErr != nil {
					s.logger.Warn("openlineage.dead_letter_persist_failed",
						"id", r.ID.String(), "error", dlErr.Error())
				}
				if err := s.advanceCursor(ctx, r); err != nil {
					return err
				}
				continue
			}
			s.logger.Warn("openlineage.emit_failed",
				"id", r.ID.String(),
				"record_kind", r.RecordKind,
				"error", err.Error())
			return nil
		}
		if err := s.advanceCursor(ctx, r); err != nil {
			return err
		}
	}
	return nil
}

func (s *Subscriber) advanceCursor(ctx context.Context, r LineageRow) error {
	s.cursorAt = r.ObservedAt
	s.cursorID = r.ID
	if err := s.persistCursor(ctx); err != nil {
		return fmt.Errorf("openlineage: persist cursor: %w", err)
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
		return MakeLeafRunEvent(rec, r.ObservedAt, s.cfg.Namespace), nil
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
