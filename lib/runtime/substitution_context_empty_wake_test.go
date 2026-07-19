// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	_ "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

func TestBuildAttributeDeps_SkipsEmptyWakeMessages(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	d, err := persistence.Open(ctx, persistence.Config{
		Driver: "sqlite",
		SQLite: &persistence.SQLiteConfig{Path: filepath.Join(t.TempDir(), "state.db")},
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := d.Migrate(ctx, shared.SilentLogger{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	tables := d.Tables()

	templateHash := "sha256-" + uuid.NewString()
	instanceID := shared.UUID(uuid.New())
	mainScopeID := shared.UUID(uuid.New())
	emptyWakeMsgID := shared.UUID(uuid.New())
	namedMsgID := shared.UUID(uuid.New())

	tmpl := spec.TemplateSpec{
		Name: "build-attribute-deps-empty-wake-fixture", Version: "1",
		Nodes: []spec.TemplateNodeDef{{Type: "receiver", Executor: "test-executor"}},
	}

	now := time.Now()
	var frameID shared.UUID
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := tables.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID: templateHash, Spec: tmpl, State: persistence.TemplateStateRegistered, Source: "direct",
		}, tx); err != nil {
			return err
		}
		if _, err := tables.Instances().Create(ctx, persistence.InstanceCreateInput{
			ID: instanceID, TemplateHash: templateHash,
		}, tx); err != nil {
			return err
		}
		if err := tables.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID: mainScopeID, GraphName: spec.MainGraphName, InstanceID: instanceID,
		}); err != nil {
			return err
		}
		if err := tables.Messages().Insert(ctx, tx, persistence.EnqueueMessageRequest{
			ID: emptyWakeMsgID, InstanceID: instanceID, Type: "", Sender: "operator", SenderKind: "operator",
		}); err != nil {
			return err
		}
		fid, err := tables.Frames().InsertRunningFrame(ctx, instanceID, emptyWakeMsgID, mainScopeID, tx)
		if err != nil {
			return err
		}
		frameID = fid
		if _, err := tables.Messages().MarkDelivered(ctx, tx, emptyWakeMsgID, frameID, now); err != nil {
			return err
		}
		if err := tables.Messages().Insert(ctx, tx, persistence.EnqueueMessageRequest{
			ID: namedMsgID, InstanceID: instanceID, Type: "named-event", Sender: "operator", SenderKind: "operator",
			Payload: json.RawMessage(`{"k":"v"}`),
		}); err != nil {
			return err
		}
		if _, err := tables.Messages().MarkDelivered(ctx, tx, namedMsgID, frameID, now); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatalf("seed fixture: %v", err)
	}

	args := RunArgs{Persist: tables, Logger: shared.SilentLogger{}}

	var deps map[string]json.RawMessage
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		d, err := BuildAttributeDeps(ctx, tx, args, shared.UUID{}, frameID)
		deps = d
		return err
	}); err != nil {
		t.Fatalf("BuildAttributeDeps: %v", err)
	}

	if _, present := deps[""]; present {
		t.Fatalf("BuildAttributeDeps included the empty-wake message under key %q; "+
			"IsEmptyWake() must filter empty-type delivered messages out of the substitution deps map: %+v",
			"", deps)
	}
	namedRaw, present := deps["named-event"]
	if !present {
		t.Fatalf("BuildAttributeDeps dropped the named message %q; want it present: %+v", "named-event", deps)
	}
	if string(namedRaw) != `{"k":"v"}` {
		t.Fatalf("BuildAttributeDeps deps[%q] = %s, want %s", "named-event", string(namedRaw), `{"k":"v"}`)
	}
	if len(deps) != 1 {
		t.Fatalf("BuildAttributeDeps deps has %d entries, want exactly 1 (the named message; empty-wake excluded): %+v",
			len(deps), deps)
	}
}
