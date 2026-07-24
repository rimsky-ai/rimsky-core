// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package storetest

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/lifecycle"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

var fakeSequenceCounter atomic.Int64

func nextFakeSequence() int {
	return int(fakeSequenceCounter.Add(1))
}

type Fake struct {
	name string
	caps claimproducer.Capabilities

	mu    sync.Mutex
	calls []FakeCall

	OpenFunc func(claimID claimproducer.ClaimID, spec claimproducer.ClaimSpec) (claimproducer.OpenOutcome, error)

	ErrorFunc func(verb string, claimID claimproducer.ClaimID) error

	SplitClaimScopeFunc func(req claimproducer.SplitClaimScopeRequest) (claimproducer.SplitClaimScopeResponse, error)

	ScopesConflictFunc func(a, b []byte) (bool, error)

	CommitResult claimproducer.CommitResult
}

type FakeCall struct {
	Verb                  string
	ClaimID               claimproducer.ClaimID
	Selector              string
	Intent                claimproducer.Intent
	Alias                 string
	Lifetime              string
	Scope                 []byte
	Address               []byte
	TemplateID            string
	TemplateHash          string
	InstanceID            string
	InstanceKey           string
	Params                []byte
	RunScopeID            string
	LeaseToken            string
	TerminalReason        string
	TerminatedAtUnixMs    int64
	ServiceBindings       []byte
	OwnerAPIKeyID         string
	TargetRoutingIdentity string
	Sequence              int
}

func NewFake(name string, caps claimproducer.Capabilities) *Fake {
	return &Fake{
		name: name,
		caps: caps,
	}
}

var (
	_ locks.ClaimProducer  = (*Fake)(nil)
	_ lifecycle.Subscriber = (*Fake)(nil)
)

func (f *Fake) Name() string { return f.name }

func (f *Fake) Capabilities(_ context.Context) (claimproducer.Capabilities, error) {
	return f.caps, nil
}

func (f *Fake) Open(_ context.Context, claimID claimproducer.ClaimID, spec claimproducer.ClaimSpec) (claimproducer.OpenOutcome, error) {
	f.mu.Lock()
	f.calls = append(f.calls, FakeCall{
		Verb:       "open",
		ClaimID:    claimID,
		Selector:   spec.Selector,
		Intent:     spec.Intent,
		Alias:      spec.Alias,
		Lifetime:   spec.Lifetime,
		TemplateID: spec.TemplateID,
		InstanceID: spec.InstanceID,
		RunScopeID: spec.RunScopeID,
		Sequence:   nextFakeSequence(),
	})
	errFn := f.ErrorFunc
	openFn := f.OpenFunc
	caps := f.caps
	f.mu.Unlock()

	if errFn != nil {
		if err := errFn("open", claimID); err != nil {
			return claimproducer.OpenOutcome{}, err
		}
	}
	if openFn != nil {
		return openFn(claimID, spec)
	}
	addr, _ := json.Marshal(spec.Selector)
	scope, _ := json.Marshal(spec.Selector)
	var rws claimproducer.WriteSemantics
	if len(caps.WriteSemanticsAllowed) > 0 {
		rws = caps.WriteSemanticsAllowed[0]
	}
	outcome := claimproducer.OpenOutcome{
		Available: true,
		Result: claimproducer.ClaimResult{
			Address:                addr,
			ClaimScope:             scope,
			RealizedWriteSemantics: rws,
		},
	}
	return outcome, nil
}

func (f *Fake) Commit(_ context.Context, claimID claimproducer.ClaimID, scope, address []byte, leaseToken string) (claimproducer.CommitResult, error) {
	f.mu.Lock()
	f.calls = append(f.calls, FakeCall{
		Verb: "commit", ClaimID: claimID,
		Scope: cloneBytes(scope), Address: cloneBytes(address),
		LeaseToken: leaseToken,
		Sequence:   nextFakeSequence(),
	})
	errFn := f.ErrorFunc
	res := f.CommitResult
	f.mu.Unlock()

	if errFn != nil {
		if err := errFn("commit", claimID); err != nil {
			return claimproducer.CommitResult{}, err
		}
	}
	return res, nil
}

func (f *Fake) Abandon(_ context.Context, claimID claimproducer.ClaimID, scope, address []byte, leaseToken string) error {
	f.mu.Lock()
	f.calls = append(f.calls, FakeCall{
		Verb: "abandon", ClaimID: claimID,
		Scope: cloneBytes(scope), Address: cloneBytes(address),
		LeaseToken: leaseToken,
		Sequence:   nextFakeSequence(),
	})
	errFn := f.ErrorFunc
	f.mu.Unlock()

	if errFn != nil {
		if err := errFn("abandon", claimID); err != nil {
			return err
		}
	}
	return nil
}

func (f *Fake) Release(_ context.Context, claimID claimproducer.ClaimID, scope, address []byte, leaseToken string) error {
	f.mu.Lock()
	f.calls = append(f.calls, FakeCall{
		Verb: "release", ClaimID: claimID,
		Scope: cloneBytes(scope), Address: cloneBytes(address),
		LeaseToken: leaseToken,
		Sequence:   nextFakeSequence(),
	})
	errFn := f.ErrorFunc
	f.mu.Unlock()

	if errFn != nil {
		if err := errFn("release", claimID); err != nil {
			return err
		}
	}
	return nil
}

func (f *Fake) Calls() []FakeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]FakeCall, len(f.calls))
	copy(out, f.calls)
	return out
}

func (f *Fake) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = nil
}

func (f *Fake) OnTemplateRegistered(_ context.Context, req lifecycle.OnTemplateRegisteredRequest) error {
	return f.recordLifecycle("on_template_registered", req.TemplateHash, "")
}

func (f *Fake) OnTemplateDeployed(_ context.Context, req lifecycle.OnTemplateDeployedRequest) error {
	return f.recordLifecycle("on_template_deployed", req.TemplateHash, "")
}

func (f *Fake) OnTemplateUndeployed(_ context.Context, req lifecycle.OnTemplateUndeployedRequest) error {
	return f.recordLifecycle("on_template_undeployed", req.TemplateHash, "")
}

func (f *Fake) OnTemplateDeregistered(_ context.Context, req lifecycle.OnTemplateDeregisteredRequest) error {
	return f.recordLifecycle("on_template_deregistered", req.TemplateHash, "")
}

func (f *Fake) OnInstanceCreated(_ context.Context, req lifecycle.OnInstanceCreatedRequest) error {
	return f.recordLifecycleCall(FakeCall{
		Verb:                  "on_instance_created",
		TemplateHash:          req.TemplateHash,
		InstanceID:            req.InstanceID,
		InstanceKey:           req.InstanceKey,
		Params:                cloneBytes(req.Params),
		ServiceBindings:       cloneBytes(req.ServiceBindings),
		OwnerAPIKeyID:         req.OwnerAPIKeyID,
		TargetRoutingIdentity: req.TargetRoutingIdentity,
	})
}

func (f *Fake) OnInstanceTerminated(_ context.Context, req lifecycle.OnInstanceTerminatedRequest) error {
	return f.recordLifecycleCall(FakeCall{
		Verb:               "on_instance_terminated",
		TemplateHash:       req.TemplateHash,
		InstanceID:         req.InstanceID,
		TerminatedAtUnixMs: req.TerminatedAtUnixMs,
	})
}

func (f *Fake) OnRunScopeTerminal(_ context.Context, req lifecycle.OnRunScopeTerminalRequest) error {
	return f.recordLifecycleCall(FakeCall{
		Verb:           "on_run_scope_terminal",
		RunScopeID:     req.RunScopeID,
		TerminalReason: req.TerminalReason,
		InstanceID:     req.InstanceID,
	})
}

func (f *Fake) SplitScope(_ context.Context, req claimproducer.SplitClaimScopeRequest) (claimproducer.SplitClaimScopeResponse, error) {
	f.mu.Lock()
	f.calls = append(f.calls, FakeCall{
		Verb:     "split_scope",
		Sequence: nextFakeSequence(),
	})
	fn := f.SplitClaimScopeFunc
	f.mu.Unlock()
	if fn == nil {
		return claimproducer.SplitClaimScopeResponse{}, claimproducer.ErrSplitScopeUnsupported
	}
	return fn(req)
}

func (f *Fake) ScopesConflict(_ context.Context, a, b []byte) (bool, error) {
	f.mu.Lock()
	f.calls = append(f.calls, FakeCall{
		Verb:     "scopes_conflict",
		Sequence: nextFakeSequence(),
	})
	fn := f.ScopesConflictFunc
	supportsScopesConflict := f.caps.SupportsScopesConflict
	f.mu.Unlock()
	if fn != nil {
		return fn(a, b)
	}
	if !supportsScopesConflict {
		return false, claimproducer.ErrScopesConflictUnsupported
	}
	return locks.ClaimScopesByteEqual(a, b), nil
}

func (f *Fake) recordLifecycle(verb, templateHash, instanceID string) error {
	return f.recordLifecycleCall(FakeCall{
		Verb:         verb,
		TemplateHash: templateHash,
		InstanceID:   instanceID,
	})
}

func (f *Fake) recordLifecycleCall(call FakeCall) error {
	f.mu.Lock()
	call.Sequence = nextFakeSequence()
	f.calls = append(f.calls, call)
	errFn := f.ErrorFunc
	f.mu.Unlock()

	if errFn != nil {
		if err := errFn(call.Verb, ""); err != nil {
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
