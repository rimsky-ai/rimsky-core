// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

package claimproducer

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

type scopesConflictFake struct {
	scopesConflictFunc func(a, b []byte) (bool, error)
}

func (f *scopesConflictFake) Name() string { return "conformance-target" }

func (f *scopesConflictFake) Capabilities(context.Context) (claimproducer.Capabilities, error) {
	return claimproducer.Capabilities{
		WriteSemanticsAllowed:  []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
		SupportsScopesConflict: true,
	}, nil
}

func (f *scopesConflictFake) Open(_ context.Context, _ claimproducer.ClaimID, spec claimproducer.ClaimSpec) (claimproducer.OpenOutcome, error) {
	scope, _ := json.Marshal(spec.Selector)
	return claimproducer.OpenOutcome{
		Available: true,
		Result: claimproducer.ClaimResult{
			Address:                scope,
			ClaimScope:             scope,
			RealizedWriteSemantics: claimproducer.WriteSemanticsSync,
		},
	}, nil
}

func (f *scopesConflictFake) Commit(context.Context, claimproducer.ClaimID, []byte, []byte, string) (claimproducer.CommitResult, error) {
	return claimproducer.CommitResult{}, nil
}

func (f *scopesConflictFake) Abandon(context.Context, claimproducer.ClaimID, []byte, []byte, string) error {
	return nil
}

func (f *scopesConflictFake) Release(context.Context, claimproducer.ClaimID, []byte, []byte, string) error {
	return nil
}

func (f *scopesConflictFake) SplitScope(context.Context, claimproducer.SplitClaimScopeRequest) (claimproducer.SplitClaimScopeResponse, error) {
	return claimproducer.SplitClaimScopeResponse{}, claimproducer.ErrSplitScopeUnsupported
}

func (f *scopesConflictFake) ScopesConflict(_ context.Context, a, b []byte) (bool, error) {
	return f.scopesConflictFunc(a, b)
}

func TestCheckScopesConflict_Honest_Passes(t *testing.T) {
	fp := &scopesConflictFake{
		scopesConflictFunc: func(a, b []byte) (bool, error) {
			return bytes.Equal(a, b), nil
		},
	}
	results := Run(context.Background(), fp)
	row := findRow(t, results, "ScopesConflict")
	if row.Err != nil {
		t.Errorf("ScopesConflict row expected to PASS for honest byte-equal predicate, got Err: %v", row.Err)
	}
}

func TestCheckScopesConflict_ConstantTrue_NondeterminismUndetected(t *testing.T) {
	fp := &scopesConflictFake{
		scopesConflictFunc: func(a, b []byte) (bool, error) {
			return true, nil
		},
	}
	results := Run(context.Background(), fp)
	row := findRow(t, results, "ScopesConflict")
	if row.Err != nil {
		t.Errorf("ScopesConflict row expected to PASS for a deterministic (if overly broad) constant-true predicate, got Err: %v", row.Err)
	}
}

func TestCheckScopesConflict_Random_Fails(t *testing.T) {
	fp := &scopesConflictFake{
		scopesConflictFunc: func() func(a, b []byte) (bool, error) {
			toggle := false
			return func(a, b []byte) (bool, error) {
				if bytes.Equal(a, b) {
					return true, nil
				}
				toggle = !toggle
				return toggle, nil
			}
		}(),
	}
	results := Run(context.Background(), fp)
	row := findRow(t, results, "ScopesConflict")
	if row.Err == nil {
		t.Errorf("ScopesConflict row expected non-nil Err for a predicate that answers differently across repeated calls with identical inputs, got PASS")
	}
}

func TestCheckScopesConflict_Asymmetric_Fails(t *testing.T) {
	fp := &scopesConflictFake{
		scopesConflictFunc: func(a, b []byte) (bool, error) {
			if bytes.Equal(a, b) {
				return true, nil
			}
			return string(a) < string(b), nil
		},
	}
	results := Run(context.Background(), fp)
	row := findRow(t, results, "ScopesConflict")
	if row.Err == nil {
		t.Errorf("ScopesConflict row expected non-nil Err for an asymmetric predicate (conflicts(a,b) != conflicts(b,a)), got PASS")
	}
}

type wireProbingScopesConflictFake struct {
	*scopesConflictFake
	supportsScopesConflict bool
	wireCalled             bool
	wireConflicts          bool
	wireErr                error
}

func (f *wireProbingScopesConflictFake) Capabilities(context.Context) (claimproducer.Capabilities, error) {
	return claimproducer.Capabilities{
		WriteSemanticsAllowed:  []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
		SupportsScopesConflict: f.supportsScopesConflict,
	}, nil
}

func (f *wireProbingScopesConflictFake) ScopesConflictWire(_ context.Context, _, _ []byte) (bool, error) {
	f.wireCalled = true
	return f.wireConflicts, f.wireErr
}

func TestCheckScopesConflict_SupportsFalse_WireProbeExercisesTargetDirectly(t *testing.T) {
	fake := &wireProbingScopesConflictFake{
		scopesConflictFake: &scopesConflictFake{
			scopesConflictFunc: func(a, b []byte) (bool, error) {
				return claimproducer.ErrScopesConflictUnsupportedFallback(a, b), nil
			},
		},
		wireErr: claimproducer.ErrScopesConflictUnsupported,
	}
	results := Run(context.Background(), fake)
	if !fake.wireCalled {
		t.Fatalf("ScopesConflictWire was never invoked; the supports=false negative probe must exercise the producer's wire directly, not only the client-side byte-equal fallback")
	}
	row := findRow(t, results, "ScopesConflictWireByteEqualInvariant")
	if row.Err != nil {
		t.Errorf("expected PASS when the producer's wire declines scopes_conflict as unimplemented, got Err: %v", row.Err)
	}
}

func TestCheckScopesConflict_SupportsFalse_WireProbeFlagsBrokenImplementation(t *testing.T) {
	fake := &wireProbingScopesConflictFake{
		scopesConflictFake: &scopesConflictFake{
			scopesConflictFunc: func(a, b []byte) (bool, error) {
				return claimproducer.ErrScopesConflictUnsupportedFallback(a, b), nil
			},
		},
		wireConflicts: false,
		wireErr:       nil,
	}
	results := Run(context.Background(), fake)
	row := findRow(t, results, "ScopesConflictWireByteEqualInvariant")
	if row.Err == nil {
		t.Errorf("expected non-nil Err when the producer's wire implements ScopesConflict but returns Conflicts=false for byte-equal non-empty scopes, got PASS")
	}
}
