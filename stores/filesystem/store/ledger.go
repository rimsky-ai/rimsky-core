// Package store: ledger.go is the in-memory per-claim history used by
// the filesystem store's observability surface (StoreObservability).
// Rimsky-side claim content invariants (blessed-invariant 20) do not
// govern stores' *own* observability — the store decides what to expose
// (spec §3.2). The ledger is bounded by a max claim count per state
// bucket so a long-running deployment doesn't grow unbounded.
package store

import (
	"strconv"
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
	Region   []byte
	Selector string
	OpenedAt time.Time
	ClosedAt *time.Time
	History  []ClaimEvent
}

// ClaimLedger is a bounded in-memory ledger of claim lifecycle events.
// Thread-safe.
type ClaimLedger struct {
	mu      sync.RWMutex
	records map[string]*ClaimRecord
	max     int
	order   []string // insertion order of claim_ids for bounded eviction
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
	}
}

// RecordOpen adds a claim_opened event and creates the record.
func (l *ClaimLedger) RecordOpen(claimID, selector string, address, region []byte) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now().UTC()
	rec := &ClaimRecord{
		ClaimID:  claimID,
		State:    ClaimStateOpen,
		Address:  address,
		Region:   region,
		Selector: selector,
		OpenedAt: now,
		History: []ClaimEvent{{
			EventID:   uuid.New().String(),
			Timestamp: now,
			Severity:  "INFO",
			Category:  "claim_opened",
			Attributes: map[string]any{
				"selector": selector,
			},
		}},
	}
	l.records[claimID] = rec
	l.order = append(l.order, claimID)
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
	rec.History = append(rec.History, ClaimEvent{
		EventID:    uuid.New().String(),
		Timestamp:  now,
		Severity:   severity,
		Category:   category,
		Attributes: attrs,
	})
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
	rec.History = append(rec.History, ClaimEvent{
		EventID:    uuid.New().String(),
		Timestamp:  now,
		Severity:   "INFO",
		Category:   category,
		Attributes: attrs,
	})
	l.evictIfNeeded()
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
// Pagination is by insertion order (cursor is the next index, base10).
func (l *ClaimLedger) List(stateFilter string, cursor string, limit int) ([]*ClaimRecord, string) {
	if l == nil {
		return nil, ""
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	if limit <= 0 {
		limit = 50
	}
	start := 0
	if cursor != "" {
		if n, err := strconv.Atoi(cursor); err == nil {
			start = n
		}
	}
	out := make([]*ClaimRecord, 0, limit)
	i := start
	for i < len(l.order) && len(out) < limit {
		id := l.order[i]
		i++
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
	}
	next := ""
	if i < len(l.order) {
		next = strconv.Itoa(i)
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
