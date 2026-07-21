// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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

type Store struct {
	caps         claimproducer.Capabilities
	pickPolicies map[string]*pickPolicy

	commitVersionID        string
	commitProducerMetadata []byte

	mu     sync.Mutex
	calls  []Call
	claims map[string]string
}

func (s *Store) CommitResponseFields() (string, []byte) {
	return s.commitVersionID, s.commitProducerMetadata
}

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

type Call struct {
	Verb     string
	ClaimID  string
	Selector string
	Intent   string
	Scope    []byte
	Address  []byte
}

type Config struct {
	Capabilities           claimproducer.Capabilities
	PickPolicies           map[string]PickPolicyConfig
	CommitVersionID        string
	CommitProducerMetadata []byte
}

type PickPolicyConfig struct {
	OnCommit         action.Action
	OnGiveUp         action.Action
	InitialItems     []json.RawMessage
	UnavailableClass string
}

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

func (s *Store) Capabilities() claimproducer.Capabilities { return s.caps }

func (s *Store) Open(_ context.Context, claimID, selector, intent string) (claimproducer.OpenOutcome, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, Call{Verb: "open", ClaimID: claimID, Selector: selector, Intent: intent})
	rws := s.realizedSemantics()
	if pp, ok := s.pickPolicies[selector]; ok {
		if len(pp.queue) == 0 {
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

func (s *Store) realizedSemantics() claimproducer.WriteSemantics {
	if len(s.caps.WriteSemanticsAllowed) == 0 {
		return claimproducer.WriteSemanticsSync
	}
	return s.caps.WriteSemanticsAllowed[0]
}

func (s *Store) Commit(_ context.Context, claimID string, scope, address []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, Call{
		Verb: "commit", ClaimID: claimID,
		Scope: cloneBytes(scope), Address: cloneBytes(address),
	})
	return s.applyPickActionByClaimID(claimID, true)
}

func (s *Store) Abandon(_ context.Context, claimID string, scope, address []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, Call{
		Verb: "abandon", ClaimID: claimID,
		Scope: cloneBytes(scope), Address: cloneBytes(address),
	})
	return s.applyPickActionByClaimID(claimID, false)
}

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

func (s *Store) Calls() []Call {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Call, len(s.calls))
	copy(out, s.calls)
	return out
}

func (s *Store) QueueLen(selector string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	pp, ok := s.pickPolicies[selector]
	if !ok {
		return -1
	}
	return len(pp.queue)
}

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
