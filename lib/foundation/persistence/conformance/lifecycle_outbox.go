// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @decision: lifecycle-subscriber-at-least-once-delivery

package conformance

import (
	"context"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

func testLifecycleOutboxDeliversInStagedOrder(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	store := d.Tables()
	kind := persistence.LifecycleIdempotencyScopeTemplate

	stage := func(peer, scope, event, payload string) {
		t.Helper()
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			return store.LifecycleOutbox().Stage(ctx, persistence.LifecycleOutboxRow{
				ClaimProducerName: peer,
				ScopeKind:         kind,
				ScopeID:           scope,
				Event:             event,
				Payload:           []byte(payload),
			}, tx)
		}); err != nil {
			t.Fatalf("stage %s/%s: %v", scope, event, err)
		}
	}
	listScope := func(scope string) []persistence.LifecycleOutboxRow {
		t.Helper()
		var rows []persistence.LifecycleOutboxRow
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			r, err := store.LifecycleOutbox().ListPendingForScope(ctx, kind, scope, tx)
			rows = r
			return err
		}); err != nil {
			t.Fatalf("ListPendingForScope(%s): %v", scope, err)
		}
		return rows
	}
	listHeads := func(limit int) []persistence.LifecycleOutboxRow {
		t.Helper()
		var rows []persistence.LifecycleOutboxRow
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			r, err := store.LifecycleOutbox().ListOldestPendingPerStream(ctx, limit, tx)
			rows = r
			return err
		}); err != nil {
			t.Fatalf("ListOldestPendingPerStream: %v", err)
		}
		return rows
	}
	events := func(rows []persistence.LifecycleOutboxRow) []string {
		out := make([]string, 0, len(rows))
		for _, r := range rows {
			out = append(out, r.Event)
		}
		return out
	}
	sameOrder := func(got []string, want ...string) bool {
		if len(got) != len(want) {
			return false
		}
		for i := range got {
			if got[i] != want[i] {
				return false
			}
		}
		return true
	}

	stage("peer-a", "tpl-1", "EventTemplateDeployed", `{"n":1}`)
	stage("peer-a", "tpl-1", "EventTemplateUndeployed", `{"n":2}`)
	stage("peer-a", "tpl-1", "EventTemplateDeployed", `{"n":3}`)

	restaged := listScope("tpl-1")
	if !sameOrder(events(restaged), "EventTemplateDeployed", "EventTemplateUndeployed", "EventTemplateDeployed") {
		t.Fatalf("a re-staged event took the position of its first staging: got %v, want deploy, undeploy, deploy",
			events(restaged))
	}
	if string(restaged[0].Payload) != `{"n":1}` || string(restaged[2].Payload) != `{"n":3}` {
		t.Fatalf("the two deploy stagings share one row: got payloads %q and %q",
			string(restaged[0].Payload), string(restaged[2].Payload))
	}
	if restaged[0].StagedAt.IsZero() {
		t.Fatal("StagedAt came back zero")
	}

	stage("peer-b", "tpl-2", "EventTemplateRegistered", `{"n":4}`)
	heads := listHeads(100)
	if len(heads) != 2 {
		t.Fatalf("ListOldestPendingPerStream returned %d rows, want one head per stream (2)", len(heads))
	}
	if heads[0].ClaimProducerName != "peer-a" || heads[0].ScopeID != "tpl-1" || heads[0].Event != "EventTemplateDeployed" {
		t.Fatalf("the first head is %+v, want peer-a's oldest tpl-1 row", heads[0])
	}
	if heads[1].ClaimProducerName != "peer-b" || heads[1].ScopeID != "tpl-2" {
		t.Fatalf("peer-b's stream is missing from the heads: got %+v", heads[1])
	}

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.LifecycleOutbox().DeleteBySeq(ctx, restaged[0].Seq, tx)
	}); err != nil {
		t.Fatalf("DeleteBySeq: %v", err)
	}
	afterDelete := listScope("tpl-1")
	if !sameOrder(events(afterDelete), "EventTemplateUndeployed", "EventTemplateDeployed") {
		t.Fatalf("DeleteBySeq removed the wrong row: scope holds %v", events(afterDelete))
	}

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.LifecycleOutbox().DeleteByScope(ctx, kind, "tpl-1", tx)
	}); err != nil {
		t.Fatalf("DeleteByScope: %v", err)
	}
	remaining := listHeads(100)
	if len(remaining) != 1 || remaining[0].ScopeID != "tpl-2" {
		t.Fatalf("after DeleteByScope the outbox holds %+v, want the tpl-2 row alone", remaining)
	}
}

// @decision: lifecycle-subscriber-at-least-once-delivery
func testLifecycleOutboxDropsRowsPastTheRetentionCutoff(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	store := d.Tables()
	kind := persistence.LifecycleIdempotencyScopeTemplate
	cutoff := time.Now().UTC().Add(-time.Hour)

	stage := func(scope string, stagedAt time.Time) {
		t.Helper()
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			return store.LifecycleOutbox().Stage(ctx, persistence.LifecycleOutboxRow{
				ClaimProducerName: "peer-a",
				ScopeKind:         kind,
				ScopeID:           scope,
				Event:             "EventTemplateDeployed",
				Payload:           []byte(`{}`),
				StagedAt:          stagedAt,
			}, tx)
		}); err != nil {
			t.Fatalf("stage %s: %v", scope, err)
		}
	}

	stage("tpl-old", cutoff.Add(-time.Minute))
	stage("tpl-new", cutoff.Add(time.Minute))

	var deleted int64
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		n, err := store.LifecycleOutbox().DeleteOlderThan(ctx, cutoff, tx)
		deleted = n
		return err
	}); err != nil {
		t.Fatalf("DeleteOlderThan: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("DeleteOlderThan reported %d rows, want the one staged before the cutoff", deleted)
	}

	var heads []persistence.LifecycleOutboxRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.LifecycleOutbox().ListOldestPendingPerStream(ctx, 100, tx)
		heads = r
		return err
	}); err != nil {
		t.Fatalf("ListOldestPendingPerStream: %v", err)
	}
	if len(heads) != 1 || heads[0].ScopeID != "tpl-new" {
		t.Fatalf("after the sweep the outbox holds %+v, want the row staged after the cutoff alone", heads)
	}
}
