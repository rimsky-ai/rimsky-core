// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

// Fan-out dispatch unit tests — pure helpers; the integration-tx
// wiring (CreateFanOutChildren against a real RunTreeTable, the
// auto-terminal rendezvous with the producer's Commit response) is
// covered by N2/N10 scenario tests.

func TestPlanFanOutChildren_ProjectsPartitionKeys(t *testing.T) {
	parentRun := shared.UUID(uuid.New())
	parentNode := shared.UUID(uuid.New())
	frameID := shared.UUID(uuid.New())
	subClaims := []SubClaim{
		{ClaimHandleID: shared.UUID(uuid.New()), PartitionKey: "p1", Address: json.RawMessage(`{"path":"a"}`)},
		{ClaimHandleID: shared.UUID(uuid.New()), PartitionKey: "p2", Address: json.RawMessage(`{"path":"b"}`)},
		{ClaimHandleID: shared.UUID(uuid.New()), PartitionKey: "p3", Address: json.RawMessage(`{"path":"c"}`)},
	}
	plans := PlanFanOutChildren(parentRun, parentNode, frameID, subClaims, "stub", []string{"content"})
	if len(plans) != 3 {
		t.Fatalf("plans: %d (want 3)", len(plans))
	}
	for i, p := range plans {
		if p.ParentRunID != parentRun {
			t.Errorf("plan[%d].ParentRunID: %s", i, p.ParentRunID)
		}
		if p.PartitionKey != subClaims[i].PartitionKey {
			t.Errorf("plan[%d].PartitionKey: %s want %s", i, p.PartitionKey, subClaims[i].PartitionKey)
		}
		if p.SubClaimHandleID != subClaims[i].ClaimHandleID {
			t.Errorf("plan[%d].SubClaimHandleID mismatch", i)
		}
		if p.Executor != "stub" {
			t.Errorf("plan[%d].Executor: %s", i, p.Executor)
		}
	}
}

func TestFanOutParallelismSemaphore_BoundsConcurrency(t *testing.T) {
	sem := NewFanOutParallelismSemaphore(2)
	if err := sem.Acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := sem.Acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	if sem.InFlight() != 2 {
		t.Errorf("in-flight: %d (want 2)", sem.InFlight())
	}
	// Third Acquire should block; verify via a timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	err := sem.Acquire(ctx)
	if err == nil {
		t.Errorf("expected context-deadline error from 3rd acquire on cap=2 semaphore")
	}
	// Release one — next acquire passes.
	sem.Release()
	if err := sem.Acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	if sem.InFlight() != 2 {
		t.Errorf("in-flight after release+acquire: %d (want 2)", sem.InFlight())
	}
}

func TestFanOutParallelismSemaphore_Unbounded(t *testing.T) {
	sem := NewFanOutParallelismSemaphore(0)
	// Unbounded → 1000 acquires complete immediately, no block.
	for i := 0; i < 1000; i++ {
		if err := sem.Acquire(context.Background()); err != nil {
			t.Fatalf("unbounded acquire[%d]: %v", i, err)
		}
	}
	if sem.InFlight() != 0 {
		t.Errorf("unbounded in-flight reports: %d (want 0)", sem.InFlight())
	}
	sem.Release() // no-op on unbounded; should not panic
}

func TestFanOutSemaphoreRegistry_GetOrCreateIsIdempotent(t *testing.T) {
	r := NewFanOutSemaphoreRegistry()
	parent := shared.UUID(uuid.New())
	s1 := r.GetOrCreate(parent, 4)
	s2 := r.GetOrCreate(parent, 999) // cap argument ignored on re-lookup
	if s1 != s2 {
		t.Errorf("registry returned different semaphores for same parent")
	}
	r.Drop(parent)
	s3 := r.GetOrCreate(parent, 8)
	if s3 == s1 {
		t.Errorf("registry returned dropped semaphore")
	}
}

func TestFanOutSemaphoreRegistry_ConcurrentLookup(t *testing.T) {
	r := NewFanOutSemaphoreRegistry()
	parent := shared.UUID(uuid.New())
	var wg sync.WaitGroup
	var mu sync.Mutex
	seen := map[*FanOutParallelismSemaphore]struct{}{}
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s := r.GetOrCreate(parent, 2)
			mu.Lock()
			seen[s] = struct{}{}
			mu.Unlock()
		}()
	}
	wg.Wait()
	if len(seen) != 1 {
		t.Errorf("concurrent lookups returned %d distinct semaphores (want 1)", len(seen))
	}
}

func TestIsFanOutNode(t *testing.T) {
	cases := []struct {
		name string
		def  *node.TemplateNodeDef
		want bool
	}{
		{"nil", nil, false},
		{"no fan_out", &node.TemplateNodeDef{Executor: "stub"}, false},
		{"fan_out set", &node.TemplateNodeDef{
			Executor: "stub",
			FanOut: &spec.FanOutSpec{
				Claim:            "content",
				PartitionRequest: `{"kind":"prefix"}`,
				ErrorPolicy:      spec.AggregationPolicy{Kind: "strict"},
			},
		}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsFanOutNode(c.def); got != c.want {
				t.Errorf("got %v want %v", got, c.want)
			}
		})
	}
}

func TestFanOutAggregationPolicy_PullsFromNode(t *testing.T) {
	def := &node.TemplateNodeDef{
		FanOut: &spec.FanOutSpec{
			Claim:       "content",
			ErrorPolicy: spec.AggregationPolicy{Kind: "threshold", MaxFailures: 2},
		},
	}
	got := FanOutAggregationPolicy(def)
	if got.Kind != "threshold" || got.MaxFailures != 2 {
		t.Errorf("policy: %+v", got)
	}
	zero := FanOutAggregationPolicy(nil)
	if zero.Kind != "" {
		t.Errorf("nil def should return zero policy, got %+v", zero)
	}
}
