// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package claimproducer

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/conformance/check"
)

func identSafeSelector(prefix string) string {
	u := uuid.New()
	return prefix + hex.EncodeToString(u[:8])
}

type CheckResult = check.Result

func Run(ctx context.Context, c claimproducer.ClaimProducer) []CheckResult {
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

	spec := claimproducer.ClaimSpec{
		ProducerName: "conformance-target",
		Selector:     "rimsky/conformance/uniformity",
		Intent:       claimproducer.IntentRead,
		Alias:        "conformance",
	}
	out1, err := c.Open(ctx, claimproducer.ClaimID(uuid.New().String()), spec)
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
	if out1.Result.RealizedWriteSemantics == claimproducer.WriteSemanticsUnknown {
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

	out2, err := c.Open(ctx, claimproducer.ClaimID(uuid.New().String()), spec)
	if err != nil {
		results = append(results, CheckResult{Name: "OpenSecond", Err: err})
		results = append(results, runOptionalChecks(ctx, c, caps)...)
		return results
	}
	if !out2.Available {
		results = append(results, CheckResult{Name: "OpenSecond"})
		results = append(results, runOptionalChecks(ctx, c, caps)...)
		return results
	}
	results = append(results, CheckResult{Name: "OpenSecond"})

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

func runOptionalChecks(ctx context.Context, c claimproducer.ClaimProducer, caps claimproducer.Capabilities) []CheckResult {
	out := make([]CheckResult, 0, 10)
	out = append(out, checkTerminals(ctx, c)...)
	out = append(out, checkSplitScope(ctx, c, caps)...)
	out = append(out, checkScopesConflict(ctx, c, caps)...)
	out = append(out, checkSerialization9b(ctx, c, caps))
	return out
}

type splitScopeListElement struct {
	key     string
	payload []byte
}

var splitScopeListProbeElements = []splitScopeListElement{
	{key: "alpha", payload: []byte(`{"v":1}`)},
	{key: "bravo", payload: []byte(`{"v":2}`)},
}

func splitScopeListProbeRequest() []byte {
	var buf bytes.Buffer
	buf.WriteString(`{"list":[`)
	for i, el := range splitScopeListProbeElements {
		if i > 0 {
			buf.WriteByte(',')
		}
		fmt.Fprintf(&buf, `{"key":%q,"payload":%s}`, el.key, string(el.payload))
	}
	buf.WriteString(`]}`)
	return buf.Bytes()
}

type rawSplitScopeProber interface {
	SplitScopeWire(ctx context.Context, req claimproducer.SplitClaimScopeRequest) (claimproducer.SplitClaimScopeResponse, error)
}

type rawScopesConflictProber interface {
	ScopesConflictWire(ctx context.Context, a, b []byte) (bool, error)
}

func checkSplitScope(ctx context.Context, c claimproducer.ClaimProducer, caps claimproducer.Capabilities) []CheckResult {
	if !caps.SupportsSplitScope {
		_, err := c.SplitScope(ctx, claimproducer.SplitClaimScopeRequest{ClaimHandleID: "probe"})
		if err == nil {
			return []CheckResult{{
				Name: "SplitScopeSkipped",
				Err:  fmt.Errorf("producer does not advertise SupportsSplitScope yet SplitScope returned nil error"),
			}}
		}
		if !errors.Is(err, claimproducer.ErrSplitScopeUnsupported) {
			if !containsErrorSubstring(err, "split_scope unsupported", "unsupported", "unimplemented") {
				return []CheckResult{{
					Name: "SplitScopeSkipped",
					Err:  fmt.Errorf("expected ErrSplitScopeUnsupported (or unimplemented status), got %v", err),
				}}
			}
		}
		results := []CheckResult{{Name: "SplitScopeSkipped"}}
		if prober, ok := c.(rawSplitScopeProber); ok {
			_, wireErr := prober.SplitScopeWire(ctx, claimproducer.SplitClaimScopeRequest{ClaimHandleID: "conformance-probe-nonexistent"})
			results = append(results, CheckResult{
				Name: "SplitScopeWireUnexercisedByUnsupportedClaim",
				Err:  requireWireFailsForUnknownClaim(wireErr, "SplitScope"),
			})
		}
		return results
	}

	claimID := claimproducer.ClaimID(uuid.New().String())
	openOut, err := c.Open(ctx, claimID, claimproducer.ClaimSpec{
		ProducerName: "conformance-target",
		Selector:     identSafeSelector("rimsky_conformance_split_scope_"),
		Intent:       claimproducer.IntentReadWrite,
		Alias:        "conformance-split-scope",
	})
	if err != nil {
		return []CheckResult{{Name: "SplitScope", Err: fmt.Errorf("parent Open failed: %w", err)}}
	}
	if !openOut.Available {
		return []CheckResult{{Name: "SplitScopeSkipped"}}
	}
	defer func() {
		_ = c.Release(ctx, claimID, openOut.Result.ClaimScope, openOut.Result.Address, "")
	}()

	req := claimproducer.SplitClaimScopeRequest{
		ClaimHandleID:    string(claimID),
		PartitionRequest: splitScopeListProbeRequest(),
	}
	resp, err := c.SplitScope(ctx, req)
	if err != nil {
		return []CheckResult{{Name: "SplitScopeListShapeProbeSkipped"}}
	}

	results := make([]CheckResult, 0, 4)

	if len(resp.SubClaimScopes) != len(splitScopeListProbeElements) {
		results = append(results, CheckResult{
			Name: "SplitScopeListShapeReturnsAllElements",
			Err: fmt.Errorf("expected %d sub-scopes (one per list element), got %d",
				len(splitScopeListProbeElements), len(resp.SubClaimScopes)),
		})
		return results
	}
	results = append(results, CheckResult{Name: "SplitScopeListShapeReturnsAllElements"})

	subByKey := make(map[string]claimproducer.SubClaimScopeDescriptor, len(resp.SubClaimScopes))
	for _, sub := range resp.SubClaimScopes {
		subByKey[sub.PartitionKey] = sub
	}

	keyMissing := ""
	for _, el := range splitScopeListProbeElements {
		if _, ok := subByKey[el.key]; !ok {
			keyMissing = el.key
			break
		}
	}
	if keyMissing != "" {
		results = append(results, CheckResult{
			Name: "SplitScopeListShapePreservesPartitionKey",
			Err: fmt.Errorf("input element key %q is not the PartitionKey of any returned sub-scope "+
				"(having accepted the conformance kit's list-shape probe, the producer must surface each "+
				"input element's key verbatim on its sub-scope)", keyMissing),
		})
	} else {
		results = append(results, CheckResult{Name: "SplitScopeListShapePreservesPartitionKey"})
	}

	payloadMismatch := ""
	for _, el := range splitScopeListProbeElements {
		sub, ok := subByKey[el.key]
		if !ok {
			continue
		}
		if !bytes.Equal(sub.Payload, el.payload) {
			payloadMismatch = fmt.Sprintf("key %q: expected payload bytes %q, got %q",
				el.key, string(el.payload), string(sub.Payload))
			break
		}
	}
	if payloadMismatch != "" {
		results = append(results, CheckResult{
			Name: "SplitScopeListShapePreservesPayload",
			Err: fmt.Errorf("%s (having accepted the conformance kit's list-shape probe, payload bytes must round-trip byte-equal)",
				payloadMismatch),
		})
	} else {
		results = append(results, CheckResult{Name: "SplitScopeListShapePreservesPayload"})
	}

	addrFailure := ""
	for _, sub := range resp.SubClaimScopes {
		if len(sub.Address) != 0 {
			addrFailure = fmt.Sprintf("sub-scope for key %q returned non-empty Address %q for the conformance kit's list-shape probe; "+
				"the probe shape carries data in payload only — address must be empty",
				sub.PartitionKey, string(sub.Address))
			break
		}
	}
	if addrFailure != "" {
		results = append(results, CheckResult{
			Name: "SplitScopeListShapeAddressFieldPresent",
			Err:  fmt.Errorf("%s", addrFailure),
		})
	} else {
		results = append(results, CheckResult{Name: "SplitScopeListShapeAddressFieldPresent"})
	}

	return results
}

func checkScopesConflict(ctx context.Context, c claimproducer.ClaimProducer, caps claimproducer.Capabilities) []CheckResult {
	if !caps.SupportsScopesConflict {
		got, err := c.ScopesConflict(ctx, []byte("a"), []byte("a"))
		if err != nil {
			if !errors.Is(err, claimproducer.ErrScopesConflictUnsupported) &&
				!containsErrorSubstring(err, "scopes_conflict unsupported", "unsupported", "unimplemented") {
				return []CheckResult{{
					Name: "ScopesConflictSkipped",
					Err:  fmt.Errorf("expected nil error (byte-equal fallback) or ErrScopesConflictUnsupported, got %v", err),
				}}
			}
			return []CheckResult{{Name: "ScopesConflictSkipped"}}
		}
		if !got {
			return []CheckResult{{
				Name: "ScopesConflictSkipped",
				Err:  fmt.Errorf("byte-equal fallback returned Conflicts=false; byte-equal scopes are required to conflict"),
			}}
		}
		results := []CheckResult{{Name: "ScopesConflictSkipped"}}
		if prober, ok := c.(rawScopesConflictProber); ok {
			wireGot, wireErr := prober.ScopesConflictWire(ctx, []byte("conformance-wire-probe"), []byte("conformance-wire-probe"))
			results = append(results, CheckResult{
				Name: "ScopesConflictWireByteEqualInvariant",
				Err:  requireWireConflictsOrUnimplemented(wireGot, wireErr),
			})
		}
		return results
	}
	scope := []byte(`{"k":"v"}`)
	different := []byte(`{"k":"different"}`)

	conflicts, err := c.ScopesConflict(ctx, scope, append([]byte{}, scope...))
	if err != nil {
		return []CheckResult{{Name: "ScopesConflict", Err: err}}
	}
	if !conflicts {
		return []CheckResult{{
			Name: "ScopesConflict",
			Err:  fmt.Errorf("byte-equal scopes returned Conflicts=false; producer-supplied conflict must agree with byte-equal on identical inputs"),
		}}
	}

	first, err := c.ScopesConflict(ctx, scope, different)
	if err != nil {
		return []CheckResult{{Name: "ScopesConflict", Err: err}}
	}
	second, err := c.ScopesConflict(ctx, scope, different)
	if err != nil {
		return []CheckResult{{Name: "ScopesConflict", Err: err}}
	}
	if second != first {
		return []CheckResult{{
			Name: "ScopesConflict",
			Err: fmt.Errorf("ScopesConflict(a, b) returned %v then %v on repeated calls with identical inputs; "+
				"the conflict predicate must be a deterministic function of its inputs, not random", first, second),
		}}
	}
	reversed, err := c.ScopesConflict(ctx, different, scope)
	if err != nil {
		return []CheckResult{{Name: "ScopesConflict", Err: err}}
	}
	if reversed != first {
		return []CheckResult{{
			Name: "ScopesConflict",
			Err: fmt.Errorf("ScopesConflict(a, b)=%v but ScopesConflict(b, a)=%v; the overlap predicate must be symmetric",
				first, reversed),
		}}
	}
	return []CheckResult{{Name: "ScopesConflict"}}
}

func requireWireFailsForUnknownClaim(err error, rpc string) error {
	if err == nil {
		return fmt.Errorf("%s wire call for a never-Open'd claim_handle_id returned nil error; "+
			"the producer must not fabricate a successful response for an unknown claim", rpc)
	}
	return nil
}

func requireWireConflictsOrUnimplemented(conflicts bool, err error) error {
	if err != nil {
		return nil
	}
	if !conflicts {
		return fmt.Errorf("wire ScopesConflict returned Conflicts=false for byte-equal non-empty scopes; " +
			"identical claim scopes must always conflict when the producer implements the RPC")
	}
	return nil
}

func containsErrorSubstring(err error, substrs ...string) bool {
	if err == nil {
		return false
	}
	if st, ok := status.FromError(err); ok && st.Code() == codes.Unimplemented {
		return true
	}
	msg := strings.ToLower(err.Error())
	for _, s := range substrs {
		if s == "" {
			continue
		}
		if strings.Contains(msg, strings.ToLower(s)) {
			return true
		}
	}
	return false
}
