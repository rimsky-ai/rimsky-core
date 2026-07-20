// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func openSQLiteForDeadLetter(t *testing.T) persistence.Database {
	t.Helper()
	ctx := context.Background()
	d, err := persistence.Open(ctx, persistence.Config{
		Driver: "sqlite",
		SQLite: &persistence.SQLiteConfig{Path: filepath.Join(t.TempDir(), "deadletter.db")},
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := d.Migrate(ctx, shared.SilentLogger{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func TestDeliverNamedMessageInTx_NoReceiverRecordsDeadLetterAuditEvent(t *testing.T) {
	ctx := context.Background()
	d := openSQLiteForDeadLetter(t)
	tables := d.Tables()

	templateHash := "sha256-" + uuid.NewString()
	instanceID := shared.UUID(uuid.New())

	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := tables.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID:    templateHash,
			State: persistence.TemplateStateDeployed,
		}, tx); err != nil {
			return err
		}
		_, err := tables.Instances().Create(ctx, persistence.InstanceCreateInput{
			ID: instanceID, TemplateHash: templateHash,
		}, tx)
		return err
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	msg := persistence.MessageRow{
		ID:   shared.UUID(uuid.New()),
		Type: "no-such-receiver-type",
	}

	require := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("deliverNamedMessageInTx: %v", err)
		}
	}
	require(tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return deliverNamedMessageInTx(ctx, tables, shared.SilentLogger{}, tx,
			instanceID, shared.UUID(uuid.New()), msg, shared.UUID(uuid.New()), time.Now())
	}))

	var evs persistence.EventListResult
	require(tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := tables.Events().List(ctx, persistence.EventListFilter{InstanceID: &instanceID},
			persistence.ListPagination{Limit: 50}, tx)
		evs = r
		return err
	}))

	var found *persistence.EventRow
	for i := range evs.Events {
		if evs.Events[i].KindRaw == events.KindMessageDeadLettered().String() {
			found = &evs.Events[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("expected a %s event to be persisted for the no-receiver message; events = %+v",
			events.KindMessageDeadLettered().String(), evs.Events)
	}
	if found.Payload["message_id"] != msg.ID.String() {
		t.Fatalf("dead-letter event payload message_id = %v, want %s", found.Payload["message_id"], msg.ID.String())
	}
	if found.Payload["message_type"] != msg.Type {
		t.Fatalf("dead-letter event payload message_type = %v, want %s", found.Payload["message_type"], msg.Type)
	}
}
