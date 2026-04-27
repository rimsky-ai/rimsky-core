// In-process stub store. Holds state in memory; no postgres, no
// filesystem. Suitable for scenario tests that exercise runner /
// state-machine semantics without standing up real store
// infrastructure. Implements the five-verb store.Store interface
// (spec §11.5).

package stub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"sync"

	"github.com/fallguy/rimsky/core/store"
)

// Store is the in-memory stub store. State held entirely in Store
// itself; no external resources. Two operating modes:
//
//   - Region-direct mode (default): selectors map directly to opaque
//     region tokens. Tokens conflict iff equal (or contain a common
//     element when serialized as JSON arrays). Open returns the
//     selector verbatim as Address/Region; Commit/Abandon/Delete are
//     recorder no-ops.
//
//   - Pick-policy mode: when a selector matches a configured pick-
//     policy key (e.g. "@queue"), Open pops the policy's FIFO queue,
//     returns the picked item's address + payload + region (the chosen
//     item id). Commit/Abandon honor the policyOverride to release-to-
//     back / release-to-head / delete.
//
// Capabilities are operator-configured at construction. Defaults are
// {WriteSemantics: direct}.
//
// Thread-safety: a single sync.Mutex protects all mutable state.
type Store struct {
	name         string
	kind         string
	capabilities store.Capabilities

	mu sync.Mutex

	// per-pick-policy in-memory state. Keyed by recognized selector
	// prefix (e.g. "@queue", "@ring"). When a selector matches a key,
	// Open invokes that policy.
	pickPolicies map[string]*pickPolicy

	// Recorder for test assertions. Each entry records one verb call.
	calls []Call
}

// pickPolicy is an in-memory FIFO queue + in-flight set, used to model
// substrate-side pick-policy semantics in tests.
type pickPolicy struct {
	queue           []item          // available items, head at index 0
	inFlight        map[string]item // address-as-string → item
	defaultOnCommit string          // "delete" | "release_to_back" | "release_to_head"
	defaultOnGiveUp string
	nextSeq         int
}

type item struct {
	id      string
	payload json.RawMessage
}

// Call records one invocation of a verb. Tests use Calls() to assert
// what fired.
type Call struct {
	Verb           string // "open" | "commit" | "abandon" | "delete" | "release"
	Selector       string // empty unless verb is "open"
	Intent         store.Intent
	Region         json.RawMessage
	Address        json.RawMessage
	PolicyOverride string
}

// Compile-time interface check.
var _ store.Store = (*Store)(nil)

// newStore constructs a fresh *Store with the given identity and
// capability flags. State maps initialised so callers can use the
// store immediately without further setup.
func newStore(name, kind string, caps store.Capabilities) *Store {
	return &Store{
		name:         name,
		kind:         kind,
		capabilities: caps,
		pickPolicies: make(map[string]*pickPolicy),
	}
}

// Name returns the operator-configured store name.
func (s *Store) Name() string { return s.name }

// Kind returns the configured kind string. Tests can register the stub
// under a production kind by editing the registry.
func (s *Store) Kind() string { return s.kind }

// Capabilities returns the configured capability struct.
func (s *Store) Capabilities() store.Capabilities { return s.capabilities }

// RegionsConflict reports whether two stub regions overlap. The grammar
// is a JSON []string of opaque tokens; two regions conflict iff they
// share any token. Wrong-shape inputs are treated as conflicting
// (fail-closed).
func (s *Store) RegionsConflict(a, b []byte) bool {
	ga, errA := decodeRegion(a)
	gb, errB := decodeRegion(b)
	if errA != nil || errB != nil {
		return true
	}
	set := make(map[string]struct{}, len(ga))
	for _, t := range ga {
		set[t] = struct{}{}
	}
	for _, t := range gb {
		if _, ok := set[t]; ok {
			return true
		}
	}
	return false
}

// UnmarshalRegion is a no-op for the stub: callers pass raw bytes
// straight to RegionsConflict, so the canonical form here is the input
// bytes themselves (after a copy to avoid aliasing).
func (s *Store) UnmarshalRegion(raw []byte) ([]byte, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make([]byte, len(raw))
	copy(out, raw)
	return out, nil
}

// Open produces a substrate-native address. For pick-policy selectors,
// pops the configured policy's queue. For other selectors, echoes the
// selector verbatim as Address and Region (region-direct mode).
func (s *Store) Open(_ context.Context, spec store.ClaimSpec) (store.ClaimResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, Call{Verb: "open", Selector: spec.Selector, Intent: spec.Intent})

	if pp, ok := s.pickPolicies[spec.Selector]; ok {
		if len(pp.queue) == 0 {
			return store.ClaimResult{}, nil
		}
		head := pp.queue[0]
		pp.queue = pp.queue[1:]
		addrBytes, _ := json.Marshal(head.id)
		regionBytes, _ := json.Marshal(head.id)
		pp.inFlight[head.id] = head
		return store.ClaimResult{
			Address: json.RawMessage(addrBytes),
			Payload: head.payload,
			Region:  json.RawMessage(regionBytes),
		}, nil
	}
	// Region-direct mode: selector is the region.
	addr, _ := json.Marshal(spec.Selector)
	region, _ := json.Marshal(spec.Selector)
	return store.ClaimResult{
		Address: json.RawMessage(addr),
		Region:  json.RawMessage(region),
	}, nil
}

// Commit records the call and applies the pick-policy on_commit action
// (delete / release_to_back / release_to_head) when the region matches
// an in-flight pick-policy item.
func (s *Store) Commit(_ context.Context, region []byte, address []byte, policyOverride string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, Call{
		Verb: "commit", Region: copyBytes(region), Address: copyBytes(address),
		PolicyOverride: policyOverride,
	})
	return s.applyPickAction(region, policyOverride, true)
}

// Abandon records the call and applies the on_give_up action.
func (s *Store) Abandon(_ context.Context, region []byte, address []byte, policyOverride string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, Call{
		Verb: "abandon", Region: copyBytes(region), Address: copyBytes(address),
		PolicyOverride: policyOverride,
	})
	return s.applyPickAction(region, policyOverride, false)
}

// Delete records the call. For pick-policy items it removes the
// in-flight entry without re-queueing. For region-direct it is a no-op.
func (s *Store) Delete(_ context.Context, region []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, Call{Verb: "delete", Region: copyBytes(region)})
	itemID, ok := decodeItemID(region)
	if !ok {
		return nil
	}
	for _, pp := range s.pickPolicies {
		delete(pp.inFlight, itemID)
	}
	return nil
}

// Release records the call. For pick-policy claims at v1 there is no
// substrate-side state to tear down (Open is the only state-registering
// step); for staged_async substrates this would dispose of read-side
// state. No v1 store implementation registers such state.
func (s *Store) Release(_ context.Context, region []byte, address []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, Call{
		Verb: "release", Region: copyBytes(region), Address: copyBytes(address),
	})
	return nil
}

// applyPickAction looks up the in-flight item matching region and
// applies the requested action. successPath=true uses on_commit defaults
// when policyOverride is empty; successPath=false uses on_give_up.
func (s *Store) applyPickAction(region []byte, policyOverride string, successPath bool) error {
	itemID, ok := decodeItemID(region)
	if !ok {
		return nil
	}
	for _, pp := range s.pickPolicies {
		it, ok := pp.inFlight[itemID]
		if !ok {
			continue
		}
		action := policyOverride
		if action == "" {
			if successPath {
				action = pp.defaultOnCommit
			} else {
				action = pp.defaultOnGiveUp
			}
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
			return fmt.Errorf("stub store %q: applyPickAction: unknown action %q", s.name, action)
		}
		return nil
	}
	return nil
}

// SeedPickPolicyItem appends a payload to a configured pick-policy's
// FIFO queue and returns the assigned item id. Test helper.
func (s *Store) SeedPickPolicyItem(selector string, payload json.RawMessage) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pp, ok := s.pickPolicies[selector]
	if !ok {
		return "", fmt.Errorf("stub store %q: SeedPickPolicyItem: no policy for selector %q", s.name, selector)
	}
	pp.nextSeq++
	id := fmt.Sprintf("stub-%s-%d", selector, pp.nextSeq)
	pp.queue = append(pp.queue, item{id: id, payload: payload})
	return id, nil
}

// ConfigurePickPolicy registers a pick-policy under the given selector
// key. Tests call this to enable pick-policy behaviour. Calling twice
// for the same key replaces the prior config.
func (s *Store) ConfigurePickPolicy(selector, defaultOnCommit, defaultOnGiveUp string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pickPolicies[selector] = &pickPolicy{
		inFlight:        make(map[string]item),
		defaultOnCommit: defaultOnCommit,
		defaultOnGiveUp: defaultOnGiveUp,
	}
}

// Calls returns a copy of the recorder slice. Test helper.
func (s *Store) Calls() []Call {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Call, len(s.calls))
	copy(out, s.calls)
	return out
}

// QueueLen returns the current FIFO queue length for the named
// pick-policy. Test helper. Returns -1 if no policy is configured for
// the selector.
func (s *Store) QueueLen(selector string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	pp, ok := s.pickPolicies[selector]
	if !ok {
		return -1
	}
	return len(pp.queue)
}

// InFlight returns the set of currently-in-flight item IDs for the
// named pick-policy, sorted for deterministic test assertions. Returns
// nil if no policy is configured.
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

// decodeRegion attempts to decode region bytes as a JSON []string. On
// failure (empty / wrong shape) returns ([], err). Used by
// RegionsConflict.
func decodeRegion(b []byte) ([]string, error) {
	if len(b) == 0 {
		return nil, nil
	}
	var s []string
	if err := json.Unmarshal(b, &s); err == nil {
		return s, nil
	}
	// Try as a single string (selector-as-region from Open).
	var one string
	if err := json.Unmarshal(b, &one); err != nil {
		return nil, err
	}
	return []string{one}, nil
}

// decodeItemID extracts a string item ID from region bytes if they
// encode a single JSON string (as Open writes for pick-policy claims).
func decodeItemID(b []byte) (string, bool) {
	if len(b) == 0 {
		return "", false
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return "", false
	}
	return s, true
}

// copyBytes returns a fresh copy of the input slice. Used in the
// recorder so callers can mutate input slices without corrupting
// recorded calls.
func copyBytes(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out
}

// stringifySeq is a tiny helper for older test paths that may still
// expect synthetic IDs in "stub-N" form. Retained for parity though
// the new SeedPickPolicyItem mints "stub-<selector>-N".
func stringifySeq(n int) string { return strconv.Itoa(n) }

// errInvariant is a sentinel for stub-side invariant checks. Currently
// unused; placeholder in case the recorder grows internal-consistency
// asserts.
var errInvariant = errors.New("stub store: invariant violation")

var _ = stringifySeq
var _ = errInvariant
