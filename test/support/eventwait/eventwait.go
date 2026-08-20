// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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

// @decision: polling-audit
// @decision: testing-scenario-based-e2e
func WaitForEvent(ctx context.Context, t testing.TB, db persistence.Tables, m Matcher) []persistence.EventRow {
	t.Helper()
	for poll := 1; ; poll++ {
		matched, _, err := read(ctx, db, m)
		if err == nil && len(matched) >= m.minCount() {
			return matched
		}
		if poll == 1 {
			t.Logf("eventwait.WaitForEvent: polling for matcher {%s} (first observation: %d matched, err=%v) — "+
				"blocks until the event appears and says so once, so a wait that never ends leaves the test "+
				"guard's no-progress watchdog free to trip", m, len(matched), err)
		}
		//nolint:testwallclock-outcome inter-poll cadence; this loop returns only when the matcher is satisfied
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
