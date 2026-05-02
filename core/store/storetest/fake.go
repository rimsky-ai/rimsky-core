package storetest

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"

	"github.com/fallguy/rimsky/core/store"
)

// fakeSequenceCounter is a process-global monotonic counter assigned
// to every FakeCall across every Fake instance. Used by tests that
// assert call ordering between two or more Fakes (e.g., that fan-out
// dispatched alpha before beta regardless of input order).
var fakeSequenceCounter atomic.Int64

func nextFakeSequence() int {
	return int(fakeSequenceCounter.Add(1))
}

// Fake is an in-memory store satisfying core/store.Store. Used by unit
// tests that exercise rimsky-side logic in isolation. State is
// per-Fake; not shared across instances.
type Fake struct {
	name string
	caps store.Capabilities

	mu    sync.Mutex
	state map[store.ClaimID]fakeState
	calls []FakeCall

	// OpenFunc is an optional override the test sets to control the
	// OpenOutcome returned by Open. Default behavior echoes the
	// selector as Address and Region (Available: true). Set to a
	// function that returns OpenOutcome{Available: false} to simulate
	// a store having no claim to give right now.
	OpenFunc func(claimID store.ClaimID, spec store.ClaimSpec) (store.OpenOutcome, error)

	// ErrorFunc is an optional override the test sets to inject
	// errors on a specific verb. Receives the verb name; returning
	// non-nil short-circuits the call.
	ErrorFunc func(verb string, claimID store.ClaimID) error
}

type fakeState struct {
	region  []byte
	address []byte
}

// FakeCall records one verb invocation. Tests assert against the
// Calls() slice to verify what fired.
type FakeCall struct {
	Verb       string
	ClaimID    store.ClaimID
	Selector   string
	Intent     store.Intent
	Region     []byte
	Address    []byte
	TemplateID string // populated for lifecycle calls and Open
	InstanceID string // populated for instance-scope lifecycle and Open
	// Sequence is a process-global monotonic counter assigned at the
	// moment the call was recorded. Use it to assert ordering across
	// multiple Fake instances (e.g., that fan-out called alpha before
	// beta regardless of input ordering).
	Sequence int
}

// NewFake returns an empty Fake under name with the given capabilities.
func NewFake(name string, caps store.Capabilities) *Fake {
	return &Fake{
		name:  name,
		caps:  caps,
		state: make(map[store.ClaimID]fakeState),
	}
}

// Compile-time interface check.
var _ store.Store = (*Fake)(nil)

// Name returns the operator-configured store name.
func (f *Fake) Name() string { return f.name }

// Capabilities returns the configured capability struct.
func (f *Fake) Capabilities(_ context.Context) (store.Capabilities, error) {
	return f.caps, nil
}

// Open records the call and returns a deterministic ClaimResult.
//
// User-supplied callbacks (OpenFunc, ErrorFunc) run AFTER f.mu is
// released so a callback that itself calls Calls() / Reset() / etc. on
// the same Fake won't deadlock. The recorder append (calls = append…)
// stays inside the lock; the post-record callback dispatch runs
// without the lock held; and on the default-behavior path the function
// briefly re-acquires f.mu to write the synthesized state into
// f.state before returning. Mirror this pattern in any future verbs
// that mutate state after a callback dispatch.
func (f *Fake) Open(_ context.Context, claimID store.ClaimID, spec store.ClaimSpec) (store.OpenOutcome, error) {
	f.mu.Lock()
	f.calls = append(f.calls, FakeCall{
		Verb:       "open",
		ClaimID:    claimID,
		Selector:   spec.Selector,
		Intent:     spec.Intent,
		TemplateID: spec.TemplateID,
		InstanceID: spec.InstanceID,
		Sequence:   nextFakeSequence(),
	})
	errFn := f.ErrorFunc
	openFn := f.OpenFunc
	f.mu.Unlock()

	if errFn != nil {
		if err := errFn("open", claimID); err != nil {
			return store.OpenOutcome{}, err
		}
	}
	if openFn != nil {
		return openFn(claimID, spec)
	}
	addr, _ := json.Marshal(spec.Selector)
	region, _ := json.Marshal(spec.Selector)
	outcome := store.OpenOutcome{
		Available: true,
		Result:    store.ClaimResult{Address: addr, Region: region},
	}
	f.mu.Lock()
	f.state[claimID] = fakeState{region: region, address: addr}
	f.mu.Unlock()
	return outcome, nil
}

// Commit records the call.
//
// User-supplied ErrorFunc runs AFTER f.mu is released so a callback that
// itself calls Calls() / Reset() / etc. on the same Fake won't deadlock.
// Mirrors the Open lock-discipline pattern.
func (f *Fake) Commit(_ context.Context, claimID store.ClaimID, region, address []byte) error {
	f.mu.Lock()
	f.calls = append(f.calls, FakeCall{
		Verb: "commit", ClaimID: claimID,
		Region: cloneBytes(region), Address: cloneBytes(address),
		Sequence: nextFakeSequence(),
	})
	errFn := f.ErrorFunc
	f.mu.Unlock()

	if errFn != nil {
		if err := errFn("commit", claimID); err != nil {
			return err
		}
	}
	f.mu.Lock()
	delete(f.state, claimID)
	f.mu.Unlock()
	return nil
}

// Abandon records the call.
//
// User-supplied ErrorFunc runs AFTER f.mu is released so a callback that
// itself calls Calls() / Reset() / etc. on the same Fake won't deadlock.
// Mirrors the Open lock-discipline pattern.
func (f *Fake) Abandon(_ context.Context, claimID store.ClaimID, region, address []byte) error {
	f.mu.Lock()
	f.calls = append(f.calls, FakeCall{
		Verb: "abandon", ClaimID: claimID,
		Region: cloneBytes(region), Address: cloneBytes(address),
		Sequence: nextFakeSequence(),
	})
	errFn := f.ErrorFunc
	f.mu.Unlock()

	if errFn != nil {
		if err := errFn("abandon", claimID); err != nil {
			return err
		}
	}
	f.mu.Lock()
	delete(f.state, claimID)
	f.mu.Unlock()
	return nil
}

// Release records the call.
//
// User-supplied ErrorFunc runs AFTER f.mu is released so a callback that
// itself calls Calls() / Reset() / etc. on the same Fake won't deadlock.
// Mirrors the Open lock-discipline pattern.
func (f *Fake) Release(_ context.Context, claimID store.ClaimID, region, address []byte) error {
	f.mu.Lock()
	f.calls = append(f.calls, FakeCall{
		Verb: "release", ClaimID: claimID,
		Region: cloneBytes(region), Address: cloneBytes(address),
		Sequence: nextFakeSequence(),
	})
	errFn := f.ErrorFunc
	f.mu.Unlock()

	if errFn != nil {
		if err := errFn("release", claimID); err != nil {
			return err
		}
	}
	f.mu.Lock()
	delete(f.state, claimID)
	f.mu.Unlock()
	return nil
}

// Calls returns a copy of the recorded call slice.
func (f *Fake) Calls() []FakeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]FakeCall, len(f.calls))
	copy(out, f.calls)
	return out
}

// Reset clears the recorded calls and the in-memory state.
func (f *Fake) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = nil
	f.state = make(map[store.ClaimID]fakeState)
}

// Lifecycle event methods. Each records a FakeCall and returns nil
// unless ErrorFunc is set for the matching verb.

func (f *Fake) OnTemplateRegistered(_ context.Context, templateID string) error {
	return f.recordLifecycle("on_template_registered", templateID, "")
}

func (f *Fake) OnTemplateDeployed(_ context.Context, templateID string) error {
	return f.recordLifecycle("on_template_deployed", templateID, "")
}

func (f *Fake) OnTemplateUndeployed(_ context.Context, templateID string) error {
	return f.recordLifecycle("on_template_undeployed", templateID, "")
}

func (f *Fake) OnTemplateDeregistered(_ context.Context, templateID string) error {
	return f.recordLifecycle("on_template_deregistered", templateID, "")
}

func (f *Fake) OnInstanceCreated(_ context.Context, templateID, instanceID string) error {
	return f.recordLifecycle("on_instance_created", templateID, instanceID)
}

func (f *Fake) OnInstanceTerminated(_ context.Context, templateID, instanceID string) error {
	return f.recordLifecycle("on_instance_terminated", templateID, instanceID)
}

func (f *Fake) recordLifecycle(verb, templateID, instanceID string) error {
	f.mu.Lock()
	f.calls = append(f.calls, FakeCall{
		Verb:       verb,
		TemplateID: templateID,
		InstanceID: instanceID,
		Sequence:   nextFakeSequence(),
	})
	errFn := f.ErrorFunc
	f.mu.Unlock()

	if errFn != nil {
		if err := errFn(verb, ""); err != nil {
			return err
		}
	}
	return nil
}

func cloneBytes(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out
}
