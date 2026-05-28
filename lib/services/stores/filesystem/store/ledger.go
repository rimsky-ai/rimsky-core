// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Package store: ledger.go is the in-memory per-claim history used by
// the filesystem store's observability surface (ClaimProducerObservability).
// Rimsky-side claim content invariants (blessed-invariant 20) do not
// govern stores' *own* observability — the store decides what to expose
// (spec §3.2). The ledger is bounded by a max claim count per state
// bucket so a long-running deployment doesn't grow unbounded.
//
// This is the canonical implementation; the postgres store carries a
// near-identical copy.
//
//	@source: stores/postgres/store/ledger.go
package store

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

// ClaimState mirrors the proto ClaimState enum for the store
// observability protocol. Stored as string for json/struct uniformity.
type ClaimState string

const (
	ClaimStateOpen      ClaimState = "OPEN"
	ClaimStateCommitted ClaimState = "COMMITTED"
	ClaimStateAbandoned ClaimState = "ABANDONED"
	ClaimStateReleased  ClaimState = "RELEASED"
	ClaimStateUnknown   ClaimState = "UNKNOWN"
)

// ClaimEvent is one entry in a claim's history.
type ClaimEvent struct {
	EventID    string         `json:"event_id"`
	Timestamp  time.Time      `json:"timestamp"`
	Severity   string         `json:"severity"`
	Category   string         `json:"category"`
	Message    string         `json:"message,omitempty"`
	Attributes map[string]any `json:"attributes,omitempty"`
}

// ClaimRecord is the in-memory record for one claim.
type ClaimRecord struct {
	ClaimID  string
	State    ClaimState
	Address  []byte
	Scope    []byte
	Selector string
	OpenedAt time.Time
	ClosedAt *time.Time
	History  []ClaimEvent
}

// subscriber is a per-claim live-event listener.
//
//	@source: stores/postgres/store/ledger.go:subscriber
type subscriber struct {
	ch chan ClaimEvent
}

// ClaimLedger is a bounded in-memory ledger of claim lifecycle events.
// Thread-safe.
type ClaimLedger struct {
	mu      sync.RWMutex
	records map[string]*ClaimRecord
	max     int
	order   []string // insertion order of claim_ids for bounded eviction
	subs    map[string]map[*subscriber]struct{}
}

// NewClaimLedger returns a ledger that retains at most max claims (after
// terminal). When the bound is reached, the oldest terminal claim is
// evicted.
func NewClaimLedger(max int) *ClaimLedger {
	if max <= 0 {
		max = 1024
	}
	return &ClaimLedger{
		records: make(map[string]*ClaimRecord),
		max:     max,
		subs:    make(map[string]map[*subscriber]struct{}),
	}
}

// RecordOpen adds a claim_opened event and creates the record.
func (l *ClaimLedger) RecordOpen(claimID, selector string, address, scope []byte) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now().UTC()
	openEvent := ClaimEvent{
		EventID:   uuid.New().String(),
		Timestamp: now,
		Severity:  "INFO",
		Category:  "claim_opened",
		Attributes: map[string]any{
			"selector": selector,
		},
	}
	rec := &ClaimRecord{
		ClaimID:  claimID,
		State:    ClaimStateOpen,
		Address:  address,
		Scope:    scope,
		Selector: selector,
		OpenedAt: now,
		History:  []ClaimEvent{openEvent},
	}
	l.records[claimID] = rec
	l.order = append(l.order, claimID)
	l.broadcast(claimID, openEvent)
	l.evictIfNeeded()
}

// RecordEvent appends a non-terminal event to the claim's history
// without altering State or ClosedAt. Used for failure events
// (claim_commit_failed / claim_abandon_failed) that don't actually
// close the claim — the next retry may still succeed.
func (l *ClaimLedger) RecordEvent(claimID, category, severity string, attrs map[string]any) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	rec, ok := l.records[claimID]
	if !ok {
		rec = &ClaimRecord{ClaimID: claimID, State: ClaimStateUnknown, OpenedAt: time.Now().UTC()}
		l.records[claimID] = rec
		l.order = append(l.order, claimID)
	}
	now := time.Now().UTC()
	if severity == "" {
		severity = "INFO"
	}
	ev := ClaimEvent{
		EventID:    uuid.New().String(),
		Timestamp:  now,
		Severity:   severity,
		Category:   category,
		Attributes: attrs,
	}
	rec.History = append(rec.History, ev)
	l.broadcast(claimID, ev)
	l.evictIfNeeded()
}

// RecordTerminal appends a terminal event (commit/abandon/release).
// category is one of claim_committed | claim_abandoned | claim_released.
func (l *ClaimLedger) RecordTerminal(claimID, category string, attrs map[string]any) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	rec, ok := l.records[claimID]
	if !ok {
		// Open was not recorded (e.g. evicted, restarted) — synthesise a
		// minimal record so the dashboard still sees the terminal.
		rec = &ClaimRecord{ClaimID: claimID, State: ClaimStateUnknown, OpenedAt: time.Now().UTC()}
		l.records[claimID] = rec
		l.order = append(l.order, claimID)
	}
	now := time.Now().UTC()
	rec.ClosedAt = &now
	switch category {
	case "claim_committed":
		rec.State = ClaimStateCommitted
	case "claim_abandoned":
		rec.State = ClaimStateAbandoned
	case "claim_released":
		rec.State = ClaimStateReleased
	}
	ev := ClaimEvent{
		EventID:    uuid.New().String(),
		Timestamp:  now,
		Severity:   "INFO",
		Category:   category,
		Attributes: attrs,
	}
	rec.History = append(rec.History, ev)
	l.broadcast(claimID, ev)
	for sub := range l.subs[claimID] {
		close(sub.ch)
	}
	delete(l.subs, claimID)
	l.evictIfNeeded()
}

// SubscribeWithSnapshot atomically returns the current history plus a
// channel of new events. Eliminates the snapshot/subscribe race.
//
//	@source: stores/postgres/store/ledger.go:SubscribeWithSnapshot
func (l *ClaimLedger) SubscribeWithSnapshot(claimID string) ([]ClaimEvent, *ClaimRecord, <-chan ClaimEvent, func()) {
	if l == nil {
		ch := make(chan ClaimEvent)
		close(ch)
		return nil, nil, ch, func() {}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	rec, ok := l.records[claimID]
	if !ok {
		ch := make(chan ClaimEvent)
		close(ch)
		return nil, nil, ch, func() {}
	}
	cp := *rec
	cp.History = append([]ClaimEvent(nil), rec.History...)
	if rec.State != ClaimStateOpen {
		ch := make(chan ClaimEvent)
		close(ch)
		return cp.History, &cp, ch, func() {}
	}
	sub := &subscriber{ch: make(chan ClaimEvent, 32)}
	if l.subs[claimID] == nil {
		l.subs[claimID] = map[*subscriber]struct{}{}
	}
	l.subs[claimID][sub] = struct{}{}
	unsub := func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		if subs, ok := l.subs[claimID]; ok {
			delete(subs, sub)
		}
	}
	return cp.History, &cp, sub.ch, unsub
}

// broadcast pushes ev to each live subscriber. Caller MUST hold l.mu.
//
//	@source: stores/postgres/store/ledger.go:broadcast
func (l *ClaimLedger) broadcast(claimID string, ev ClaimEvent) {
	for sub := range l.subs[claimID] {
		select {
		case sub.ch <- ev:
		default:
		}
	}
}

// Get returns the claim record for claimID, or (nil, false).
func (l *ClaimLedger) Get(claimID string) (*ClaimRecord, bool) {
	if l == nil {
		return nil, false
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	rec, ok := l.records[claimID]
	if !ok {
		return nil, false
	}
	// Return a defensive copy of history.
	cp := *rec
	cp.History = append([]ClaimEvent(nil), rec.History...)
	return &cp, true
}

// List returns up to limit records, optionally filtered by state.
// Cursor encodes the last-returned claim_id (stable across concurrent
// eviction).
//
//	@source: stores/postgres/store/ledger.go:List
func (l *ClaimLedger) List(stateFilter string, cursor string, limit int) ([]*ClaimRecord, string) {
	if l == nil {
		return nil, ""
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	if limit <= 0 {
		limit = 50
	}
	skip := cursor != ""
	out := make([]*ClaimRecord, 0, limit)
	lastID := ""
	for _, id := range l.order {
		if skip {
			if id == cursor {
				skip = false
			}
			continue
		}
		rec, ok := l.records[id]
		if !ok {
			continue
		}
		if stateFilter != "" && string(rec.State) != stateFilter {
			continue
		}
		cp := *rec
		cp.History = append([]ClaimEvent(nil), rec.History...)
		out = append(out, &cp)
		lastID = id
		if len(out) >= limit {
			break
		}
	}
	next := ""
	if len(out) >= limit && lastID != "" {
		next = lastID
	}
	return out, next
}

// evictIfNeeded enforces the ledger bound. Prefers evicting terminal
// records first (so live OPEN claims survive longer), but falls back
// to dropping the oldest record regardless of state once the soft
// terminal-only sweep can't reduce size further. Without the
// fall-through, a long run of never-terminal dispatches would grow
// the ledger unbounded. Caller must hold l.mu.
func (l *ClaimLedger) evictIfNeeded() {
	for len(l.records) > l.max {
		// Soft pass: oldest terminal record.
		evicted := ""
		for i, id := range l.order {
			rec, ok := l.records[id]
			if !ok {
				continue
			}
			if rec.State != ClaimStateOpen {
				delete(l.records, id)
				l.order = append(l.order[:i], l.order[i+1:]...)
				evicted = id
				break
			}
		}
		if evicted != "" {
			continue
		}
		// Hard fall-through: oldest record regardless of state.
		if len(l.order) == 0 {
			return
		}
		oldest := l.order[0]
		l.order = l.order[1:]
		delete(l.records, oldest)
	}
}
