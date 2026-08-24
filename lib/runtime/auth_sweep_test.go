// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package runtime_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/auth"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	_ "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
)

type appendFailingEventTable struct {
	persistence.EventTable
	err error
}

func (f appendFailingEventTable) Append(_ context.Context, _ persistence.EventAppendInput, _ persistence.Tx) error {
	return f.err
}

type appendFailingTables struct {
	persistence.Tables
	err error
}

func (f appendFailingTables) Events() persistence.EventTable {
	return appendFailingEventTable{f.Tables.Events(), f.err}
}

func TestSweepRotationGrace(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	d, err := persistence.Open(ctx, persistence.Config{
		Driver: "sqlite",
		SQLite: &persistence.SQLiteConfig{Path: filepath.Join(dir, "state.db")},
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if err := d.Migrate(ctx, shared.SilentLogger{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	tables := d.Tables()

	clock := shared.NewControllableClock(time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC))

	keyID := uuid.New()
	hash := sha256.Sum256([]byte("rk_sweep_target"))
	past := clock.Now().Add(-1 * time.Minute)
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := tables.APIKeys().Insert(ctx, persistence.APIKey{
			ID:          keyID,
			KeyHash:     hash[:],
			Name:        "to-sweep",
			Permissions: []byte(`[{"action":"*"}]`),
			CreatedAt:   clock.Now().Add(-1 * time.Hour),
		}, tx); err != nil {
			return err
		}
		_, err := tables.APIKeys().SetRevokeAt(ctx, keyID, past, tx)
		return err
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var hookCalls int
	unregister := runtime.RegisterAuthMutationHook(func() { hookCalls++ })
	defer unregister()

	logger := shared.NewCapturingLogger()
	n, err := runtime.SweepRotationGrace(ctx, tables, clock, logger)
	if err != nil {
		t.Fatalf("SweepRotationGrace: %v", err)
	}
	if n != 1 {
		t.Fatalf("sweep returned %d; want 1", n)
	}
	if hookCalls != 1 {
		t.Fatalf("expected 1 auth-mutation hook invocation when a key was swept, got %d", hookCalls)
	}

	row, ok, err := tables.APIKeys().GetByID(ctx, keyID, nil)
	if err != nil || !ok || row.RevokedAt == nil {
		t.Fatalf("post-sweep row: err=%v ok=%v revoked_at=%v", err, ok, row.RevokedAt)
	}

	n, err = runtime.SweepRotationGrace(ctx, tables, clock, logger)
	if err != nil {
		t.Fatalf("re-sweep: %v", err)
	}
	if n != 0 {
		t.Fatalf("re-sweep returned %d; want 0", n)
	}
	if hookCalls != 1 {
		t.Fatalf("expected no additional auth-mutation hook invocation when zero keys were swept, got %d", hookCalls)
	}

	var auditFound int
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		rl, err := tables.Events().List(ctx, persistence.EventListFilter{KindIn: []string{auth.EventKeyRevoked.String()}}, persistence.ListPagination{}, tx)
		if err != nil {
			return err
		}
		for _, e := range rl.Events {
			if e.KindRaw == auth.EventKeyRevoked.String() {
				auditFound++
				if reason, _ := e.Payload.Map()["reason"].(string); reason != string(auth.RevokeReasonRotationGrace) {
					t.Errorf("audit reason: got %q want %q", reason, auth.RevokeReasonRotationGrace)
				}
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("Events.List: %v", err)
	}
	if auditFound != 1 {
		t.Fatalf("expected 1 auth.key_revoked event; got %d", auditFound)
	}
}

func TestSweepRotationGrace_AuditWriteFailureSurfacedAndAtomic(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	d, err := persistence.Open(ctx, persistence.Config{
		Driver: "sqlite",
		SQLite: &persistence.SQLiteConfig{Path: filepath.Join(dir, "state.db")},
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if err := d.Migrate(ctx, shared.SilentLogger{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	tables := d.Tables()

	clock := shared.NewControllableClock(time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC))

	keyID := uuid.New()
	hash := sha256.Sum256([]byte("rk_sweep_audit_fail"))
	past := clock.Now().Add(-1 * time.Minute)
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := tables.APIKeys().Insert(ctx, persistence.APIKey{
			ID:          keyID,
			KeyHash:     hash[:],
			Name:        "to-sweep-audit-fail",
			Permissions: []byte(`[{"action":"*"}]`),
			CreatedAt:   clock.Now().Add(-1 * time.Hour),
		}, tx); err != nil {
			return err
		}
		_, err := tables.APIKeys().SetRevokeAt(ctx, keyID, past, tx)
		return err
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	appendErr := errors.New("simulated audit append failure")
	failing := appendFailingTables{Tables: tables, err: appendErr}
	logger := shared.NewCapturingLogger()

	n, err := runtime.SweepRotationGrace(ctx, failing, clock, logger)
	if err == nil {
		t.Fatal("expected SweepRotationGrace to surface the audit-write failure, got nil error")
	}
	if n != 0 {
		t.Fatalf("expected 0 swept on audit-write failure, got %d", n)
	}

	var foundErrorLog bool
	for _, r := range logger.Records() {
		if r.Level == "error" && r.Msg == "AUTH.KEYREVOKEDEVENT.APPENDFAILED" {
			foundErrorLog = true
		}
	}
	if !foundErrorLog {
		t.Fatalf("expected an error-level AUTH.KEYREVOKEDEVENT.APPENDFAILED log record; got %+v", logger.Records())
	}

	row, ok, err := tables.APIKeys().GetByID(ctx, keyID, nil)
	if err != nil || !ok {
		t.Fatalf("post-attempt row lookup: err=%v ok=%v", err, ok)
	}
	if row.RevokedAt != nil {
		t.Fatalf("expected revocation to be rolled back when the audit write fails atomically, but revoked_at=%v", row.RevokedAt)
	}
}
