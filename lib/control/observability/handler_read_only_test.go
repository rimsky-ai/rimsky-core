// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package observability_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/control/observability"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	sqlitedriver "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func dbContentSnapshot(t *testing.T, db *sql.DB) string {
	t.Helper()
	tableRows, err := db.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatalf("dbContentSnapshot: list tables: %v", err)
	}
	var tables []string
	for tableRows.Next() {
		var name string
		if err := tableRows.Scan(&name); err != nil {
			t.Fatalf("dbContentSnapshot: scan table name: %v", err)
		}
		tables = append(tables, name)
	}
	if err := tableRows.Err(); err != nil {
		t.Fatalf("dbContentSnapshot: %v", err)
	}
	_ = tableRows.Close()
	sort.Strings(tables)

	var sb strings.Builder
	for _, tbl := range tables {
		rows, err := db.Query(fmt.Sprintf(`SELECT * FROM %q ORDER BY rowid`, tbl))
		if err != nil {
			t.Fatalf("dbContentSnapshot: query %s: %v", tbl, err)
		}
		cols, err := rows.Columns()
		if err != nil {
			t.Fatalf("dbContentSnapshot: columns %s: %v", tbl, err)
		}
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatalf("dbContentSnapshot: scan %s: %v", tbl, err)
			}
			fmt.Fprintf(&sb, "%s|%v\n", tbl, vals)
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("dbContentSnapshot: %v", err)
		}
		_ = rows.Close()
	}
	return sb.String()
}

func TestObservabilityHandlers_NeverMutateState(t *testing.T) {
	d := newSQLiteDriver(t)
	store := d.Tables()
	ctx := context.Background()

	fix := seedInstanceWithNode(t, ctx, store, singleNodeTemplateSpec("worker"))
	frameID, _ := seedFrame(t, ctx, store, fix.InstanceID, fix.MainRunScopeID, "fixture/read-only")
	runID := seedPendingRun(t, ctx, d, fix.NodeID, frameID, fix.MainRunScopeID)

	producer := "read-only-fixture-store"
	claimHandleID := shared.UUID(uuid.New())
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := store.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                 claimHandleID,
			LockKind:           persistence.LockKindScope,
			ProducerName:       &producer,
			ClaimScopeData:     []byte(`"/read-only"`),
			HolderSupervisorID: "sup-read-only",
			HolderNodeID:       fix.NodeID,
			ExpiresAt:          time.Now().Add(time.Hour),
		}, tx); err != nil {
			return err
		}
		return store.ClaimHolders().Insert(ctx, persistence.ClaimHolderInsertInput{
			ID:              shared.UUID(uuid.New()),
			ClaimHandleID:   claimHandleID,
			HolderNodeRunID: runID,
		}, tx)
	}); err != nil {
		t.Fatalf("seed claim handle: %v", err)
	}
	seedEventAt(t, ctx, store, events.SignalKind("terminal/success"), time.Now().UTC())

	disc := observability.NewDiscovery(&nopProber{})
	deps := observability.Deps{
		Tables:         store,
		Queue:          d.Queue(),
		Driver:         d,
		Discovery:      disc,
		ClaimProducers: []observability.PeerSpec{{Name: producer, Endpoint: "store:9000"}},
		Executors:      []observability.PeerSpec{{Name: "worker", Endpoint: "exec:9000"}},
	}
	r := newRouter(t, deps)
	rawDB := sqlitedriver.DBFromDatabase(d)

	missingID := "00000000-0000-0000-0000-000000000000"
	paths := []string{
		"/v1/observability/claim-producers",
		"/v1/observability/claim-producers/" + producer,
		"/v1/observability/claim-producers/missing-store",
		"/v1/observability/executors",
		"/v1/observability/executors/worker",
		"/v1/observability/executors/missing-executor",
		"/v1/observability/templates",
		"/v1/observability/templates/" + fix.TemplateHash,
		"/v1/observability/templates/missing-hash",
		"/v1/observability/instances",
		"/v1/observability/instances/" + fix.InstanceID.String(),
		"/v1/observability/instances/" + missingID,
		"/v1/observability/frames",
		"/v1/observability/frames/" + frameID.String(),
		"/v1/observability/frames/" + missingID,
		"/v1/observability/nodes/" + fix.InstanceID.String() + "/worker",
		"/v1/observability/nodes/" + fix.InstanceID.String() + "/missing-type",
		"/v1/observability/node-runs",
		"/v1/observability/node-runs/" + runID.String(),
		"/v1/observability/node-runs/" + missingID,
		"/v1/observability/claim-handles",
		"/v1/observability/claim-handles/" + claimHandleID.String(),
		"/v1/observability/claim-handles/" + missingID,
		"/v1/observability/events",
		"/v1/observability/system/health",
		"/v1/observability/system/summary",
	}

	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			before := dbContentSnapshot(t, rawDB)
			req := httptest.NewRequest("GET", path, nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			after := dbContentSnapshot(t, rawDB)
			if before != after {
				t.Fatalf("GET %s mutated the database (status=%d); the observability surface must be read-only.\nbefore:\n%s\nafter:\n%s",
					path, w.Code, before, after)
			}
		})
	}
}
