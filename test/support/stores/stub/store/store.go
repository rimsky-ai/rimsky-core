// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Package store is the store-internal logic for the stub store-
// service. In-memory state; deterministic; no external dependencies.
// Per spec §8.3.
package store

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/action"
	claimproducer "github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

// Store is the in-memory store implementation. Two operating modes: scoped-direct
// (selectors echoed verbatim) and pick-policy (FIFO queue per
// configured selector).
type Store struct {
	caps         claimproducer.Capabilities
	pickPolicies map[string]*pickPolicy

	// commitVersionID / commitProducerMetadata are stamped on every
	// CommitResponse by the gRPC server layer (see Config).
	commitVersionID        string
	commitProducerMetadata []byte

	mu     sync.Mutex
	calls  []Call
	claims map[string]string // claim_id → item_id (pick-policy claims)
}

// CommitResponseFields returns the configured base-Commit response
// stamps (version_id, producer_metadata). Read by the server layer
// when building CommitResponse.
func (s *Store) CommitResponseFields() (string, []byte) {
	return s.commitVersionID, s.commitProducerMetadata
}

// pickPolicy is an in-memory FIFO queue + in-flight set.
type pickPolicy struct {
	queue            []item
	inFlight         map[string]item
	defaultOnCommit  action.Action
	defaultOnGiveUp  action.Action
	unavailableClass string
	nextSeq          int
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
	Scope    []byte
	Address  []byte
}

// Config is the store's config schema.
type Config struct {
	Capabilities claimproducer.Capabilities
	PickPolicies map[string]PickPolicyConfig
	// CommitVersionID / CommitProducerMetadata, when set, are stamped
	// on every base-protocol CommitResponse the stub's server emits —
	// the fixture for scenarios proving rimsky honors the Commit
	// response body (version_id persisted on the claim-handle row,
	// producer_metadata surfaced in the fan-out parent's writeback).
	// Zero values = empty response (the default producer behavior).
	CommitVersionID        string
	CommitProducerMetadata []byte
}

// PickPolicyConfig is the per-policy config (store-internal).
//
// UnavailableClass, when non-empty, is the producer-declared
// acquisition-failure class (e.g. "pg/claim_unavailable") this policy
// names on its Unavailable arm when the queue is empty — mirroring a
// real producer that classifies its acquisition failures
// (OpenOutcome.UnavailableClass). Tests that set it should also
// declare the class in Config.Capabilities.DeclaredErrorClasses so
// the registration validator's vocabulary check sees it.
type PickPolicyConfig struct {
	OnCommit         action.Action
	OnGiveUp         action.Action
	InitialItems     []json.RawMessage
	UnavailableClass string
}

// New constructs a Store from cfg. The stub store declares a singleton
// envelope of [sync] by default — selectors are echoed verbatim and no
// async staging is involved.
func New(cfg Config) *Store {
	caps := cfg.Capabilities
	if len(caps.WriteSemanticsAllowed) == 0 {
		caps.WriteSemanticsAllowed = []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}
	}
	s := &Store{
		caps:                   caps,
		pickPolicies:           make(map[string]*pickPolicy),
		claims:                 make(map[string]string),
		commitVersionID:        cfg.CommitVersionID,
		commitProducerMetadata: cfg.CommitProducerMetadata,
	}
	for selector, pc := range cfg.PickPolicies {
		pp := &pickPolicy{
			inFlight:         make(map[string]item),
			defaultOnCommit:  pc.OnCommit,
			defaultOnGiveUp:  pc.OnGiveUp,
			unavailableClass: pc.UnavailableClass,
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
func (s *Store) Capabilities() claimproducer.Capabilities { return s.caps }

// Open performs the store's claim acquisition. ctx is accepted for
// signature uniformity with the postgres / filesystem stores; the
// stub store has no async work that consults it.
//
// The stub declares a singleton envelope, so RealizedWriteSemantics is
// uniform across all claims (uniformity invariant trivially holds).
func (s *Store) Open(_ context.Context, claimID, selector string) (claimproducer.OpenOutcome, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, Call{Verb: "open", ClaimID: claimID, Selector: selector})
	rws := s.realizedSemantics()
	if pp, ok := s.pickPolicies[selector]; ok {
		if len(pp.queue) == 0 {
			// Carry the producer-declared class on the Unavailable arm
			// when configured (mirrors OpenOutcome.UnavailableClass on
			// a classifying producer); empty otherwise.
			return claimproducer.OpenOutcome{Available: false, UnavailableClass: pp.unavailableClass}, nil
		}
		head := pp.queue[0]
		pp.queue = pp.queue[1:]
		pp.inFlight[head.id] = head
		s.claims[claimID] = head.id
		addrBytes, _ := json.Marshal(head.id)
		scopeBytes, _ := json.Marshal(head.id)
		return claimproducer.OpenOutcome{
			Available: true,
			Result: claimproducer.ClaimResult{
				Address:                json.RawMessage(addrBytes),
				Payload:                head.payload,
				ClaimScope:             json.RawMessage(scopeBytes),
				RealizedWriteSemantics: rws,
			},
		}, nil
	}
	addr, _ := json.Marshal(selector)
	scope, _ := json.Marshal(selector)
	return claimproducer.OpenOutcome{
		Available: true,
		Result: claimproducer.ClaimResult{
			Address:                json.RawMessage(addr),
			ClaimScope:             json.RawMessage(scope),
			RealizedWriteSemantics: rws,
		},
	}, nil
}

// realizedSemantics picks the realized value for an Open. The stub
// declares a singleton envelope, so the returned value is fixed across
// claims (satisfies the uniformity invariant trivially).
func (s *Store) realizedSemantics() claimproducer.WriteSemantics {
	if len(s.caps.WriteSemanticsAllowed) == 0 {
		return claimproducer.WriteSemanticsSync
	}
	return s.caps.WriteSemanticsAllowed[0]
}

// Commit records the call and applies the on_commit action. Lookup is
// claim_id-based (idempotent in claim_id per spec §7.8 obligation #3):
// a duplicated terminal RPC after the claim was already terminated
// finds no live state for the claim_id and is a no-op.
func (s *Store) Commit(_ context.Context, claimID string, scope, address []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, Call{
		Verb: "commit", ClaimID: claimID,
		Scope: cloneBytes(scope), Address: cloneBytes(address),
	})
	return s.applyPickActionByClaimID(claimID, true)
}

// Abandon records the call and applies the on_give_up action. Lookup
// is claim_id-based (idempotent per §7.8 obligation #3).
func (s *Store) Abandon(_ context.Context, claimID string, scope, address []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, Call{
		Verb: "abandon", ClaimID: claimID,
		Scope: cloneBytes(scope), Address: cloneBytes(address),
	})
	return s.applyPickActionByClaimID(claimID, false)
}

// Release records the call. No state to tear down for stub.
func (s *Store) Release(_ context.Context, claimID string, scope, address []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, Call{
		Verb: "release", ClaimID: claimID,
		Scope: cloneBytes(scope), Address: cloneBytes(address),
	})
	delete(s.claims, claimID)
	return nil
}

// applyPickActionByClaimID resolves the in-flight item via the
// claim_id → item_id map populated at Open. Idempotent per spec §7.8
// obligation #3: an unknown claim_id (already terminated, or never
// belonged to a pick policy) is a no-op. The configured default
// (defaultOnCommit when successPath, defaultOnGiveUp otherwise) is
// the only governing input — store-internal vocabulary excised from the
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
		var act action.Action
		if successPath {
			act = pp.defaultOnCommit
		} else {
			act = pp.defaultOnGiveUp
		}
		switch act.Kind {
		case action.Pop, action.PopAndMove, action.PopAndDelete:
			// Stub has no separate folder concept; all three "pop" variants
			// drop the in-flight entry. The distinction matters for
			// fs/pg-store mechanics but reduces to "drain queue entry"
			// here.
			delete(pp.inFlight, itemID)
		case action.Recycle:
			delete(pp.inFlight, itemID)
			pp.queue = append(pp.queue, it)
		default:
			return fmt.Errorf("stub store: applyPickAction: unknown action %q", act.Kind)
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
