// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package runtime

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/lifecycle"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks/storetest"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

func serviceDeliveryEntries(t *testing.T, tables persistence.Tables) []persistence.EventRow {
	t.Helper()
	ctx := context.Background()
	var out []persistence.EventRow
	require.NoError(t, tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		res, err := tables.Events().List(ctx, persistence.EventListFilter{
			KindIn: []string{
				events.KindServiceDeliveryStalled().String(),
				events.KindServiceDeliveryRecovered().String(),
			},
		}, persistence.ListPagination{Limit: 100}, tx)
		out = res.Events
		return err
	}))
	return out
}

func entryKinds(rows []persistence.EventRow) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.KindRaw)
	}
	return out
}

func oldestFirst(rows []persistence.EventRow) []persistence.EventRow {
	out := make([]persistence.EventRow, len(rows))
	for i, r := range rows {
		out[len(rows)-1-i] = r
	}
	return out
}

// @decision: service-delivery-stall-signal
func TestLifecycleDrain_ADeadServiceStallsOnceAndRecoversOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	const stallAfter = 10 * time.Minute
	f := newLifecycleDrainFixture(t, time.Second, stallAfter)

	alphaDown := true
	f.alpha.ErrorFunc = func(string, claimproducer.ClaimID) error {
		if alphaDown {
			return errors.New("alpha is down")
		}
		return nil
	}
	f.stageTemplateEvent(t, lifecycle.EventTemplateDeployed, "sha256-stalling", "alpha")

	f.drain.DrainOnce(ctx)
	require.Empty(t, entryKinds(serviceDeliveryEntries(t, f.tables)),
		"a failed delivery younger than the threshold is not a stall")

	f.clock.Advance(stallAfter + time.Minute)
	f.drain.DrainOnce(ctx)
	entries := oldestFirst(serviceDeliveryEntries(t, f.tables))
	require.Equal(t, []string{events.KindServiceDeliveryStalled().String()}, entryKinds(entries),
		"the drain writes the stall entry on the pass where the oldest pending row crosses the threshold")
	stalled := entries[0].Payload.Map()
	require.Equal(t, "alpha", stalled["service"])
	require.Equal(t, string(persistence.ServiceDeliveryOutboxLifecycle), stalled["outbox"])

	f.clock.Advance(stallAfter + time.Minute)
	f.drain.DrainOnce(ctx)
	require.Len(t, serviceDeliveryEntries(t, f.tables), 1,
		"a service that is still stalled writes no further entry, because the signal is the edge")

	alphaDown = false
	f.clock.Advance(stallAfter + time.Minute)
	f.drain.DrainOnce(ctx)
	require.Equal(t,
		[]string{
			events.KindServiceDeliveryStalled().String(),
			events.KindServiceDeliveryRecovered().String(),
		},
		entryKinds(oldestFirst(serviceDeliveryEntries(t, f.tables))),
		"the pass that leaves the service nothing past the threshold writes the recovery entry")

	f.clock.Advance(stallAfter + time.Minute)
	f.drain.DrainOnce(ctx)
	require.Len(t, serviceDeliveryEntries(t, f.tables), 2,
		"a service with nothing owed writes no further entry")
}

// @decision: service-delivery-stall-signal
func TestLifecycleDrain_AServiceStaysStalledWhileOneStreamBlocksAndAnotherDelivers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	const stallAfter = 10 * time.Minute
	f := newLifecycleDrainFixture(t, time.Second, stallAfter)

	f.alpha.ErrorFunc = func(verb string, _ claimproducer.ClaimID) error {
		if verb == "on_template_deployed" {
			return errors.New("alpha refuses this template's deploy forever")
		}
		return nil
	}
	f.stageTemplateEvent(t, lifecycle.EventTemplateDeployed, "sha256-blocked", "alpha")
	f.drain.DrainOnce(ctx)

	f.clock.Advance(stallAfter + time.Minute)
	f.stageTemplateEvent(t, lifecycle.EventTemplateRegistered, "sha256-flowing", "alpha")
	f.drain.DrainOnce(ctx)
	require.Equal(t, []string{events.KindServiceDeliveryStalled().String()},
		entryKinds(oldestFirst(serviceDeliveryEntries(t, f.tables))),
		"the blocked stream stalls the service even while another of its streams delivers")

	for i := 0; i < 3; i++ {
		f.clock.Advance(stallAfter + time.Minute)
		f.stageTemplateEvent(t, lifecycle.EventTemplateRegistered, "sha256-flowing", "alpha")
		f.drain.DrainOnce(ctx)
	}
	require.Empty(t, f.pendingRows(t, "sha256-flowing"), "the flowing stream delivered every row staged for it")
	require.NotEmpty(t, f.pendingRows(t, "sha256-blocked"), "the blocked stream is still owed")
	require.Equal(t, []string{events.KindServiceDeliveryStalled().String()},
		entryKinds(oldestFirst(serviceDeliveryEntries(t, f.tables))),
		"a delivery on one stream is not a recovery while another stream of the same service stands past the threshold")
}

// @decision: service-delivery-stall-signal
func TestLifecycleDrain_AServiceStallsAgainAfterTheSweepTakesItsRows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	const stallAfter = 10 * time.Minute
	f := newLifecycleDrainFixture(t, time.Second, stallAfter)

	f.alpha.ErrorFunc = func(string, claimproducer.ClaimID) error {
		return errors.New("alpha is down")
	}
	f.stageTemplateEvent(t, lifecycle.EventTemplateDeployed, "sha256-first", "alpha")
	f.clock.Advance(stallAfter + time.Minute)
	f.drain.DrainOnce(ctx)
	require.Equal(t, []string{events.KindServiceDeliveryStalled().String()},
		entryKinds(oldestFirst(serviceDeliveryEntries(t, f.tables))))

	swept, err := SweepLifecycleOutbox(ctx, f.tables,
		RetentionConfig{LifecycleOutboxTrailing: time.Minute}, f.clock.Now(), shared.SilentLogger{})
	require.NoError(t, err)
	require.Positive(t, swept, "the retention window takes the service's only pending row")

	f.drain.DrainOnce(ctx)
	require.Equal(t,
		[]string{
			events.KindServiceDeliveryStalled().String(),
			events.KindServiceDeliveryRecovered().String(),
		},
		entryKinds(oldestFirst(serviceDeliveryEntries(t, f.tables))),
		"a service owed nothing is no longer stalled, whatever took its rows")

	f.stageTemplateEvent(t, lifecycle.EventTemplateDeployed, "sha256-second", "alpha")
	f.clock.Advance(stallAfter + time.Minute)
	f.drain.DrainOnce(ctx)
	require.Equal(t,
		[]string{
			events.KindServiceDeliveryStalled().String(),
			events.KindServiceDeliveryRecovered().String(),
			events.KindServiceDeliveryStalled().String(),
		},
		entryKinds(oldestFirst(serviceDeliveryEntries(t, f.tables))),
		"the next stall writes its own entry. A marker the sweep stranded would have silenced it")
}

// @decision: service-delivery-stall-signal
func TestProducerVerbDispatcher_ADeadProducerStallsOnceAndRecoversOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	const stallAfter = 10 * time.Minute
	d := openSQLiteForOutbox(t)
	tables := d.Tables()
	outbox := outboxOfTables(t, tables)
	start := time.Unix(1000, 0).UTC()
	clock := shared.NewControllableClock(start)

	fake := storetest.NewFake("store-a", claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
	})
	disp := newOutboxDispatcherWithStallAfter(t, tables, fake, clock, stallAfter)

	producerDown := true
	fake.ErrorFunc = func(string, claimproducer.ClaimID) error {
		if producerDown {
			return errors.New("producer unreachable")
		}
		return nil
	}
	enqueueOutboxVerb(ctx, t, outbox, shared.UUID(uuid.New()), "store-a",
		persistence.ProducerVerbCommit, []byte(`"s1"`), start)

	_, err := disp.DispatchOnce(ctx)
	require.NoError(t, err)
	require.Empty(t, entryKinds(serviceDeliveryEntries(t, tables)),
		"a failed verb younger than the threshold is not a stall")

	clock.Advance(stallAfter + time.Minute)
	_, err = disp.DispatchOnce(ctx)
	require.NoError(t, err)
	entries := oldestFirst(serviceDeliveryEntries(t, tables))
	require.Equal(t, []string{events.KindServiceDeliveryStalled().String()}, entryKinds(entries),
		"the producer-verb outbox writes the same stall entry as the lifecycle outbox")
	stalled := entries[0].Payload.Map()
	require.Equal(t, "store-a", stalled["service"])
	require.Equal(t, string(persistence.ServiceDeliveryOutboxProducerVerb), stalled["outbox"],
		"the entry names which outbox is stalled, so one service stalling on both is two edges")

	clock.Advance(stallAfter + time.Minute)
	_, err = disp.DispatchOnce(ctx)
	require.NoError(t, err)
	require.Len(t, serviceDeliveryEntries(t, tables), 1,
		"a producer that is still stalled writes no further entry")

	producerDown = false
	clock.Advance(stallAfter + time.Minute)
	_, err = disp.DispatchOnce(ctx)
	require.NoError(t, err)
	require.Equal(t,
		[]string{
			events.KindServiceDeliveryStalled().String(),
			events.KindServiceDeliveryRecovered().String(),
		},
		entryKinds(oldestFirst(serviceDeliveryEntries(t, tables))),
		"the pass that leaves the producer nothing past the threshold writes the recovery entry")
}

// @decision: service-delivery-stall-signal
func TestProducerVerbDispatcher_TheRetryBackoffNeverOutlastsTheStallThreshold(t *testing.T) {
	t.Parallel()
	d := openSQLiteForOutbox(t)
	fake := storetest.NewFake("store-a", claimproducer.Capabilities{})
	clock := shared.NewControllableClock(time.Unix(1000, 0).UTC())

	tight := newOutboxDispatcherWithStallAfter(t, d.Tables(), fake, clock, 5*time.Second)
	require.Equal(t, 5*time.Second, tight.MaxBackoff,
		"a deployment that declares a service stalled after 5s must retry it at least that often")

	loose := newOutboxDispatcherWithStallAfter(t, d.Tables(), fake, clock, time.Hour)
	require.Equal(t, defaultProducerVerbMaxBackoff, loose.MaxBackoff,
		"a threshold wider than the dispatcher's own ceiling leaves that ceiling alone")
}

// @decision: service-delivery-stall-signal
func TestProducerVerbDispatcher_AProducerStallsAgainAfterItsRowsLeaveTheOutbox(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	const stallAfter = 10 * time.Minute
	d := openSQLiteForOutbox(t)
	tables := d.Tables()
	outbox := outboxOfTables(t, tables)
	start := time.Unix(1000, 0).UTC()
	clock := shared.NewControllableClock(start)

	fake := storetest.NewFake("store-a", claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
	})
	fake.ErrorFunc = func(string, claimproducer.ClaimID) error { return errors.New("producer unreachable") }
	disp := newOutboxDispatcherWithStallAfter(t, tables, fake, clock, stallAfter)

	enqueueOutboxVerb(ctx, t, outbox, shared.UUID(uuid.New()), "store-a",
		persistence.ProducerVerbCommit, []byte(`"s1"`), start)
	clock.Advance(stallAfter + time.Minute)
	_, err := disp.DispatchOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{events.KindServiceDeliveryStalled().String()},
		entryKinds(oldestFirst(serviceDeliveryEntries(t, tables))))

	rows, err := outbox.ListAll(ctx, nil)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.NoError(t, tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return outbox.Delete(ctx, rows[0].Seq, tx)
	}))

	_, err = disp.DispatchOnce(ctx)
	require.NoError(t, err)
	require.Equal(t,
		[]string{
			events.KindServiceDeliveryStalled().String(),
			events.KindServiceDeliveryRecovered().String(),
		},
		entryKinds(oldestFirst(serviceDeliveryEntries(t, tables))),
		"a pass over an empty outbox still clears the marker of a producer that now owes nothing")

	enqueueOutboxVerb(ctx, t, outbox, shared.UUID(uuid.New()), "store-a",
		persistence.ProducerVerbCommit, []byte(`"s2"`), clock.Now())
	clock.Advance(stallAfter + time.Minute)
	_, err = disp.DispatchOnce(ctx)
	require.NoError(t, err)
	require.Equal(t,
		[]string{
			events.KindServiceDeliveryStalled().String(),
			events.KindServiceDeliveryRecovered().String(),
			events.KindServiceDeliveryStalled().String(),
		},
		entryKinds(oldestFirst(serviceDeliveryEntries(t, tables))),
		"the next stall writes its own entry. A marker stranded by an empty pass would have silenced it")
}
