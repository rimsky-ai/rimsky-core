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
	kind := persistence.LifecycleScopeTemplate

	stage := func(service, scope, event, payload string) {
		t.Helper()
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			return store.LifecycleOutbox().Stage(ctx, persistence.LifecycleOutboxRow{
				ClaimProducerName: service,
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
			r, err := store.LifecycleOutbox().ListOldestPendingPerStream(ctx, limit, time.Now().UTC().Add(time.Minute), tx)
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

	stage("service-a", "tpl-1", "EventTemplateDeployed", `{"n":1}`)
	stage("service-a", "tpl-1", "EventTemplateUndeployed", `{"n":2}`)
	stage("service-a", "tpl-1", "EventTemplateDeployed", `{"n":3}`)

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

	stage("service-b", "tpl-2", "EventTemplateRegistered", `{"n":4}`)
	heads := listHeads(100)
	if len(heads) != 2 {
		t.Fatalf("ListOldestPendingPerStream returned %d rows, want one head per stream (2)", len(heads))
	}
	if heads[0].ClaimProducerName != "service-a" || heads[0].ScopeID != "tpl-1" || heads[0].Event != "EventTemplateDeployed" {
		t.Fatalf("the first head is %+v, want service-a's oldest tpl-1 row", heads[0])
	}
	if heads[1].ClaimProducerName != "service-b" || heads[1].ScopeID != "tpl-2" {
		t.Fatalf("service-b's stream is missing from the heads: got %+v", heads[1])
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
}

// @decision: lifecycle-subscriber-at-least-once-delivery
func testLifecycleOutboxDropsRowsPastTheRetentionCutoff(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	store := d.Tables()
	kind := persistence.LifecycleScopeTemplate
	cutoff := time.Now().UTC().Add(-time.Hour)

	stage := func(scope string, stagedAt time.Time) {
		t.Helper()
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			return store.LifecycleOutbox().Stage(ctx, persistence.LifecycleOutboxRow{
				ClaimProducerName: "service-a",
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
		r, err := store.LifecycleOutbox().ListOldestPendingPerStream(ctx, 100, time.Now().UTC().Add(time.Minute), tx)
		heads = r
		return err
	}); err != nil {
		t.Fatalf("ListOldestPendingPerStream: %v", err)
	}
	if len(heads) != 1 || heads[0].ScopeID != "tpl-new" {
		t.Fatalf("after the sweep the outbox holds %+v, want the row staged after the cutoff alone", heads)
	}
}

// @decision: lifecycle-subscriber-at-least-once-delivery
// @decision: service-delivery-stall-signal
func testLifecycleOutboxCarriesItsDeliveryFailureState(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	store := d.Tables()
	kind := persistence.LifecycleScopeTemplate
	staged := time.Now().UTC().Truncate(time.Millisecond)
	nextAttempt := staged.Add(90 * time.Second)

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.LifecycleOutbox().Stage(ctx, persistence.LifecycleOutboxRow{
			ClaimProducerName: "service-a",
			ScopeKind:         kind,
			ScopeID:           "tpl-attempts",
			Event:             "EventTemplateDeployed",
			Payload:           []byte(`{}`),
			StagedAt:          staged,
		}, tx)
	}); err != nil {
		t.Fatalf("stage: %v", err)
	}

	head := func() persistence.LifecycleOutboxRow {
		t.Helper()
		var rows []persistence.LifecycleOutboxRow
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			r, err := store.LifecycleOutbox().ListPendingForScope(ctx, kind, "tpl-attempts", tx)
			rows = r
			return err
		}); err != nil {
			t.Fatalf("ListPendingForScope: %v", err)
		}
		if len(rows) != 1 {
			t.Fatalf("ListPendingForScope returned %d rows, want 1", len(rows))
		}
		return rows[0]
	}

	fresh := head()
	if fresh.AttemptCount != 0 || fresh.LastError != "" {
		t.Fatalf("a freshly staged row carries %d attempts and error %q, want none", fresh.AttemptCount, fresh.LastError)
	}
	if !fresh.NextAttemptAt.Equal(staged) {
		t.Fatalf("a freshly staged row is due at %s, want its staging time %s", fresh.NextAttemptAt, staged)
	}

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.LifecycleOutbox().RecordAttempt(ctx, fresh.Seq, nextAttempt, "subscriber unreachable", tx)
	}); err != nil {
		t.Fatalf("RecordAttempt: %v", err)
	}
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.LifecycleOutbox().RecordAttempt(ctx, fresh.Seq, nextAttempt, "subscriber unreachable again", tx)
	}); err != nil {
		t.Fatalf("RecordAttempt (second): %v", err)
	}

	after := head()
	if after.AttemptCount != 2 {
		t.Fatalf("RecordAttempt left %d attempts on the row, want 2", after.AttemptCount)
	}
	if !after.NextAttemptAt.Equal(nextAttempt) {
		t.Fatalf("RecordAttempt left next attempt %s, want %s", after.NextAttemptAt, nextAttempt)
	}
	if after.LastError != "subscriber unreachable again" {
		t.Fatalf("RecordAttempt left last error %q, want the latest attempt's", after.LastError)
	}

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.LifecycleOutbox().Stage(ctx, persistence.LifecycleOutboxRow{
			ClaimProducerName: "service-a",
			ScopeKind:         kind,
			ScopeID:           "tpl-attempts",
			Event:             "EventTemplateUndeployed",
			Payload:           []byte(`{}`),
			StagedAt:          staged.Add(time.Second),
		}, tx)
	}); err != nil {
		t.Fatalf("stage the row behind the backed-off head: %v", err)
	}

	dueHeads := func(dueAt time.Time) []persistence.LifecycleOutboxRow {
		t.Helper()
		var rows []persistence.LifecycleOutboxRow
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			r, err := store.LifecycleOutbox().ListOldestPendingPerStream(ctx, 100, dueAt, tx)
			rows = r
			return err
		}); err != nil {
			t.Fatalf("ListOldestPendingPerStream: %v", err)
		}
		return rows
	}
	if heads := dueHeads(nextAttempt.Add(-time.Second)); len(heads) != 0 {
		t.Fatalf("a head waiting on its next attempt withholds its whole stream, but %+v came back as due — "+
			"the due-time predicate must filter the stream's head, not select the oldest due row behind it", heads)
	}
	if heads := dueHeads(nextAttempt); len(heads) != 1 || heads[0].Seq != fresh.Seq {
		t.Fatalf("the head is owed again at its next-attempt time, got %+v", heads)
	}
}

// @concept: lifecycle-subscriber
func testLifecycleOutboxListsWhatOneServiceIsOwed(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	store := d.Tables()

	stage := func(service, scope string, kind persistence.LifecycleScopeKind) {
		t.Helper()
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			return store.LifecycleOutbox().Stage(ctx, persistence.LifecycleOutboxRow{
				ClaimProducerName: service,
				ScopeKind:         kind,
				ScopeID:           scope,
				Event:             "EventTemplateDeployed",
				Payload:           []byte(`{}`),
			}, tx)
		}); err != nil {
			t.Fatalf("stage %s/%s: %v", service, scope, err)
		}
	}
	owed := func(service string, limit int) []persistence.LifecycleOutboxRow {
		t.Helper()
		var rows []persistence.LifecycleOutboxRow
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			r, err := store.LifecycleOutbox().ListPendingForService(ctx, service, limit, tx)
			rows = r
			return err
		}); err != nil {
			t.Fatalf("ListPendingForService(%s): %v", service, err)
		}
		return rows
	}
	// @decision: service-delivery-stall-signal
	summary := func() []persistence.ServiceOutboxPending {
		t.Helper()
		var out []persistence.ServiceOutboxPending
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			r, err := store.LifecycleOutbox().PendingSummaryByService(ctx, tx)
			out = r
			return err
		}); err != nil {
			t.Fatalf("PendingSummaryByService: %v", err)
		}
		return out
	}

	if rows := owed("no-such-service", 0); len(rows) != 0 {
		t.Fatalf("a service nothing was staged for is owed %d rows, want none", len(rows))
	}

	stage("service-a", "tpl-1", persistence.LifecycleScopeTemplate)
	stage("service-a", "inst-1", persistence.LifecycleScopeInstance)
	stage("service-b", "tpl-1", persistence.LifecycleScopeTemplate)

	rows := owed("service-a", 0)
	if len(rows) != 2 {
		t.Fatalf("service-a is owed %d rows, want the two staged for it across both scopes", len(rows))
	}
	if rows[0].Seq >= rows[1].Seq {
		t.Fatalf("what a service is owed comes back oldest first, got seqs %d then %d", rows[0].Seq, rows[1].Seq)
	}
	for _, r := range rows {
		if r.ClaimProducerName != "service-a" {
			t.Fatalf("another service's row came back for service-a: %+v", r)
		}
	}

	// @decision: service-delivery-stall-signal
	if page := owed("service-a", 1); len(page) != 1 || page[0].Seq != rows[0].Seq {
		t.Fatalf("a limited read of what a service is owed returns the oldest row alone, got %+v", page)
	}

	// @decision: service-delivery-stall-signal
	byService := map[string]persistence.ServiceOutboxPending{}
	for _, e := range summary() {
		byService[e.Service] = e
	}
	a, ok := byService["service-a"]
	if !ok {
		t.Fatalf("the pending summary names no service-a, got %+v", byService)
	}
	if a.PendingCount != 2 {
		t.Fatalf("service-a's pending count is %d, want 2", a.PendingCount)
	}
	if !a.OldestPendingAt.Equal(rows[0].StagedAt) {
		t.Fatalf("service-a's oldest pending time is %s, want the staged time of its oldest row %s",
			a.OldestPendingAt, rows[0].StagedAt)
	}
	b, ok := byService["service-b"]
	if !ok || b.PendingCount != 1 {
		t.Fatalf("service-b's summary is %+v, want one pending row", b)
	}
}

// @decision: service-delivery-stall-signal
func testServiceDeliveryStallMarkerIsAnEdgePerServiceAndOutbox(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	store := d.Tables()
	since := time.Now().UTC().Truncate(time.Second)

	mark := func(service string, outbox persistence.ServiceDeliveryOutbox) bool {
		t.Helper()
		var fresh bool
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			f, err := store.ServiceDeliveryStalls().MarkStalled(ctx, service, outbox, since, tx)
			fresh = f
			return err
		}); err != nil {
			t.Fatalf("MarkStalled(%s, %s): %v", service, outbox, err)
		}
		return fresh
	}
	clear := func(service string, outbox persistence.ServiceDeliveryOutbox) bool {
		t.Helper()
		var wasStalled bool
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			w, err := store.ServiceDeliveryStalls().ClearStalled(ctx, service, outbox, tx)
			wasStalled = w
			return err
		}); err != nil {
			t.Fatalf("ClearStalled(%s, %s): %v", service, outbox, err)
		}
		return wasStalled
	}

	stalledOn := func(outbox persistence.ServiceDeliveryOutbox) []string {
		t.Helper()
		var out []string
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			s, err := store.ServiceDeliveryStalls().ListStalled(ctx, outbox, tx)
			out = s
			return err
		}); err != nil {
			t.Fatalf("ListStalled(%s): %v", outbox, err)
		}
		return out
	}

	if got := stalledOn(persistence.ServiceDeliveryOutboxLifecycle); len(got) != 0 {
		t.Fatalf("nothing is stalled yet, got %v", got)
	}
	if !mark("service-a", persistence.ServiceDeliveryOutboxLifecycle) {
		t.Fatal("the first mark of a service that was not stalled must report the edge")
	}
	if got := stalledOn(persistence.ServiceDeliveryOutboxLifecycle); len(got) != 1 || got[0] != "service-a" {
		t.Fatalf("the lifecycle outbox lists %v as stalled, want service-a alone", got)
	}
	if got := stalledOn(persistence.ServiceDeliveryOutboxProducerVerb); len(got) != 0 {
		t.Fatalf("the other outbox lists %v as stalled, want nothing: a marker belongs to one outbox", got)
	}
	if mark("service-a", persistence.ServiceDeliveryOutboxLifecycle) {
		t.Fatal("a second mark of a service already stalled must report no edge, or every pass writes an entry")
	}
	if !mark("service-a", persistence.ServiceDeliveryOutboxProducerVerb) {
		t.Fatal("the same service stalling on the other outbox is its own edge")
	}
	if !clear("service-a", persistence.ServiceDeliveryOutboxLifecycle) {
		t.Fatal("clearing a stalled service must report the recovery edge")
	}
	if clear("service-a", persistence.ServiceDeliveryOutboxLifecycle) {
		t.Fatal("clearing a service that was not stalled must report no edge")
	}
	if !clear("service-a", persistence.ServiceDeliveryOutboxProducerVerb) {
		t.Fatal("the other outbox's marker stands until it is cleared for that outbox")
	}
}
