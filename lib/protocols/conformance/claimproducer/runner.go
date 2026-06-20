// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package claimproducer

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

type CheckResult struct {
	Name string
	Err  error
}

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
	out = append(out, checkScopesConflict(ctx, c, caps))
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
		return []CheckResult{{Name: "SplitScopeSkipped"}}
	}

	claimID := claimproducer.ClaimID(uuid.New().String())
	openOut, err := c.Open(ctx, claimID, claimproducer.ClaimSpec{
		ProducerName: "conformance-target",
		Selector:     "rimsky/conformance/split-scope/" + uuid.New().String(),
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
		_ = c.Abandon(ctx, claimID, openOut.Result.ClaimScope, openOut.Result.Address)
		_ = c.Release(ctx, claimID, openOut.Result.ClaimScope, openOut.Result.Address)
	}()

	req := claimproducer.SplitClaimScopeRequest{
		ClaimHandleID:    string(claimID),
		PartitionRequest: splitScopeListProbeRequest(),
	}
	resp, err := c.SplitScope(ctx, req)
	if err != nil {
		return []CheckResult{{Name: "SplitScope", Err: err}}
	}

	results := make([]CheckResult, 0, 4)

	if len(resp.SubClaimScopes) != len(splitScopeListProbeElements) {
		results = append(results, CheckResult{
			Name: "SplitScopeListReturnsAllElements",
			Err: fmt.Errorf("expected %d sub-scopes (one per list element), got %d",
				len(splitScopeListProbeElements), len(resp.SubClaimScopes)),
		})
		return results
	}
	results = append(results, CheckResult{Name: "SplitScopeListReturnsAllElements"})

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
			Name: "SplitScopePreservesPartitionKey",
			Err: fmt.Errorf("input element key %q is not the PartitionKey of any returned sub-scope "+
				"(producer must surface each input element's key verbatim on its sub-scope)", keyMissing),
		})
	} else {
		results = append(results, CheckResult{Name: "SplitScopePreservesPartitionKey"})
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
			Name: "SplitScopePreservesPayload",
			Err: fmt.Errorf("%s (the universal list shape requires payload bytes byte-equal on round-trip)",
				payloadMismatch),
		})
	} else {
		results = append(results, CheckResult{Name: "SplitScopePreservesPayload"})
	}

	addrFailure := ""
	for _, sub := range resp.SubClaimScopes {
		if len(sub.Address) != 0 {
			addrFailure = fmt.Sprintf("sub-scope for key %q returned non-empty Address %q on a list shape; "+
				"the list shape carries data in payload only — address must be empty",
				sub.PartitionKey, string(sub.Address))
			break
		}
	}
	if addrFailure != "" {
		results = append(results, CheckResult{
			Name: "SplitScopeAddressFieldPresent",
			Err:  fmt.Errorf("%s", addrFailure),
		})
	} else {
		results = append(results, CheckResult{Name: "SplitScopeAddressFieldPresent"})
	}

	return results
}

func checkScopesConflict(ctx context.Context, c claimproducer.ClaimProducer, caps claimproducer.Capabilities) CheckResult {
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
				Err:  fmt.Errorf("byte-equal fallback returned Conflicts=false; byte-equal scopes are required to conflict"),
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
