// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Package store: ledger.go is the in-memory per-claim history surfaced
// by the postgres store's ClaimProducerObservability protocol. The store's
// authoritative record of in-flight pick-policy claims is the items
// table's claim_token column; the ledger is an additive, observability-
// only artifact populated from each Open/Commit/Abandon/Release call.
//
// Bounded by a max claim count to bound memory growth.
//
// This file is a tracked copy of the filesystem store's ledger; the
// two stores intentionally share a near-identical bounded in-memory
// ledger. The third-call-site rule for shared extraction is not yet
// satisfied; until it is, divergence is tracked via @source on each
// method-level near-duplicate.
//
//	@source: lib/services/stores/filesystem/store/ledger.go
package store

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

// ClaimState mirrors proto ClaimState.
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

// subscriber is a per-claim live-event listener. ch is the receive
// side; close(done) signals the producer to stop sending. The producer
// uses a non-blocking send under l.mu; missing wakeups are safe because
// the dispatcher always replays the latest history before transitioning
// to the live phase.
type subscriber struct {
	ch chan ClaimEvent
}

// ClaimLedger is a bounded in-memory ledger.
type ClaimLedger struct {
	mu      sync.RWMutex
	records map[string]*ClaimRecord
	order   []string
	max     int
	// subs is the per-claim subscriber set. Producers call broadcast()
	// under mu after appending an event; subscribers receive it on ch.
	subs map[string]map[*subscriber]struct{}
}

// NewClaimLedger constructs a bounded ledger.
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

// RecordOpen records a new claim_opened event.
func (l *ClaimLedger) RecordOpen(claimID, selector string, address, scope []byte) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now().UTC()
	openEvent := ClaimEvent{
		EventID:    uuid.New().String(),
		Timestamp:  now,
		Severity:   "INFO",
		Category:   "claim_opened",
		Attributes: map[string]any{"selector": selector},
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

// RecordTerminal records a terminal event.
func (l *ClaimLedger) RecordTerminal(claimID, category string, attrs map[string]any) {
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
	// @constraint: close every subscriber channel on terminal so StreamClaim
	// handlers exit via range-loop completion; no further events will be
	// appended to a terminal record.
	for sub := range l.subs[claimID] {
		close(sub.ch)
	}
	delete(l.subs, claimID)
	l.evictIfNeeded()
}

// Subscribe returns a channel of new events for claimID and an
// unsubscribe function. The channel is closed when the claim hits a
// terminal event (so consumers can range over it). Existing history is
// the caller's concern — replay it before subscribing or accept the
// race window. The bridge replays under l.mu via SubscribeWithSnapshot
// to avoid the gap.
func (l *ClaimLedger) Subscribe(claimID string) (<-chan ClaimEvent, func()) {
	if l == nil {
		ch := make(chan ClaimEvent)
		close(ch)
		return ch, func() {}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	sub := &subscriber{ch: make(chan ClaimEvent, 32)}
	if _, ok := l.records[claimID]; !ok {
		close(sub.ch)
		return sub.ch, func() {}
	}
	if l.subs[claimID] == nil {
		l.subs[claimID] = map[*subscriber]struct{}{}
	}
	l.subs[claimID][sub] = struct{}{}
	unsub := func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		if subs, ok := l.subs[claimID]; ok {
			if _, ok := subs[sub]; ok {
				delete(subs, sub)
				// @constraint: drain one buffered event so a concurrent
				// producer's non-blocking send cannot race a future close().
				select {
				case <-sub.ch:
				default:
				}
			}
		}
	}
	return sub.ch, unsub
}

// SubscribeWithSnapshot atomically returns the current history plus a
// channel of new events. Eliminates the race between snapshot and
// subscribe (events landing between the two would otherwise be lost).
// The returned channel closes on terminal.
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

// broadcast pushes ev to each live subscriber for claimID. Caller MUST
// hold l.mu. Non-blocking: a slow consumer dropping events is preferable
// to stalling the producer (terminal close still arrives).
func (l *ClaimLedger) broadcast(claimID string, ev ClaimEvent) {
	for sub := range l.subs[claimID] {
		select {
		case sub.ch <- ev:
		default:
		}
	}
}

// Get returns a defensive copy of the claim record.
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
	cp := *rec
	cp.History = append([]ClaimEvent(nil), rec.History...)
	return &cp, true
}

// List returns up to limit records, optionally state-filtered. Cursor
// encodes the last-returned claim_id (stable across concurrent
// eviction; positional indexes shift when the soft pass deletes
// arbitrary terminal records, so a string-comparable opaque cursor
// avoids that bug).
func (l *ClaimLedger) List(stateFilter, cursor string, limit int) ([]*ClaimRecord, string) {
	if l == nil {
		return nil, ""
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	if limit <= 0 {
		limit = 50
	}
	// @deliberate: scan order linearly from the cursor's claim_id; order is
	// append-only at insert, so the linear walk is O(n) until `limit`
	// records are collected — adequate given the bounded ledger size.
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
// records first; falls back to dropping the oldest record regardless
// of state once the soft pass can't reduce size further. See the
// matching @source: lib/services/stores/filesystem/store/ledger.go:evictIfNeeded.
//
//	@source: lib/services/stores/filesystem/store/ledger.go:evictIfNeeded
func (l *ClaimLedger) evictIfNeeded() {
	for len(l.records) > l.max {
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
		if len(l.order) == 0 {
			return
		}
		oldest := l.order[0]
		l.order = l.order[1:]
		delete(l.records, oldest)
	}
}
