// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package claimledger

import (
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
)

type ClaimState string

const (
	ClaimStateOpen      ClaimState = "OPEN"
	ClaimStateCommitted ClaimState = "COMMITTED"
	ClaimStateAbandoned ClaimState = "ABANDONED"
	ClaimStateReleased  ClaimState = "RELEASED"
	ClaimStateUnknown   ClaimState = "UNKNOWN"
)

type ClaimEvent struct {
	EventID    string         `json:"event_id"`
	Timestamp  time.Time      `json:"timestamp"`
	Severity   string         `json:"severity"`
	Category   string         `json:"category"`
	Message    string         `json:"message,omitempty"`
	Attributes map[string]any `json:"attributes,omitempty"`
}

type ClaimRecord struct {
	ClaimID  string
	State    ClaimState
	Address  []byte
	Scope    []byte
	Selector string
	OpenedAt time.Time
	ClosedAt *time.Time
	History  []ClaimEvent

	seq int64
}

type subscriber struct {
	ch chan ClaimEvent
}

type ClaimLedger struct {
	mu      sync.RWMutex
	records map[string]*ClaimRecord
	order   []string
	max     int
	subs    map[string]map[*subscriber]struct{}
	nextSeq int64
}

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
	l.nextSeq++
	_, existed := l.records[claimID]
	rec := &ClaimRecord{
		ClaimID:  claimID,
		State:    ClaimStateOpen,
		Address:  address,
		Scope:    scope,
		Selector: selector,
		OpenedAt: now,
		History:  []ClaimEvent{openEvent},
		seq:      l.nextSeq,
	}
	l.records[claimID] = rec
	if !existed {
		l.order = append(l.order, claimID)
	}
	l.broadcast(claimID, openEvent)
	l.evictIfNeeded()
}

func (l *ClaimLedger) newUnknownRecordLocked(claimID string) *ClaimRecord {
	l.nextSeq++
	rec := &ClaimRecord{ClaimID: claimID, State: ClaimStateUnknown, OpenedAt: time.Now().UTC(), seq: l.nextSeq}
	l.records[claimID] = rec
	l.order = append(l.order, claimID)
	return rec
}

func (l *ClaimLedger) RecordEvent(claimID, category, severity string, attrs map[string]any) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	rec, ok := l.records[claimID]
	if !ok {
		rec = l.newUnknownRecordLocked(claimID)
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

func (l *ClaimLedger) RecordTerminal(claimID, category string, attrs map[string]any) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	rec, ok := l.records[claimID]
	if !ok {
		rec = l.newUnknownRecordLocked(claimID)
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

func (l *ClaimLedger) broadcast(claimID string, ev ClaimEvent) {
	for sub := range l.subs[claimID] {
		select {
		case sub.ch <- ev:
		default:
		}
	}
}

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

func (l *ClaimLedger) List(stateFilter, cursor string, limit int) ([]*ClaimRecord, string) {
	if l == nil {
		return nil, ""
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	if limit <= 0 {
		limit = 50
	}
	afterSeq := int64(0)
	if cursor != "" {
		parsed, err := strconv.ParseInt(cursor, 10, 64)
		if err != nil {
			return nil, ""
		}
		afterSeq = parsed
	}
	out := make([]*ClaimRecord, 0, limit)
	lastSeq := afterSeq
	for _, id := range l.order {
		rec, ok := l.records[id]
		if !ok || rec.seq <= afterSeq {
			continue
		}
		if stateFilter != "" && string(rec.State) != stateFilter {
			continue
		}
		cp := *rec
		cp.History = append([]ClaimEvent(nil), rec.History...)
		out = append(out, &cp)
		lastSeq = rec.seq
		if len(out) >= limit {
			break
		}
	}
	next := ""
	if len(out) >= limit && lastSeq != afterSeq {
		next = strconv.FormatInt(lastSeq, 10)
	}
	return out, next
}

func (l *ClaimLedger) evictIfNeeded() {
	for len(l.records) > l.max {
		evicted := false
		for i, id := range l.order {
			rec, ok := l.records[id]
			if !ok {
				l.order = append(l.order[:i], l.order[i+1:]...)
				evicted = true
				break
			}
			if rec.State != ClaimStateOpen {
				delete(l.records, id)
				l.order = append(l.order[:i], l.order[i+1:]...)
				evicted = true
				break
			}
		}
		if !evicted {
			return
		}
	}
}
