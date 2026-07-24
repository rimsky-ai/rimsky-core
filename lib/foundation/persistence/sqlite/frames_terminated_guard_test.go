// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package sqlite_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	sqlitedrv "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
)

func TestPendingMessageNotPickedForTerminatedInstance(t *testing.T) {
	t.Parallel()
	d := openSQLite(t)
	ctx := context.Background()

	rawDB, ok := sqlitedrv.DBFromDatabaseForTest(d)
	if !ok {
		t.Fatal("DBFromDatabaseForTest: not a sqlite database")
	}

	templateID := "sha256-" + uuid.NewString()
	instanceID := uuid.New()

	if _, err := rawDB.ExecContext(ctx,
		`INSERT INTO rimsky_templates (id, spec, state, source) VALUES (?, '{}', 'registered', 'direct')`,
		templateID,
	); err != nil {
		t.Fatalf("seed template: %v", err)
	}
	if _, err := rawDB.ExecContext(ctx,
		`INSERT INTO rimsky_instances (id, template_hash, terminated_at, target_routing_identity)
		 VALUES (?, ?, datetime('now'), 'test-agent')`,
		instanceID.String(), templateID,
	); err != nil {
		t.Fatalf("seed terminated instance: %v", err)
	}
	msgID := uuid.New().String()
	if _, err := rawDB.ExecContext(ctx,
		`INSERT INTO rimsky_messages (id, instance_id, type, sender, sender_kind)
		 VALUES (?, ?, 'fixture/message', 'operator', 'operator')`,
		msgID, instanceID.String(),
	); err != nil {
		t.Fatalf("seed message: %v", err)
	}

	store := d.Tables()
	var picks []persistence.PendingMessagePick
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		out, err := store.Messages().PickPendingMessagesForIdleInstances(ctx, tx)
		picks = out
		return err
	}); err != nil {
		t.Fatalf("PickPendingMessagesForIdleInstances: %v", err)
	}

	for _, p := range picks {
		if p.InstanceID.String() == instanceID.String() {
			t.Fatalf("pending message %s for terminated instance %s was picked for frame open; "+
				"a frame must never open against a terminated instance", msgID, instanceID)
		}
	}
}
