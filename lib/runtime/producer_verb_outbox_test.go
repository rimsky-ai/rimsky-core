// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks/storetest"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	_ "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

func openSQLiteForOutbox(t *testing.T) persistence.Database {
	t.Helper()
	ctx := context.Background()
	d, err := persistence.Open(ctx, persistence.Config{
		Driver: "sqlite",
		SQLite: &persistence.SQLiteConfig{Path: filepath.Join(t.TempDir(), "outbox.db")},
	})
	require.NoError(t, err)
	require.NoError(t, d.Migrate(ctx, shared.SilentLogger{}))
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func outboxOfTables(t *testing.T, tables persistence.Tables) persistence.ProducerVerbOutboxTable {
	t.Helper()
	p, ok := tables.(producerVerbOutboxProvider)
	require.True(t, ok, "Tables backend must provide the producer verb outbox")
	return p.ProducerVerbOutbox()
}

func enqueueOutboxVerb(
	ctx context.Context, t *testing.T, outbox persistence.ProducerVerbOutboxTable,
	claimID shared.UUID, producer string, verb persistence.ProducerVerb, scope []byte, at time.Time,
) {
	t.Helper()
	require.NoError(t, outbox.Enqueue(ctx, persistence.ProducerVerbOutboxInsertInput{
		ClaimHandleID:  claimID,
		ProducerName:   producer,
		Verb:           verb,
		ClaimScopeData: scope,
		Address:        []byte(`"addr"`),
		SupervisorID:   "sup-outbox",
		NextAttemptAt:  at,
		EnqueuedAt:     at,
	}, nil))
}

func newOutboxDispatcher(
	t *testing.T, tables persistence.Tables, fake *storetest.Fake, clock shared.Clock,
) *ProducerVerbDispatcher {
	t.Helper()
	reg := locks.NewRegistry()
	reg.Add(fake.Name(), fake)
	return NewProducerVerbDispatcher(
		outboxOfTables(t, tables), tables,
		reg, clock, shared.SilentLogger{})
}

func TestProducerVerbOutbox_EnqueueIsIdempotentPerClaimAndVerb(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := openSQLiteForOutbox(t)
	outbox := outboxOfTables(t, d.Tables())
	start := time.Unix(1000, 0).UTC()
	claimID := shared.UUID(uuid.New())
	enqueueOutboxVerb(ctx, t, outbox, claimID, "store-a", persistence.ProducerVerbCommit, []byte(`"s1"`), start)
	enqueueOutboxVerb(ctx, t, outbox, claimID, "store-a", persistence.ProducerVerbCommit, []byte(`"s1"`), start)
	rows, err := outbox.ListAll(ctx, nil)
	require.NoError(t, err)
	require.Len(t, rows, 1, "re-enqueue of the same (claim, verb) must be a no-op")
	require.Equal(t, persistence.ProducerVerbCommit, rows[0].Verb)
	require.Equal(t, "store-a", rows[0].ProducerName)
}

func TestProducerVerbDispatcher_PerScopeOrderingBarrierAndBackoff(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := openSQLiteForOutbox(t)
	tables := d.Tables()
	outbox := outboxOfTables(t, tables)
	start := time.Unix(1000, 0).UTC()
	clock := shared.NewControllableClock(start)

	fake := storetest.NewFake("store-a", claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
	})
	disp := newOutboxDispatcher(t, tables, fake, clock)

	claimA := shared.UUID(uuid.New())
	claimB := shared.UUID(uuid.New())
	claimC := shared.UUID(uuid.New())
	enqueueOutboxVerb(ctx, t, outbox, claimA, "store-a", persistence.ProducerVerbCommit, []byte(`"s1"`), start)
	enqueueOutboxVerb(ctx, t, outbox, claimB, "store-a", persistence.ProducerVerbAbandon, []byte(`"s1"`), start)
	enqueueOutboxVerb(ctx, t, outbox, claimC, "store-a", persistence.ProducerVerbCommit, []byte(`"s2"`), start)

	producerDown := true
	fake.ErrorFunc = func(verb string, claimID claimproducer.ClaimID) error {
		if producerDown && claimID == claimproducer.ClaimID(claimA.String()) {
			return errors.New("producer unreachable")
		}
		return nil
	}

	delivered, err := disp.DispatchOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, delivered, "only the unrelated scope may deliver while s1's head fails")
	require.Equal(t, 0, callCountFor(fake, claimB, "abandon"),
		"a failed head-of-line must block later verbs on the same scope")
	require.Equal(t, 1, callCountFor(fake, claimC, "commit"),
		"an unrelated scope must not be blocked by another scope's failure")

	rows, err := outbox.ListAll(ctx, nil)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.Equal(t, claimA, rows[0].ClaimHandleID)
	require.Equal(t, 1, rows[0].AttemptCount)
	require.Equal(t, start.Add(disp.BaseBackoff), rows[0].NextAttemptAt.UTC(),
		"retry timing must come from the injected clock plus backoff")
	require.NotEmpty(t, rows[0].LastError)

	producerDown = false
	delivered, err = disp.DispatchOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, delivered, "a backing-off row is not due until the clock advances")

	clock.Advance(disp.BaseBackoff)
	delivered, err = disp.DispatchOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, delivered, "once due, the scope's queue drains in order")

	seqA, seqB := -1, -1
	for _, c := range fake.Calls() {
		if c.ClaimID == claimproducer.ClaimID(claimA.String()) && c.Verb == "commit" {
			seqA = c.Sequence
		}
		if c.ClaimID == claimproducer.ClaimID(claimB.String()) && c.Verb == "abandon" {
			seqB = c.Sequence
		}
	}
	require.Greater(t, seqB, seqA, "per-scope delivery must preserve enqueue order")

	rows, err = outbox.ListAll(ctx, nil)
	require.NoError(t, err)
	require.Empty(t, rows, "delivered rows must be deleted")
}

func TestProducerVerbDispatcher_RedeliveryAfterCrashIsTolerated(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := openSQLiteForOutbox(t)
	tables := d.Tables()
	outbox := outboxOfTables(t, tables)
	start := time.Unix(2000, 0).UTC()
	clock := shared.NewControllableClock(start)
	fake := storetest.NewFake("store-a", claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
	})
	disp := newOutboxDispatcher(t, tables, fake, clock)

	claimID := shared.UUID(uuid.New())
	enqueueOutboxVerb(ctx, t, outbox, claimID, "store-a", persistence.ProducerVerbAbandon, []byte(`"s"`), start)
	_, err := disp.DispatchOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, callCountFor(fake, claimID, "abandon"))

	enqueueOutboxVerb(ctx, t, outbox, claimID, "store-a", persistence.ProducerVerbAbandon, []byte(`"s"`), start)
	_, err = disp.DispatchOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, callCountFor(fake, claimID, "abandon"),
		"redelivery after a crash between verb and delete must re-issue the idempotent verb")
	rows, err := outbox.ListAll(ctx, nil)
	require.NoError(t, err)
	require.Empty(t, rows)
}

func TestProducerVerbDispatcher_UnregisteredProducerKeepsRowForRetry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := openSQLiteForOutbox(t)
	tables := d.Tables()
	outbox := outboxOfTables(t, tables)
	start := time.Unix(3000, 0).UTC()
	clock := shared.NewControllableClock(start)
	fake := storetest.NewFake("store-known", claimproducer.Capabilities{})
	disp := newOutboxDispatcher(t, tables, fake, clock)

	claimID := shared.UUID(uuid.New())
	enqueueOutboxVerb(ctx, t, outbox, claimID, "store-vanished", persistence.ProducerVerbRelease, []byte(`"s"`), start)
	delivered, err := disp.DispatchOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, delivered)
	rows, err := outbox.ListAll(ctx, nil)
	require.NoError(t, err)
	require.Len(t, rows, 1, "a verb for an unregistered producer must survive for later delivery")
	require.Equal(t, 1, rows[0].AttemptCount)
}

func TestProducerVerbDispatcher_RunDeliversOnKickWithoutClockAdvance(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := openSQLiteForOutbox(t)
	tables := d.Tables()
	outbox := outboxOfTables(t, tables)
	start := time.Unix(4000, 0).UTC()
	clock := shared.NewControllableClock(start)
	fake := storetest.NewFake("store-a", claimproducer.Capabilities{})
	disp := newOutboxDispatcher(t, tables, fake, clock)

	done := make(chan struct{})
	go func() {
		disp.Run(ctx)
		close(done)
	}()

	claimID := shared.UUID(uuid.New())
	enqueueOutboxVerb(ctx, t, outbox, claimID, "store-a", persistence.ProducerVerbCommit, []byte(`"s"`), start)
	disp.Kick()
	for {
		rows, err := outbox.ListAll(context.Background(), nil)
		require.NoError(t, err)
		if len(rows) == 0 {
			break
		}
		runtime.Gosched()
	}
	cancel()
	<-done
	require.Equal(t, 1, callCountFor(fake, claimID, "commit"))
}

func TestProducerVerbBackoffDoublesToCap(t *testing.T) {
	t.Parallel()
	base := 1 * time.Second
	max := 8 * time.Second
	require.Equal(t, 1*time.Second, producerVerbBackoff(1, base, max))
	require.Equal(t, 2*time.Second, producerVerbBackoff(2, base, max))
	require.Equal(t, 4*time.Second, producerVerbBackoff(3, base, max))
	require.Equal(t, 8*time.Second, producerVerbBackoff(4, base, max))
	require.Equal(t, 8*time.Second, producerVerbBackoff(50, base, max))
}

func TestProducerVerbOutboxBarrier_DrainsMatchingScopeBeforeOpen(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := openSQLiteForOutbox(t)
	tables := d.Tables()
	outbox := outboxOfTables(t, tables)
	start := time.Unix(5000, 0).UTC()
	clock := shared.NewControllableClock(start)
	fake := storetest.NewFake("store-a", claimproducer.Capabilities{})
	args := RunArgs{
		Persist:      tables,
		ClaimHandles: tables.ClaimHandles(),
		Clock:        clock,
		Logger:       shared.SilentLogger{},
	}

	sameScope := []byte(`"s1"`)
	otherScope := []byte(`"s2"`)
	blockingClaim := shared.UUID(uuid.New())
	unrelatedClaim := shared.UUID(uuid.New())
	enqueueOutboxVerb(ctx, t, outbox, blockingClaim, "store-a", persistence.ProducerVerbCommit, sameScope, start)
	enqueueOutboxVerb(ctx, t, outbox, unrelatedClaim, "store-a", persistence.ProducerVerbAbandon, otherScope, start)

	require.NoError(t, producerVerbOutboxBarrier(ctx, args, fake, "store-a", sameScope, nil))
	require.Equal(t, 1, callCountFor(fake, blockingClaim, "commit"),
		"the barrier must deliver the scope's undelivered terminal before Open proceeds")
	require.Equal(t, 0, callCountFor(fake, unrelatedClaim, "abandon"),
		"the barrier must not touch unrelated scopes")
	rows, err := outbox.ListAll(ctx, nil)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, unrelatedClaim, rows[0].ClaimHandleID)
}

func TestProducerVerbOutboxBarrier_UndeliverableTerminalBlocksOpen(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := openSQLiteForOutbox(t)
	tables := d.Tables()
	outbox := outboxOfTables(t, tables)
	start := time.Unix(6000, 0).UTC()
	clock := shared.NewControllableClock(start)
	fake := storetest.NewFake("store-a", claimproducer.Capabilities{})
	fake.ErrorFunc = func(verb string, claimID claimproducer.ClaimID) error {
		return errors.New("producer unreachable")
	}
	args := RunArgs{
		Persist:      tables,
		ClaimHandles: tables.ClaimHandles(),
		Clock:        clock,
		Logger:       shared.SilentLogger{},
	}
	scope := []byte(`"s1"`)
	claimID := shared.UUID(uuid.New())
	enqueueOutboxVerb(ctx, t, outbox, claimID, "store-a", persistence.ProducerVerbAbandon, scope, start)

	err := producerVerbOutboxBarrier(ctx, args, fake, "store-a", scope, nil)
	require.Error(t, err, "an undeliverable terminal for the same scope must block Open")
	rows, lerr := outbox.ListAll(ctx, nil)
	require.NoError(t, lerr)
	require.Len(t, rows, 1, "the undelivered terminal must stay queued")
}

func callCountFor(fake *storetest.Fake, claimID shared.UUID, verb string) int {
	n := 0
	for _, c := range fake.Calls() {
		if c.ClaimID == claimproducer.ClaimID(claimID.String()) && c.Verb == verb {
			n++
		}
	}
	return n
}
