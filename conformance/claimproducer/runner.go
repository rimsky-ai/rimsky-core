// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// Package claimproducer is the importable form of the ClaimProducer
// conformance suite. The cmd/rimsky-claim-producer-conformance binary
// is a thin wrapper that dials the endpoint and invokes Run; tests can
// invoke Run directly against an in-process or testcontainers-hosted
// producer to assert the standard suite passes against the target.
//
// Per spec
// .ok-planner/specs/2026-05-19-multi-instance-template-ergonomics-design.md
// §Item 6: the fused stores/postgres/ binary must pass the standard
// ClaimProducer conformance suite. This package is the mechanism that
// makes that assertion expressible from a Go test.
package claimproducer

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/fallguy/rimsky/foundation/locks"
	"github.com/fallguy/rimsky/protocols/claimproducer"
)

// CheckResult is one row of conformance output. Err is nil on success.
type CheckResult struct {
	Name string
	Err  error
}

// Run drives the ClaimProducer conformance checks against the supplied
// producer. Each check is independent; failures do not short-circuit
// so the caller sees the full surface.
//
// Mirrors `cmd/rimsky-claim-producer-conformance/main.go::RunClaimProducerConformance`.
// The binary delegates here so tests can invoke the same code path.
func Run(ctx context.Context, c locks.ClaimProducer) []CheckResult {
	results := make([]CheckResult, 0, 10)
	caps, err := c.Capabilities(ctx)
	if err != nil {
		results = append(results, CheckResult{Name: "Capabilities", Err: err})
		return results
	}
	results = append(results, CheckResult{Name: "Capabilities"})
	if len(caps.WriteSemanticsAllowed) == 0 {
		results = append(results, CheckResult{
			Name: "EnvelopeNonEmpty",
			Err:  fmt.Errorf("write_semantics_allowed is empty"),
		})
		results = append(results, runOptionalChecks(ctx, c, caps)...)
		return results
	}
	results = append(results, CheckResult{Name: "EnvelopeNonEmpty"})

	// Envelope conformance + uniformity-per-scope: drive Open twice with
	// identical specs and assert returned RealizedWriteSemantics is in
	// the envelope and identical across calls. Selectors are synthetic.
	spec := locks.ClaimSpec{
		ProducerName: "conformance-target",
		Selector:     "rimsky/conformance/uniformity",
		Intent:       locks.IntentRead,
		Alias:        "conformance",
	}
	out1, err := c.Open(ctx, locks.ClaimID(uuid.New().String()), spec)
	if err != nil {
		results = append(results, CheckResult{Name: "OpenFirst", Err: err})
		results = append(results, runOptionalChecks(ctx, c, caps)...)
		return results
	}
	if !out1.Available {
		results = append(results, CheckResult{
			Name: "OpenFirst",
			Err:  fmt.Errorf("producer returned Unavailable for synthetic selector — cannot exercise uniformity"),
		})
		results = append(results, runOptionalChecks(ctx, c, caps)...)
		return results
	}
	if out1.Result.RealizedWriteSemantics == locks.WriteSemanticsUnknown {
		results = append(results, CheckResult{
			Name: "OpenFirst",
			Err:  fmt.Errorf("RealizedWriteSemantics is empty/UNKNOWN; producer must declare a concrete value"),
		})
		results = append(results, runOptionalChecks(ctx, c, caps)...)
		return results
	}
	if !caps.Contains(out1.Result.RealizedWriteSemantics) {
		results = append(results, CheckResult{
			Name: "OpenFirst",
			Err: fmt.Errorf("RealizedWriteSemantics %q not in advertised envelope %v",
				out1.Result.RealizedWriteSemantics, caps.WriteSemanticsAllowed),
		})
		results = append(results, runOptionalChecks(ctx, c, caps)...)
		return results
	}
	results = append(results, CheckResult{Name: "OpenFirst"})

	out2, err := c.Open(ctx, locks.ClaimID(uuid.New().String()), spec)
	if err != nil {
		results = append(results, CheckResult{Name: "OpenSecond", Err: err})
		results = append(results, runOptionalChecks(ctx, c, caps)...)
		return results
	}
	if !out2.Available {
		// Some producers (pick-policy queues) drain after Open. Skip
		// the uniformity check rather than fail.
		results = append(results, CheckResult{Name: "OpenSecond"})
		results = append(results, runOptionalChecks(ctx, c, caps)...)
		return results
	}
	results = append(results, CheckResult{Name: "OpenSecond"})

	// Spec §2.5 uniformity: "byte-equal Scope MUST yield identical
	// RealizedWriteSemantics". The check only applies when the two
	// Open calls returned byte-equal Scope bytes — for pick-policy
	// producers two Open calls return DIFFERENT scope bytes
	// (different items), and asserting uniformity across non-byte-
	// equal scopes is stricter than the invariant requires.
	if !bytes.Equal(out1.Result.ClaimScope, out2.Result.ClaimScope) {
		results = append(results, runOptionalChecks(ctx, c, caps)...)
		return results
	}
	if out2.Result.RealizedWriteSemantics != out1.Result.RealizedWriteSemantics {
		results = append(results, CheckResult{
			Name: "Uniformity",
			Err: fmt.Errorf("byte-equal Scope did not produce identical RealizedWriteSemantics: %q vs %q",
				out1.Result.RealizedWriteSemantics, out2.Result.RealizedWriteSemantics),
		})
		results = append(results, runOptionalChecks(ctx, c, caps)...)
		return results
	}
	results = append(results, CheckResult{Name: "Uniformity"})

	results = append(results, runOptionalChecks(ctx, c, caps)...)
	return results
}

// runOptionalChecks runs the SplitScope + ScopesConflict probes. Each
// is gated on the producer's advertised Capabilities flag; when the
// producer does not advertise, the check surfaces a SKIP marker
// (CheckResult.Name=`<verb>Skipped` with Err=nil).
func runOptionalChecks(ctx context.Context, c locks.ClaimProducer, caps locks.Capabilities) []CheckResult {
	out := make([]CheckResult, 0, 2)
	out = append(out, checkSplitScope(ctx, c, caps))
	out = append(out, checkScopesConflict(ctx, c, caps))
	return out
}

func checkSplitScope(ctx context.Context, c locks.ClaimProducer, caps locks.Capabilities) CheckResult {
	if !caps.SupportsSplitScope {
		_, err := c.SplitScope(ctx, locks.SplitClaimScopeRequest{ClaimHandleID: "probe"})
		if err == nil {
			return CheckResult{
				Name: "SplitScopeSkipped",
				Err:  fmt.Errorf("producer does not advertise SupportsSplitScope yet SplitScope returned nil error"),
			}
		}
		if !errors.Is(err, claimproducer.ErrSplitScopeUnsupported) {
			if !containsErrorSubstring(err, "split_scope unsupported", "unsupported", "unimplemented") {
				return CheckResult{
					Name: "SplitScopeSkipped",
					Err:  fmt.Errorf("expected ErrSplitScopeUnsupported (or unimplemented status), got %v", err),
				}
			}
		}
		return CheckResult{Name: "SplitScopeSkipped"}
	}
	req := locks.SplitClaimScopeRequest{
		ClaimHandleID:    "rimsky/conformance/split-scope-probe",
		PartitionRequest: []byte(`{"partition_keys":["a","b","c"]}`),
	}
	resp, err := c.SplitScope(ctx, req)
	if err != nil {
		return CheckResult{Name: "SplitScope", Err: err}
	}
	if len(resp.SubClaimScopes) == 0 {
		return CheckResult{Name: "SplitScope", Err: fmt.Errorf("SplitScope returned zero sub-claim-scopes")}
	}
	for i, sub := range resp.SubClaimScopes {
		if sub.PartitionKey == "" {
			return CheckResult{
				Name: "SplitScope",
				Err:  fmt.Errorf("sub_scopes[%d].partition_key empty (producer must set a stable human-readable key)", i),
			}
		}
	}
	return CheckResult{Name: "SplitScope"}
}

func checkScopesConflict(ctx context.Context, c locks.ClaimProducer, caps locks.Capabilities) CheckResult {
	if !caps.SupportsScopesConflict {
		got, err := c.ScopesConflict(ctx, []byte("a"), []byte("a"))
		if err != nil {
			if !errors.Is(err, claimproducer.ErrScopesConflictUnsupported) &&
				!containsErrorSubstring(err, "scopes_conflict unsupported", "unsupported", "unimplemented") {
				return CheckResult{
					Name: "ScopesConflictSkipped",
					Err:  fmt.Errorf("expected nil error (byte-equal fallback) or ErrScopesConflictUnsupported, got %v", err),
				}
			}
			return CheckResult{Name: "ScopesConflictSkipped"}
		}
		if !got {
			return CheckResult{
				Name: "ScopesConflictSkipped",
				Err:  fmt.Errorf("byte-equal fallback returned Conflicts=false; @blessed-invariant 4b requires byte-equal scopes to conflict"),
			}
		}
		return CheckResult{Name: "ScopesConflictSkipped"}
	}
	scope := []byte(`{"k":"v"}`)
	conflicts, err := c.ScopesConflict(ctx, scope, append([]byte{}, scope...))
	if err != nil {
		return CheckResult{Name: "ScopesConflict", Err: err}
	}
	if !conflicts {
		return CheckResult{
			Name: "ScopesConflict",
			Err:  fmt.Errorf("byte-equal scopes returned Conflicts=false; producer-supplied conflict must agree with byte-equal on identical inputs"),
		}
	}
	if _, err := c.ScopesConflict(ctx, scope, []byte(`{"k":"different"}`)); err != nil {
		return CheckResult{Name: "ScopesConflict", Err: err}
	}
	return CheckResult{Name: "ScopesConflict"}
}

func containsErrorSubstring(err error, substrs ...string) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, s := range substrs {
		if s == "" {
			continue
		}
		if bytes.Contains([]byte(msg), []byte(s)) {
			return true
		}
	}
	return false
}
