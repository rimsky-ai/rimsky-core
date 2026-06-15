// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Package eventwait provides test waits anchored to the rimsky_events
// ledger instead of mutable row state.
//
// The event log is append-only: once a transition's audit row lands it
// cannot be un-observed, so a poll over it can never miss a transient
// transition (a node that flips running→stale→running between samples,
// a spurious cascade that runs a node and settles it back to fresh).
// Tests that need "X happened" / "X never happened" should read this
// durable record rather than sample the mutable node/run rows that X
// transitions through.
//
// @agent-contract: WaitForEvent blocks until the matcher is satisfied
// or the deadline elapses, then fails the test fatally, dumping every
// event seen for the matcher's scope so the failure is diagnosable
// without a re-run. Events is the non-blocking read used for
// absence assertions over the same durable record. Both helpers open
// their own read transactions; neither mutates state. Safe for
// parallel tests (each test passes its own persistence handle).
package eventwait

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

// Matcher selects rows of the rimsky_events ledger. Scope fields
// (InstanceID / NodeID) are pushed down to the persistence filter;
// kind matching happens helper-side so callers can match on the
// signal type-path prefix families (terminal/, transient/retry/, …)
// that the exact-match persistence filter cannot express.
type Matcher struct {
	// InstanceID scopes to one instance's events. Optional.
	InstanceID *shared.UUID
	// NodeID scopes to one node's events. Optional.
	NodeID *shared.UUID
	// Kind matches the canonical wire kind exactly (e.g.
	// "transient/heartbeat_missed"). Empty → no exact-kind condition.
	Kind string
	// KindPrefix matches kinds by prefix (e.g. "transient/retry/").
	// Empty → no prefix condition. Combined with Kind via OR when both
	// are set (a row matching either counts).
	KindPrefix string
	// MinCount is the number of matching rows required before
	// WaitForEvent returns. Zero means 1.
	MinCount int
}

func (m Matcher) String() string {
	parts := []string{}
	if m.InstanceID != nil {
		parts = append(parts, "instance="+m.InstanceID.String())
	}
	if m.NodeID != nil {
		parts = append(parts, "node="+m.NodeID.String())
	}
	if m.Kind != "" {
		parts = append(parts, "kind="+m.Kind)
	}
	if m.KindPrefix != "" {
		parts = append(parts, "kind_prefix="+m.KindPrefix)
	}
	parts = append(parts, fmt.Sprintf("min_count=%d", m.minCount()))
	return strings.Join(parts, " ")
}

func (m Matcher) minCount() int {
	if m.MinCount <= 0 {
		return 1
	}
	return m.MinCount
}

func (m Matcher) kindMatches(kind string) bool {
	if m.Kind == "" && m.KindPrefix == "" {
		return true
	}
	if m.Kind != "" && kind == m.Kind {
		return true
	}
	return m.KindPrefix != "" && strings.HasPrefix(kind, m.KindPrefix)
}

const pollInterval = 50 * time.Millisecond

// WaitForEvent polls the append-only event log until at least
// m.MinCount rows match, returning the matching rows. On timeout it
// fails the test fatally, listing every event actually seen in the
// matcher's instance/node scope — the events that DID land are the
// diagnostic for why the awaited one didn't.
func WaitForEvent(ctx context.Context, t testing.TB, db persistence.Tables, m Matcher, deadline time.Duration) []persistence.EventRow {
	t.Helper()
	end := time.Now().Add(deadline)
	for {
		// @deliberate: Fail fast on a canceled caller context — without this, a
		// canceled ctx makes every read below error and the loop burns
		// the full deadline in 50ms sleeps before reporting.
		if ctxErr := ctx.Err(); ctxErr != nil {
			t.Fatalf("eventwait.WaitForEvent: context done before matcher {%s} satisfied: %v", m, ctxErr)
			return nil
		}
		matched, all, err := read(ctx, db, m)
		if err == nil && len(matched) >= m.minCount() {
			return matched
		}
		if !time.Now().Before(end) {
			if err != nil {
				t.Fatalf("eventwait.WaitForEvent: matcher {%s} not satisfied within %v; last read error: %v", m, deadline, err)
			}
			t.Fatalf("eventwait.WaitForEvent: matcher {%s} not satisfied within %v; got %d matching rows. Events seen in scope:\n%s",
				m, deadline, len(matched), dump(all))
			return nil
		}
		time.Sleep(pollInterval)
	}
}

// Events returns the rows currently matching m without waiting. Use it
// for absence assertions over the durable record — "this node was
// never dispatched" reads the ledger, where a transient run leaves
// work_started / terminal rows that a mutable-state sample would miss.
func Events(ctx context.Context, t testing.TB, db persistence.Tables, m Matcher) []persistence.EventRow {
	t.Helper()
	matched, _, err := read(ctx, db, m)
	if err != nil {
		t.Fatalf("eventwait.Events: matcher {%s}: %v", m, err)
	}
	return matched
}

// read lists every event in the matcher's scope (paging through the
// cursor) and partitions kind-matching rows out.
func read(ctx context.Context, db persistence.Tables, m Matcher) (matched, all []persistence.EventRow, err error) {
	filter := persistence.EventListFilter{
		InstanceID: m.InstanceID,
		NodeID:     m.NodeID,
	}
	err = db.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		cursor := ""
		for {
			res, listErr := db.Events().List(ctx, filter,
				persistence.ListPagination{Limit: 500, Cursor: cursor}, tx)
			if listErr != nil {
				return listErr
			}
			all = append(all, res.Events...)
			if res.NextCursor == "" || len(res.Events) == 0 {
				return nil
			}
			cursor = res.NextCursor
		}
	})
	if err != nil {
		return nil, nil, err
	}
	for _, e := range all {
		if m.kindMatches(e.KindRaw) {
			matched = append(matched, e)
		}
	}
	return matched, all, nil
}

func dump(events []persistence.EventRow) string {
	if len(events) == 0 {
		return "  (none)"
	}
	var b strings.Builder
	for _, e := range events {
		node := "-"
		if e.NodeID != nil {
			node = e.NodeID.String()
		}
		fmt.Fprintf(&b, "  %s  kind=%s node=%s payload=%v\n",
			e.OccurredAt.Format(time.RFC3339Nano), e.KindRaw, node, e.Payload)
	}
	return b.String()
}
