package store

import (
	"context"
	"encoding/json"
	"testing"

	corestore "github.com/fallguy/rimsky/core/store"
)

func newStubWithPolicy(t *testing.T, selector string, items []json.RawMessage, onCommit, onGiveUp string) *Store {
	t.Helper()
	cfg := Config{
		Capabilities: corestore.Capabilities{WriteSemantics: corestore.WriteSemanticsDirect},
		PickPolicies: map[string]PickPolicyConfig{
			selector: {
				OnCommitDefault: onCommit,
				OnGiveUpDefault: onGiveUp,
				InitialItems:    items,
			},
		},
	}
	return New(cfg)
}

func TestPickPolicyOpenDrainsQueueFIFO(t *testing.T) {
	items := []json.RawMessage{
		json.RawMessage(`{"value":"a"}`),
		json.RawMessage(`{"value":"b"}`),
	}
	st := newStubWithPolicy(t, "@queue", items, "release_to_back", "release_to_back")
	ctx := context.Background()

	o1, err := st.Open(ctx, "c1", "@queue")
	if err != nil {
		t.Fatalf("Open c1: %v", err)
	}
	if !o1.Available {
		t.Fatalf("Open c1 should be Available; got Unavailable")
	}
	o2, err := st.Open(ctx, "c2", "@queue")
	if err != nil {
		t.Fatalf("Open c2: %v", err)
	}
	if !o2.Available {
		t.Fatalf("Open c2 should be Available; got Unavailable")
	}
	if string(o1.Result.Region) == string(o2.Result.Region) {
		t.Fatalf("different items should have different regions; got %s twice", o1.Result.Region)
	}
	// Third Open should signal Unavailable (queue drained).
	o3, err := st.Open(ctx, "c3", "@queue")
	if err != nil {
		t.Fatalf("Open c3: %v", err)
	}
	if o3.Available {
		t.Fatalf("empty queue should yield Unavailable; got Available with %+v", o3.Result)
	}
	if got := st.QueueLen("@queue"); got != 0 {
		t.Fatalf("QueueLen after drain = %d, want 0", got)
	}
	inFlight := st.InFlight("@queue")
	if len(inFlight) != 2 {
		t.Fatalf("InFlight = %v, want 2 items", inFlight)
	}
}

func TestApplyPickActionDelete(t *testing.T) {
	items := []json.RawMessage{json.RawMessage(`{"value":"a"}`)}
	st := newStubWithPolicy(t, "@queue", items, "delete", "delete")
	ctx := context.Background()
	o, _ := st.Open(ctx, "c1", "@queue")
	if err := st.Commit(ctx, "c1", o.Result.Region, o.Result.Address); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if got := len(st.InFlight("@queue")); got != 0 {
		t.Fatalf("after delete commit, InFlight = %d, want 0", got)
	}
	if got := st.QueueLen("@queue"); got != 0 {
		t.Fatalf("delete should not return item to queue; QueueLen = %d", got)
	}
}

func TestApplyPickActionReleaseToBack(t *testing.T) {
	items := []json.RawMessage{
		json.RawMessage(`{"v":"a"}`),
		json.RawMessage(`{"v":"b"}`),
	}
	st := newStubWithPolicy(t, "@queue", items, "release_to_back", "release_to_back")
	ctx := context.Background()

	o, _ := st.Open(ctx, "c1", "@queue")
	if err := st.Commit(ctx, "c1", o.Result.Region, o.Result.Address); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if got := st.QueueLen("@queue"); got != 2 {
		t.Fatalf("after release_to_back, QueueLen = %d, want 2", got)
	}
	if got := len(st.InFlight("@queue")); got != 0 {
		t.Fatalf("release_to_back should clear in-flight; got %d", got)
	}
}

func TestApplyPickActionReleaseToHead(t *testing.T) {
	items := []json.RawMessage{
		json.RawMessage(`{"v":"a"}`),
		json.RawMessage(`{"v":"b"}`),
	}
	st := newStubWithPolicy(t, "@queue", items, "release_to_head", "release_to_head")
	ctx := context.Background()

	o, _ := st.Open(ctx, "c1", "@queue")
	// Decode the picked item id so we can verify it lands at the head.
	var pickedID string
	_ = json.Unmarshal(o.Result.Region, &pickedID)

	if err := st.Abandon(ctx, "c1", o.Result.Region, o.Result.Address); err != nil {
		t.Fatalf("Abandon: %v", err)
	}
	// After Abandon with release_to_head default, the next Open should
	// receive the same item back.
	o2, _ := st.Open(ctx, "c2", "@queue")
	var nextID string
	_ = json.Unmarshal(o2.Result.Region, &nextID)
	if pickedID == "" || pickedID != nextID {
		t.Fatalf("release_to_head should return same item to head; got picked=%q, next=%q", pickedID, nextID)
	}
}

func TestApplyPickActionUnknownConfiguredActionReturnsError(t *testing.T) {
	items := []json.RawMessage{json.RawMessage(`{"v":"a"}`)}
	// Configure an invalid default action; the store should reject at terminal.
	st := newStubWithPolicy(t, "@queue", items, "what-is-this", "what-is-this")
	ctx := context.Background()
	o, _ := st.Open(ctx, "c1", "@queue")
	err := st.Commit(ctx, "c1", o.Result.Region, o.Result.Address)
	if err == nil {
		t.Fatal("expected error for unknown configured action; got nil")
	}
}

func TestRegionalSelectorEchoesAsAddressAndRegion(t *testing.T) {
	st := New(Config{Capabilities: corestore.Capabilities{WriteSemantics: corestore.WriteSemanticsDirect}})
	ctx := context.Background()
	o, err := st.Open(ctx, "c1", "concrete/path")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !o.Available {
		t.Fatalf("regional Open should be Available; got Unavailable")
	}
	var addr, region string
	_ = json.Unmarshal(o.Result.Address, &addr)
	_ = json.Unmarshal(o.Result.Region, &region)
	if addr != "concrete/path" || region != "concrete/path" {
		t.Fatalf("regional selector should echo; got addr=%q region=%q", addr, region)
	}
}

func TestSeedPickPolicyItem(t *testing.T) {
	st := newStubWithPolicy(t, "@queue", nil, "delete", "delete")
	id, err := st.SeedPickPolicyItem("@queue", json.RawMessage(`{"v":"new"}`))
	if err != nil {
		t.Fatalf("SeedPickPolicyItem: %v", err)
	}
	if id == "" {
		t.Fatal("SeedPickPolicyItem returned empty id")
	}
	if got := st.QueueLen("@queue"); got != 1 {
		t.Fatalf("QueueLen after seed = %d, want 1", got)
	}
}

func TestSeedPickPolicyItemUnknownSelector(t *testing.T) {
	st := newStubWithPolicy(t, "@queue", nil, "delete", "delete")
	if _, err := st.SeedPickPolicyItem("@unknown", json.RawMessage(`{}`)); err == nil {
		t.Fatal("expected error for unknown selector")
	}
}

func TestCallsRecorded(t *testing.T) {
	st := New(Config{Capabilities: corestore.Capabilities{WriteSemantics: corestore.WriteSemanticsDirect}})
	ctx := context.Background()
	_, _ = st.Open(ctx, "c1", "x")
	_ = st.Commit(ctx, "c1", []byte(`"x"`), []byte(`"x"`))
	calls := st.Calls()
	if len(calls) < 2 {
		t.Fatalf("expected ≥2 calls, got %d", len(calls))
	}
	if calls[0].Verb != "open" || calls[1].Verb != "commit" {
		t.Fatalf("call sequence wrong: %v", calls)
	}
}

func TestCapabilitiesDefaultsToDirect(t *testing.T) {
	st := New(Config{})
	if got := st.Capabilities().WriteSemantics; got != corestore.WriteSemanticsDirect {
		t.Fatalf("default WriteSemantics = %q, want %q", got, corestore.WriteSemanticsDirect)
	}
}
