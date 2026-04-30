// Package store is the substrate-internal logic for the stub store-
// service. In-memory state; deterministic; no external dependencies.
// Per spec §8.3.
package store

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"

	corestore "github.com/fallguy/rimsky/core/store"
)

// Store is the in-memory substrate. Two operating modes: regional-direct
// (selectors echoed verbatim) and pick-policy (FIFO queue per
// configured selector).
type Store struct {
	caps         corestore.Capabilities
	pickPolicies map[string]*pickPolicy

	mu     sync.Mutex
	calls  []Call
	claims map[string]string // claim_id → item_id (pick-policy claims)
}

// pickPolicy is an in-memory FIFO queue + in-flight set.
type pickPolicy struct {
	queue           []item
	inFlight        map[string]item
	defaultOnCommit string
	defaultOnGiveUp string
	nextSeq         int
}

type item struct {
	id      string
	payload json.RawMessage
}

// Call records one verb invocation. Tests use Calls() to assert what
// fired.
type Call struct {
	Verb     string
	ClaimID  string
	Selector string
	Region   []byte
	Address  []byte
}

// Config is the substrate's config schema.
type Config struct {
	Capabilities corestore.Capabilities
	PickPolicies map[string]PickPolicyConfig
}

// PickPolicyConfig is the per-policy config (substrate-internal).
type PickPolicyConfig struct {
	OnCommitDefault string
	OnGiveUpDefault string
	InitialItems    []json.RawMessage
}

// New constructs a Store from cfg.
func New(cfg Config) *Store {
	caps := cfg.Capabilities
	if caps.WriteSemantics == "" {
		caps.WriteSemantics = corestore.WriteSemanticsDirect
	}
	s := &Store{
		caps:         caps,
		pickPolicies: make(map[string]*pickPolicy),
		claims:       make(map[string]string),
	}
	for selector, pc := range cfg.PickPolicies {
		pp := &pickPolicy{
			inFlight:        make(map[string]item),
			defaultOnCommit: pc.OnCommitDefault,
			defaultOnGiveUp: pc.OnGiveUpDefault,
		}
		for _, payload := range pc.InitialItems {
			pp.nextSeq++
			pp.queue = append(pp.queue, item{
				id:      fmt.Sprintf("stub-%s-%d", selector, pp.nextSeq),
				payload: payload,
			})
		}
		s.pickPolicies[selector] = pp
	}
	return s
}

// Capabilities returns the configured capability struct.
func (s *Store) Capabilities() corestore.Capabilities { return s.caps }

// Open performs the substrate's claim acquisition. ctx is accepted for
// signature uniformity with the postgres / filesystem substrates; the
// stub substrate has no async work that consults it.
func (s *Store) Open(_ context.Context, claimID, selector string) (corestore.OpenOutcome, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, Call{Verb: "open", ClaimID: claimID, Selector: selector})
	if pp, ok := s.pickPolicies[selector]; ok {
		if len(pp.queue) == 0 {
			return corestore.OpenOutcome{Available: false}, nil
		}
		head := pp.queue[0]
		pp.queue = pp.queue[1:]
		pp.inFlight[head.id] = head
		s.claims[claimID] = head.id
		addrBytes, _ := json.Marshal(head.id)
		regionBytes, _ := json.Marshal(head.id)
		return corestore.OpenOutcome{
			Available: true,
			Result: corestore.ClaimResult{
				Address: json.RawMessage(addrBytes),
				Payload: head.payload,
				Region:  json.RawMessage(regionBytes),
			},
		}, nil
	}
	addr, _ := json.Marshal(selector)
	region, _ := json.Marshal(selector)
	return corestore.OpenOutcome{
		Available: true,
		Result: corestore.ClaimResult{
			Address: json.RawMessage(addr),
			Region:  json.RawMessage(region),
		},
	}, nil
}

// Commit records the call and applies the on_commit action. Lookup is
// claim_id-based (idempotent in claim_id per spec §7.8 obligation #3):
// a duplicated terminal RPC after the claim was already terminated
// finds no live state for the claim_id and is a no-op.
func (s *Store) Commit(_ context.Context, claimID string, region, address []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, Call{
		Verb: "commit", ClaimID: claimID,
		Region: cloneBytes(region), Address: cloneBytes(address),
	})
	return s.applyPickActionByClaimID(claimID, true)
}

// Abandon records the call and applies the on_give_up action. Lookup
// is claim_id-based (idempotent per §7.8 obligation #3).
func (s *Store) Abandon(_ context.Context, claimID string, region, address []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, Call{
		Verb: "abandon", ClaimID: claimID,
		Region: cloneBytes(region), Address: cloneBytes(address),
	})
	return s.applyPickActionByClaimID(claimID, false)
}

// Release records the call. No state to tear down for stub.
func (s *Store) Release(_ context.Context, claimID string, region, address []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, Call{
		Verb: "release", ClaimID: claimID,
		Region: cloneBytes(region), Address: cloneBytes(address),
	})
	delete(s.claims, claimID)
	return nil
}

// applyPickActionByClaimID resolves the in-flight item via the
// claim_id → item_id map populated at Open. Idempotent per spec §7.8
// obligation #3: an unknown claim_id (already terminated, or never
// belonged to a pick policy) is a no-op. The configured default
// (defaultOnCommit when successPath, defaultOnGiveUp otherwise) is
// the only governing input — substrate-vocabulary excised from the
// rimsky surface per the 2026-04-30 cleanup.
func (s *Store) applyPickActionByClaimID(claimID string, successPath bool) error {
	itemID, ok := s.claims[claimID]
	if !ok {
		return nil
	}
	delete(s.claims, claimID)
	for _, pp := range s.pickPolicies {
		it, ok := pp.inFlight[itemID]
		if !ok {
			continue
		}
		var action string
		if successPath {
			action = pp.defaultOnCommit
		} else {
			action = pp.defaultOnGiveUp
		}
		switch action {
		case "delete":
			delete(pp.inFlight, itemID)
		case "release_to_back":
			delete(pp.inFlight, itemID)
			pp.queue = append(pp.queue, it)
		case "release_to_head":
			delete(pp.inFlight, itemID)
			pp.queue = append([]item{it}, pp.queue...)
		default:
			return fmt.Errorf("stub store: applyPickAction: unknown action %q", action)
		}
		return nil
	}
	return nil
}

// SeedPickPolicyItem appends a payload to the named policy's queue and
// returns the assigned id. Test helper.
func (s *Store) SeedPickPolicyItem(selector string, payload json.RawMessage) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pp, ok := s.pickPolicies[selector]
	if !ok {
		return "", fmt.Errorf("stub store: SeedPickPolicyItem: no policy for selector %q", selector)
	}
	pp.nextSeq++
	id := fmt.Sprintf("stub-%s-%d", selector, pp.nextSeq)
	pp.queue = append(pp.queue, item{id: id, payload: payload})
	return id, nil
}

// Calls returns a copy of the recorder slice.
func (s *Store) Calls() []Call {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Call, len(s.calls))
	copy(out, s.calls)
	return out
}

// QueueLen returns the FIFO length for the named policy. -1 if no
// policy.
func (s *Store) QueueLen(selector string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	pp, ok := s.pickPolicies[selector]
	if !ok {
		return -1
	}
	return len(pp.queue)
}

// InFlight returns the sorted set of in-flight item IDs for the named
// policy.
func (s *Store) InFlight(selector string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	pp, ok := s.pickPolicies[selector]
	if !ok {
		return nil
	}
	out := make([]string, 0, len(pp.inFlight))
	for id := range pp.inFlight {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func cloneBytes(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out
}
