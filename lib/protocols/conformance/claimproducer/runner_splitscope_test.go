// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package claimproducer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

func TestCheckSplitScope_SupportsFalse_RejectionAcceptedAsSkipped(t *testing.T) {
	results := Run(context.Background(), newFakeProducer())
	row := findRow(t, results, "SplitScopeSkipped")
	if row.Err != nil {
		t.Errorf("SplitScopeSkipped expected PASS for supports=false producer that rejects SplitScope, got Err: %v", row.Err)
	}
}

func TestCheckSplitScope_SupportsFalse_AcceptingSplitScopeFails(t *testing.T) {
	producer := &splitScopeFake{
		supportsSplitScope: false,
		splitScopeReturns: claimproducer.SplitClaimScopeResponse{
			SubClaimScopes: []claimproducer.SubClaimScopeDescriptor{{PartitionKey: "x"}},
		},
		terminalCalls: map[claimproducer.ClaimID]int{},
	}
	results := Run(context.Background(), producer)
	row := findRow(t, results, "SplitScopeSkipped")
	if row.Err == nil {
		t.Errorf("SplitScopeSkipped expected non-nil Err when producer advertises supports=false but accepts SplitScope, got PASS")
	}
}

func TestCheckSplitScope_SupportsTrue_ListShapeRoundTrip(t *testing.T) {
	producer := newSplitScopeFakeListShape(true, false, false)
	results := Run(context.Background(), producer)
	for _, name := range []string{
		"SplitScopeListShapeReturnsAllElements",
		"SplitScopeListShapePreservesPartitionKey",
		"SplitScopeListShapePreservesPayload",
		"SplitScopeListShapeAddressFieldEmpty",
	} {
		row := findRow(t, results, name)
		if row.Err != nil {
			t.Errorf("%s expected PASS for honest list-round-trip producer, got Err: %v", name, row.Err)
		}
	}
}

func TestCheckSplitScope_SupportsTrue_NonEmptyAddressOnListShapeFails(t *testing.T) {
	producer := newSplitScopeFakeListShape(true, true, false)
	results := Run(context.Background(), producer)
	row := findRow(t, results, "SplitScopeListShapeAddressFieldEmpty")
	if row.Err == nil {
		t.Errorf("SplitScopeListShapeAddressFieldEmpty expected non-nil Err when producer returns non-empty Address on list shape, got PASS")
	}
}

func TestCheckSplitScope_SupportsTrue_PayloadMismatchFails(t *testing.T) {
	producer := newSplitScopeFakeListShape(true, false, true)
	results := Run(context.Background(), producer)
	row := findRow(t, results, "SplitScopeListShapePreservesPayload")
	if row.Err == nil {
		t.Errorf("SplitScopeListShapePreservesPayload expected non-nil Err when producer corrupts payload bytes, got PASS")
	}
}

func TestCheckSplitScope_SupportsTrue_WrongCountFails(t *testing.T) {
	producer := &splitScopeFake{
		supportsSplitScope: true,
		splitScopeReturns: claimproducer.SplitClaimScopeResponse{
			SubClaimScopes: []claimproducer.SubClaimScopeDescriptor{{PartitionKey: "alpha"}},
		},
		terminalCalls: map[claimproducer.ClaimID]int{},
	}
	results := Run(context.Background(), producer)
	row := findRow(t, results, "SplitScopeListShapeReturnsAllElements")
	if row.Err == nil {
		t.Errorf("SplitScopeListShapeReturnsAllElements expected non-nil Err when producer returns wrong count, got PASS")
	}
}

func TestCheckSplitScope_SupportsTrue_ProducerRejectsListShapeIsSkippedNotFailed(t *testing.T) {
	producer := &splitScopeFake{
		supportsSplitScope: true,
		splitScopeErr:      fmt.Errorf("producer: partition_request does not match this producer's date-range shape"),
		terminalCalls:      map[claimproducer.ClaimID]int{},
	}
	results := Run(context.Background(), producer)
	row := findRow(t, results, "SplitScopeListShapeProbeSkipped")
	if row.Err != nil {
		t.Errorf("SplitScopeListShapeProbeSkipped expected PASS when a supports=true producer rejects the "+
			"conformance kit's own list-shape probe (partition_request is producer-defined per proto), got Err: %v", row.Err)
	}
	for _, name := range []string{
		"SplitScopeListShapeReturnsAllElements",
		"SplitScopeListShapePreservesPartitionKey",
		"SplitScopeListShapePreservesPayload",
		"SplitScopeListShapeAddressFieldEmpty",
	} {
		for _, r := range results {
			if r.Name == name {
				t.Errorf("%s must not run when the producer rejected the list-shape probe request", name)
			}
		}
	}
}

type wireProbingSplitScopeFake struct {
	*splitScopeFake
	wireCalled bool
	wireErr    error
}

func (f *wireProbingSplitScopeFake) SplitScopeWire(_ context.Context, _ claimproducer.SplitClaimScopeRequest) (claimproducer.SplitClaimScopeResponse, error) {
	f.wireCalled = true
	return claimproducer.SplitClaimScopeResponse{}, f.wireErr
}

func TestCheckSplitScope_SupportsFalse_WireProbeExercisesTargetDirectly(t *testing.T) {
	inner := &splitScopeFake{
		supportsSplitScope: false,
		splitScopeErr:      claimproducer.ErrSplitScopeUnsupported,
		terminalCalls:      map[claimproducer.ClaimID]int{},
	}
	fake := &wireProbingSplitScopeFake{
		splitScopeFake: inner,
		wireErr:        fmt.Errorf("rpc error: code = NotFound desc = unknown claim_handle_id"),
	}
	results := Run(context.Background(), fake)
	if !fake.wireCalled {
		t.Fatalf("SplitScopeWire was never invoked; the supports=false negative probe must exercise the producer's wire directly, not only the client-side short-circuit")
	}
	row := findRow(t, results, "SplitScopeWireUnexercisedByUnsupportedClaim")
	if row.Err != nil {
		t.Errorf("expected PASS when the producer's wire rejects a never-Open'd claim_handle_id, got Err: %v", row.Err)
	}
}

func TestCheckSplitScope_SupportsFalse_WireProbeFlagsFabricatedSuccess(t *testing.T) {
	inner := &splitScopeFake{
		supportsSplitScope: false,
		splitScopeErr:      claimproducer.ErrSplitScopeUnsupported,
		terminalCalls:      map[claimproducer.ClaimID]int{},
	}
	fake := &wireProbingSplitScopeFake{
		splitScopeFake: inner,
		wireErr:        nil,
	}
	results := Run(context.Background(), fake)
	row := findRow(t, results, "SplitScopeWireUnexercisedByUnsupportedClaim")
	if row.Err == nil {
		t.Errorf("expected non-nil Err when the producer's wire fabricates a successful SplitScope for a never-Open'd claim_handle_id, got PASS")
	}
}

type splitScopeFake struct {
	mu sync.Mutex

	supportsSplitScope bool
	splitScopeReturns  claimproducer.SplitClaimScopeResponse
	splitScopeErr      error

	listShapeRoundTrip       bool
	listShapeNonEmptyAddress bool
	listShapeCorruptPayload  bool

	terminalCalls map[claimproducer.ClaimID]int
}

func newSplitScopeFakeListShape(roundTrip, nonEmptyAddress, corruptPayload bool) *splitScopeFake {
	return &splitScopeFake{
		supportsSplitScope:       true,
		listShapeRoundTrip:       roundTrip,
		listShapeNonEmptyAddress: nonEmptyAddress,
		listShapeCorruptPayload:  corruptPayload,
		terminalCalls:            map[claimproducer.ClaimID]int{},
	}
}

func (f *splitScopeFake) Name() string { return "split-scope-fake" }

func (f *splitScopeFake) Capabilities(context.Context) (claimproducer.Capabilities, error) {
	return claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
		SupportsSplitScope:    f.supportsSplitScope,
	}, nil
}

func (f *splitScopeFake) Open(_ context.Context, _ claimproducer.ClaimID, spec claimproducer.ClaimSpec) (claimproducer.OpenOutcome, error) {
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

func (f *splitScopeFake) Commit(_ context.Context, id claimproducer.ClaimID, _, _ []byte, leaseToken string) (claimproducer.CommitResult, error) {
	return claimproducer.CommitResult{}, f.recordTerminal(id)
}

func (f *splitScopeFake) Abandon(_ context.Context, id claimproducer.ClaimID, _, _ []byte, leaseToken string) error {
	return f.recordTerminal(id)
}

func (f *splitScopeFake) Release(_ context.Context, id claimproducer.ClaimID, _, _ []byte, leaseToken string) error {
	return f.recordTerminal(id)
}

func (f *splitScopeFake) recordTerminal(id claimproducer.ClaimID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.terminalCalls[id]++
	return nil
}

func (f *splitScopeFake) SplitScope(_ context.Context, req claimproducer.SplitClaimScopeRequest) (claimproducer.SplitClaimScopeResponse, error) {
	if f.splitScopeErr != nil {
		return claimproducer.SplitClaimScopeResponse{}, f.splitScopeErr
	}
	if f.listShapeRoundTrip || f.listShapeNonEmptyAddress || f.listShapeCorruptPayload {
		return f.roundTripListShape(req.PartitionRequest), nil
	}
	return f.splitScopeReturns, nil
}

func (f *splitScopeFake) roundTripListShape(partitionRequest []byte) claimproducer.SplitClaimScopeResponse {
	var parsed struct {
		List []struct {
			Key     string          `json:"key"`
			Payload json.RawMessage `json:"payload"`
		} `json:"list"`
	}
	if err := json.Unmarshal(partitionRequest, &parsed); err != nil {
		return claimproducer.SplitClaimScopeResponse{}
	}
	subs := make([]claimproducer.SubClaimScopeDescriptor, 0, len(parsed.List))
	for _, el := range parsed.List {
		payload := []byte(el.Payload)
		if f.listShapeCorruptPayload {
			payload = bytes.Join([][]byte{payload, []byte("CORRUPTED")}, nil)
		}
		desc := claimproducer.SubClaimScopeDescriptor{
			PartitionKey:   el.Key,
			Payload:        payload,
			ClaimScopeData: []byte(fmt.Sprintf(`{"k":%q}`, el.Key)),
		}
		if f.listShapeNonEmptyAddress {
			desc.Address = []byte("/some/path/" + el.Key)
		}
		subs = append(subs, desc)
	}
	return claimproducer.SplitClaimScopeResponse{SubClaimScopes: subs}
}

func (f *splitScopeFake) ScopesConflict(_ context.Context, a, b []byte) (bool, error) {
	return bytes.Equal(a, b), nil
}
