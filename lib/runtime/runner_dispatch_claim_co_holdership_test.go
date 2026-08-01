// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package runtime

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

// @concept: claim-co-holdership
func TestBuildClaimProducerHandles_CoHeldClaimRidesTheWire(t *testing.T) {
	acq := &acquisition{
		HeldClaims: map[string]claimproducer.ClaimResult{
			"held-alias": {
				Address: json.RawMessage(`{"path":"/held/addr"}`),
				Payload: json.RawMessage(`{"field":"held-payload"}`),
			},
		},
	}

	out, err := buildClaimProducerHandles(acq)
	if err != nil {
		t.Fatalf("buildClaimProducerHandles: %v", err)
	}
	handle, ok := out["held-alias"]
	if !ok {
		t.Fatalf("no ClaimProducerHandle entry for co-held alias %q; entries=%v", "held-alias", out)
	}
	if handle.Handle == nil {
		t.Fatalf("co-held claim's ClaimProducerHandle.Handle is nil; executor cannot read a store-handle entry")
	}
	m := handle.Handle.AsMap()
	addr, ok := m["address"].(map[string]any)
	if !ok || addr["path"] != "/held/addr" {
		t.Fatalf("co-held claim's wire address = %v, want {path: /held/addr}", m["address"])
	}
	payload, ok := m["payload"].(map[string]any)
	if !ok || payload["field"] != "held-payload" {
		t.Fatalf("co-held claim's wire payload = %v, want {field: held-payload}", m["payload"])
	}
}

// @concept: claim-co-holdership
func TestBuildClaimProducerHandles_AliasCollisionOpenedWins(t *testing.T) {
	acq := &acquisition{
		Locks: []AcquiredLock{
			{
				Alias: "dup",
				Spec:  claimproducer.ClaimSpec{Alias: "dup", ProducerName: "opened-store", Intent: claimproducer.IntentReadWrite},
				ClaimResult: claimproducer.ClaimResult{
					Address: json.RawMessage(`{"source":"opened"}`),
				},
			},
		},
		HeldClaims: map[string]claimproducer.ClaimResult{
			"dup": {
				Address: json.RawMessage(`{"source":"held"}`),
			},
		},
	}

	out, err := buildClaimProducerHandles(acq)
	if err != nil {
		t.Fatalf("buildClaimProducerHandles: %v", err)
	}
	handle, ok := out["dup"]
	if !ok {
		t.Fatalf("no ClaimProducerHandle entry for alias %q", "dup")
	}
	m := handle.Handle.AsMap()
	addr, ok := m["address"].(map[string]any)
	if !ok || addr["source"] != "opened" {
		t.Fatalf("alias collision wire address = %v, want the opened claim's address (opened must win over held)", m["address"])
	}
	if _, hasIntent := m["intent"]; !hasIntent {
		t.Fatalf("winning entry for %q must be the opened-lock's own handle shape (carries intent); got %v", "dup", m)
	}
}

// @concept: claim-co-holdership
func TestClaimsMapFromAcq_AliasCollisionOpenedWins(t *testing.T) {
	acq := &acquisition{
		Locks: []AcquiredLock{
			{
				Alias:       "dup",
				ClaimResult: claimproducer.ClaimResult{Address: json.RawMessage(`{"source":"opened"}`)},
			},
		},
		HeldClaims: map[string]claimproducer.ClaimResult{
			"dup": {Address: json.RawMessage(`{"source":"held"}`)},
		},
	}

	claims := claimsMapFromAcq(acq)
	got, ok := claims["dup"]
	if !ok {
		t.Fatalf("claimsMapFromAcq produced no entry for alias %q", "dup")
	}
	if string(got.Address) != `{"source":"opened"}` {
		t.Fatalf("claimsMapFromAcq[%q].Address = %s, want the opened claim's address (opened must win over held in the substitution context)",
			"dup", got.Address)
	}
}

// @concept: claim-co-holdership
func TestSubstituteFanOutPartitionRequest_AliasCollisionOpenedWins(t *testing.T) {
	ctx := context.Background()
	out := &acquisition{
		HeldClaims: map[string]claimproducer.ClaimResult{
			"dup": {Address: json.RawMessage(`{"source":"held"}`)},
		},
	}
	acquiredLocks := []AcquiredLock{
		{Alias: "dup", ClaimResult: claimproducer.ClaimResult{Address: json.RawMessage(`{"source":"opened"}`)}},
	}

	got, err := substituteFanOutPartitionRequest(ctx, RunArgs{}, shared.UUID{}, out, acquiredLocks, "{{claim.dup.address}}", nil)
	if err != nil {
		t.Fatalf("substituteFanOutPartitionRequest: %v", err)
	}
	if string(got) != `{"source":"opened"}` {
		t.Fatalf("fan-out partition_request substitution for alias %q = %s, want the opened claim's address "+
			"(opened must win over held in the fan-out substitution context too)", "dup", got)
	}
}
