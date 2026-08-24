// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package sqlite_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	sqlitepersist "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

func TestSQLiteEvents_TypedKindRoundTrip(t *testing.T) {
	t.Parallel()
	d := openSQLite(t)
	ctx := context.Background()
	store := d.Tables()
	cases := []events.Kind{
		events.KindWorkStarted(),
		events.KindAuthAccessAttempted(),
		events.SignalKind("terminal/success"),
		events.SignalKind("attribute/budget_cents/changed"),
	}
	for _, k := range cases {
		k := k
		if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			return store.Events().Append(ctx, persistence.EventAppendInput{Kind: k}, tx)
		}); err != nil {
			t.Fatalf("Append(%s): %v", k.String(), err)
		}
	}
	var res persistence.EventListResult
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := store.Events().List(ctx, persistence.EventListFilter{},
			persistence.ListPagination{Limit: len(cases) + 5}, tx)
		res = r
		return err
	}); err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(res.Events) != len(cases) {
		t.Fatalf("event count = %d, want %d", len(res.Events), len(cases))
	}
	seen := map[string]bool{}
	for _, e := range res.Events {
		if e.Kind.IsZero() {
			t.Fatalf("read returned zero Kind for KindRaw=%q", e.KindRaw)
		}
		if e.Kind.String() != e.KindRaw {
			t.Fatalf("Kind.String()=%q != KindRaw=%q", e.Kind.String(), e.KindRaw)
		}
		seen[e.KindRaw] = true
	}
	for _, k := range cases {
		if !seen[k.String()] {
			t.Fatalf("did not see wire form %q in the read result", k.String())
		}
	}
}

func TestSQLiteEvents_AppendRefusesZeroKind(t *testing.T) {
	t.Parallel()
	d := openSQLite(t)
	ctx := context.Background()
	store := d.Tables()
	err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.Events().Append(ctx, persistence.EventAppendInput{}, tx)
	})
	if err == nil {
		t.Fatal("Append with zero Kind did not error")
	}
}

func TestSQLiteEvents_UnmarshalRejectsCorruptKind(t *testing.T) {
	t.Parallel()
	d := openSQLite(t)
	ctx := context.Background()
	store := d.Tables()
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.Events().Append(ctx,
			persistence.EventAppendInput{Kind: events.KindWorkStarted()}, tx)
	}); err != nil {
		t.Fatalf("Append legitimate: %v", err)
	}
	db, ok := sqlitepersist.DBFromDatabaseForTest(d)
	if !ok {
		t.Fatal("DBFromDatabaseForTest: not a sqlite database")
	}
	if _, err := db.ExecContext(ctx,
		`UPDATE rimsky_events SET kind = 'totally_made_up_kind'`); err != nil {
		t.Fatalf("raw SQL update: %v", err)
	}
	err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		_, err := store.Events().List(ctx,
			persistence.EventListFilter{}, persistence.ListPagination{Limit: 5}, tx)
		return err
	})
	if err == nil {
		t.Fatal("List with corrupt kind did not error")
	}
	if !errors.Is(err, events.ErrUnknownKind) {
		t.Fatalf("List error = %v, want ErrUnknownKind", err)
	}
}

func TestSQLiteEvents_LastTerminalByNodesRejectsCorruptPayload(t *testing.T) {
	t.Parallel()
	d := openSQLite(t)
	ctx := context.Background()
	store := d.Tables()

	templateHash := "sha256-" + uuid.NewString()
	instanceID := shared.UUID(uuid.New())
	nodeID := shared.UUID(uuid.New())
	tmpl := spec.TemplateSpec{
		Name: "events-last-terminal-fixture", Version: "1",
		Nodes: []spec.TemplateNodeDef{{Type: "fixture-node-type", Executor: "test-executor"}},
	}
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := store.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID: templateHash, Spec: tmpl, State: persistence.TemplateStateRegistered, Source: "direct",
		}, tx); err != nil {
			return err
		}
		if _, err := store.Instances().Create(ctx, persistence.InstanceCreateInput{TargetRoutingIdentity: "test-daemon",
			ID: instanceID, TemplateHash: templateHash,
		}, tx); err != nil {
			return err
		}
		if _, err := store.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: nodeID, InstanceID: instanceID, NodeType: "fixture-node-type", Executor: "test-executor",
		}, tx); err != nil {
			return err
		}
		return store.Events().Append(ctx,
			persistence.EventAppendInput{Kind: events.KindWorkCompleted(), NodeID: &nodeID}, tx)
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	db, ok := sqlitepersist.DBFromDatabaseForTest(d)
	if !ok {
		t.Fatal("DBFromDatabaseForTest: not a sqlite database")
	}
	if _, err := db.ExecContext(ctx,
		`UPDATE rimsky_events SET payload = '"not an object"' WHERE node_id = ?`, nodeID.String()); err != nil {
		t.Fatalf("raw SQL update: %v", err)
	}
	err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		_, err := store.Events().LastTerminalByNodes(ctx, []shared.UUID{nodeID}, tx)
		return err
	})
	if err == nil {
		t.Fatal("LastTerminalByNodes with corrupt payload did not error")
	}
}
