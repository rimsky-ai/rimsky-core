// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package storetest

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	cpconformance "github.com/rimsky-ai/rimsky-core/lib/protocols/conformance/claimproducer"
)

func TestFakeConformanceMinimalCapabilities(t *testing.T) {
	f := NewFake("conformance-minimal", claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
	})

	results := cpconformance.Run(context.Background(), f)
	for _, r := range results {
		if r.Err != nil {
			t.Errorf("%s: unexpected error: %v", r.Name, r.Err)
		}
	}

	wantSeen := map[string]bool{
		"SplitScopeSkipped":      false,
		"ScopesConflictSkipped":  false,
		"Serialization9bSkipped": false,
		"OpenFirst":              false,
		"Uniformity":             false,
	}
	for _, r := range results {
		if _, ok := wantSeen[r.Name]; ok {
			wantSeen[r.Name] = true
		}
	}
	for name, seen := range wantSeen {
		if !seen {
			t.Errorf("expected check %q to run against the minimal-capability fake, did not see it", name)
		}
	}
}

type splitScopeListProbeElement struct {
	Key     string          `json:"key"`
	Payload json.RawMessage `json:"payload"`
}

type splitScopeListProbe struct {
	List []splitScopeListProbeElement `json:"list"`
}

func TestFakeConformanceFullCapabilities(t *testing.T) {
	f := NewFake("conformance-full", claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{
			claimproducer.WriteSemanticsStagedAsync,
		},
		SupportsSplitScope:     true,
		SupportsScopesConflict: true,
	})
	f.SplitClaimScopeFunc = func(req claimproducer.SplitClaimScopeRequest) (claimproducer.SplitClaimScopeResponse, error) {
		var probe splitScopeListProbe
		if err := json.Unmarshal(req.PartitionRequest, &probe); err != nil {
			return claimproducer.SplitClaimScopeResponse{}, err
		}
		subs := make([]claimproducer.SubClaimScopeDescriptor, 0, len(probe.List))
		for _, el := range probe.List {
			subs = append(subs, claimproducer.SubClaimScopeDescriptor{
				PartitionKey: el.Key,
				Payload:      el.Payload,
			})
		}
		return claimproducer.SplitClaimScopeResponse{SubClaimScopes: subs}, nil
	}

	results := cpconformance.Run(context.Background(), f)
	for _, r := range results {
		if r.Err != nil {
			t.Errorf("%s: unexpected error: %v", r.Name, r.Err)
		}
	}

	wantSeen := map[string]bool{
		"OpenFirst":                                false,
		"Uniformity":                               false,
		"SplitScopeListShapeReturnsAllElements":    false,
		"SplitScopeListShapePreservesPartitionKey": false,
		"SplitScopeListShapePreservesPayload":      false,
		"SplitScopeListShapeAddressFieldEmpty":     false,
		"ScopesConflict":                           false,
		"Serialization9b":                          false,
	}
	for _, r := range results {
		if _, ok := wantSeen[r.Name]; ok {
			wantSeen[r.Name] = true
		}
	}
	for name, seen := range wantSeen {
		if !seen {
			t.Errorf("expected check %q to run against the full-capability fake, did not see it", name)
		}
	}
}
