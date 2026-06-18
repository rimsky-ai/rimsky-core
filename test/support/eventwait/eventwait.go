// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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

type Matcher struct {
	InstanceID *shared.UUID
	NodeID     *shared.UUID
	Kind       string
	KindPrefix string
	MinCount   int
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

func WaitForEvent(ctx context.Context, t testing.TB, db persistence.Tables, m Matcher, deadline time.Duration) []persistence.EventRow {
	t.Helper()
	end := time.Now().Add(deadline)
	for {
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

func Events(ctx context.Context, t testing.TB, db persistence.Tables, m Matcher) []persistence.EventRow {
	t.Helper()
	matched, _, err := read(ctx, db, m)
	if err != nil {
		t.Fatalf("eventwait.Events: matcher {%s}: %v", m, err)
	}
	return matched
}

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
