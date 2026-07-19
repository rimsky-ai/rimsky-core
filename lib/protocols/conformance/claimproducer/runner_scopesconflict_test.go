// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

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

func (f *scopesConflictFake) Commit(context.Context, claimproducer.ClaimID, []byte, []byte) (claimproducer.CommitResult, error) {
	return claimproducer.CommitResult{}, nil
}

func (f *scopesConflictFake) Abandon(context.Context, claimproducer.ClaimID, []byte, []byte) error {
	return nil
}

func (f *scopesConflictFake) Release(context.Context, claimproducer.ClaimID, []byte, []byte) error {
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
