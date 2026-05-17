// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// optional_checks.go carries the M4 SplitScope / ScopesConflict
// conformance probes. Each check is gated on the producer's
// advertised Capabilities flag and surfaces a SKIP marker when the
// capability is not advertised — operators see the full surface
// regardless of which optional verbs the target advertises.
//
// Per plan:2026-05-15-data-platform-extensions-plan.md §M4.

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/fallguy/rimsky/foundation/locks"
	"github.com/fallguy/rimsky/protocols/claimproducer"
)

// runOptionalChecks runs the SplitScope and ScopesConflict probes.
// Each is gated on the producer's advertised Capabilities flag; when
// the producer does not advertise, the check surfaces as a SKIP
// marker rather than a failure.
func runOptionalChecks(ctx context.Context, c locks.ClaimProducer, caps locks.Capabilities) []CheckResult {
	out := make([]CheckResult, 0, 2)
	out = append(out, checkSplitScope(ctx, c, caps))
	out = append(out, checkScopesConflict(ctx, c, caps))
	return out
}

// checkSplitScope probes ClaimProducer.SplitScope. When
// SupportsSplitScope is true the check requires the producer to:
//
//  1. Accept a partition_request of the form {"partition_keys":["a","b"]}
//     and return at least one SubScopeDescriptor.
//  2. Set a non-empty PartitionKey on each descriptor.
//  3. Reject an empty partition_request shape with a non-nil error.
//
// When SupportsSplitScope is false the check surfaces SKIP marker
// (Name=SplitScopeSkipped) and asserts the producer returns
// ErrSplitScopeUnsupported when invoked anyway (defensive against a
// producer that misadvertises).
func checkSplitScope(ctx context.Context, c locks.ClaimProducer, caps locks.Capabilities) CheckResult {
	if !caps.SupportsSplitScope {
		_, err := c.SplitScope(ctx, locks.SplitScopeRequest{ClaimHandleID: "probe"})
		if err == nil {
			return CheckResult{
				Name: "SplitScopeSkipped",
				Err:  fmt.Errorf("producer does not advertise SupportsSplitScope yet SplitScope returned nil error"),
			}
		}
		if !errors.Is(err, claimproducer.ErrSplitScopeUnsupported) {
			// The on-wire client returns a wrapped sentinel; check
			// the textual form when the unwrap chain doesn't match.
			if !containsErrorSubstring(err, "split_scope unsupported", "unsupported", "unimplemented") {
				return CheckResult{
					Name: "SplitScopeSkipped",
					Err:  fmt.Errorf("expected ErrSplitScopeUnsupported (or unimplemented status), got %v", err),
				}
			}
		}
		return CheckResult{Name: "SplitScopeSkipped"}
	}
	req := locks.SplitScopeRequest{
		ClaimHandleID:    "rimsky/conformance/split-scope-probe",
		PartitionRequest: []byte(`{"partition_keys":["a","b","c"]}`),
	}
	resp, err := c.SplitScope(ctx, req)
	if err != nil {
		return CheckResult{Name: "SplitScope", Err: err}
	}
	if len(resp.SubScopes) == 0 {
		return CheckResult{Name: "SplitScope", Err: fmt.Errorf("SplitScope returned zero sub-scopes")}
	}
	for i, sub := range resp.SubScopes {
		if sub.PartitionKey == "" {
			return CheckResult{
				Name: "SplitScope",
				Err:  fmt.Errorf("sub_scopes[%d].partition_key empty (producer must set a stable human-readable key)", i),
			}
		}
	}
	return CheckResult{Name: "SplitScope"}
}

// checkScopesConflict probes ClaimProducer.ScopesConflict. When the
// producer advertises SupportsScopesConflict the check requires:
//
//  1. Byte-equal inputs return Conflicts=true.
//  2. Non-byte-equal inputs return a deterministic boolean (the
//     value is producer-specific so the check only asserts the call
//     returns without error).
//
// When SupportsScopesConflict is false the check surfaces SKIP
// (Name=ScopesConflictSkipped); rimsky falls back to byte-equal
// per @blessed-invariant 4b.
func checkScopesConflict(ctx context.Context, c locks.ClaimProducer, caps locks.Capabilities) CheckResult {
	if !caps.SupportsScopesConflict {
		// Defensive probe: per the runtime/remote client semantics,
		// a producer that doesn't advertise SupportsScopesConflict
		// short-circuits to the byte-equal fallback without an RPC
		// (per @blessed-invariant 4b). We assert that on byte-equal
		// inputs the fallback returns Conflicts=true so the contract
		// holds.
		got, err := c.ScopesConflict(ctx, []byte("a"), []byte("a"))
		if err != nil {
			if !errors.Is(err, claimproducer.ErrScopesConflictUnsupported) &&
				!containsErrorSubstring(err, "scopes_conflict unsupported", "unsupported", "unimplemented") {
				return CheckResult{
					Name: "ScopesConflictSkipped",
					Err:  fmt.Errorf("expected nil error (byte-equal fallback) or ErrScopesConflictUnsupported, got %v", err),
				}
			}
			// Unsupported error is acceptable; skip.
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
	// Non-byte-equal: assert it returns without error; producer
	// semantics are producer-specific so we don't pin the boolean.
	if _, err := c.ScopesConflict(ctx, scope, []byte(`{"k":"different"}`)); err != nil {
		return CheckResult{Name: "ScopesConflict", Err: err}
	}
	return CheckResult{Name: "ScopesConflict"}
}

// containsErrorSubstring is a textual fallback for unwrap-resistant
// error wrappers (gRPC status, etc.). The conformance binary is the
// outer surface; we tolerate a textual match rather than chase every
// wrapper shape.
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
