// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package persistence_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

func TestCarryForwardBagCopiesSpilledBlobToFreshHandle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	bb := persistence.NewMemoryBackend()

	payload := []byte(`{"big":"value","tag":"prior"}`)
	priorHandle, err := bb.Write(ctx, persistence.BlobKey{NodeID: "prior", AttributeName: "data"}, payload)
	if err != nil {
		t.Fatalf("seed blob: %v", err)
	}

	carried, err := persistence.CarryForwardBag(ctx, bb,
		persistence.BlobKey{NodeID: "new", AttributeName: "data"},
		[]byte("{}"), string(priorHandle), bb.Name())
	if err != nil {
		t.Fatalf("CarryForwardBag: %v", err)
	}
	if carried.Handle == "" {
		t.Fatalf("expected a fresh handle, got empty")
	}
	if carried.Handle == string(priorHandle) {
		t.Fatalf("carry-forward aliased the prior handle %q", priorHandle)
	}
	if !bytes.Equal(carried.DispatchBag, payload) {
		t.Fatalf("dispatch bag: got %q, want %q", carried.DispatchBag, payload)
	}
	if !bytes.Equal(carried.Data, []byte("{}")) {
		t.Fatalf("spilled data column must stay inline-empty; got %q", carried.Data)
	}

	if err := bb.Delete(ctx, priorHandle); err != nil {
		t.Fatalf("reap prior handle: %v", err)
	}
	fresh, err := bb.Read(ctx, persistence.Handle(carried.Handle))
	if err != nil {
		t.Fatalf("read fresh handle after prior reap: %v", err)
	}
	if !bytes.Equal(fresh, payload) {
		t.Fatalf("fresh blob content: got %q, want %q", fresh, payload)
	}
}

func TestCarryForwardBagPassesThroughUnspilled(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	bb := persistence.NewMemoryBackend()
	payload := []byte(`{"tag":"inline"}`)

	carried, err := persistence.CarryForwardBag(ctx, bb,
		persistence.BlobKey{NodeID: "new", AttributeName: "data"},
		payload, "", "")
	if err != nil {
		t.Fatalf("CarryForwardBag: %v", err)
	}
	if carried.Handle != "" || carried.Backend != "" {
		t.Fatalf("unspilled carry must not allocate a handle; got %q/%q", carried.Handle, carried.Backend)
	}
	if !bytes.Equal(carried.Data, payload) || !bytes.Equal(carried.DispatchBag, payload) {
		t.Fatalf("unspilled carry must pass content through; got data=%q bag=%q", carried.Data, carried.DispatchBag)
	}
}

func TestCarryForwardBagMissingBlobYieldsEmpty(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	bb := persistence.NewMemoryBackend()

	carried, err := persistence.CarryForwardBag(ctx, bb,
		persistence.BlobKey{NodeID: "new", AttributeName: "data"},
		[]byte("{}"), "mem:404", bb.Name())
	if err != nil {
		t.Fatalf("CarryForwardBag: %v", err)
	}
	if carried.Handle != "" {
		t.Fatalf("missing prior blob must not carry a handle; got %q", carried.Handle)
	}
	if !bytes.Equal(carried.Data, []byte("{}")) || !bytes.Equal(carried.DispatchBag, []byte("{}")) {
		t.Fatalf("missing prior blob must yield empty payload; got data=%q bag=%q", carried.Data, carried.DispatchBag)
	}
}
