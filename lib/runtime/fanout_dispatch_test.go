// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package runtime

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/awaited"
)

type fakeScopeOnlyTables struct {
	persistence.Tables
	scopes persistence.RunScopeTable
}

func (f fakeScopeOnlyTables) RunScopes() persistence.RunScopeTable { return f.scopes }

func (f fakeScopeOnlyTables) Transaction(ctx context.Context, fn func(ctx context.Context, tx persistence.Tx) error) error {
	return fn(ctx, nil)
}

func TestFanOutPartitions_ProjectsPartitionKeys(t *testing.T) {
	subClaims := []SubClaim{
		{ClaimHandleID: shared.UUID(uuid.New()), PartitionKey: "p1", ClaimScope: json.RawMessage(`{"prefix":"a"}`)},
		{ClaimHandleID: shared.UUID(uuid.New()), PartitionKey: "p2", ClaimScope: json.RawMessage(`{"prefix":"b"}`)},
		{ClaimHandleID: shared.UUID(uuid.New()), PartitionKey: "p3", ClaimScope: json.RawMessage(`{"prefix":"c"}`)},
	}
	parts := FanOutPartitions(subClaims)
	if len(parts) != 3 {
		t.Fatalf("partitions: %d (want 3)", len(parts))
	}
	for i, p := range parts {
		if p.PartitionKey != subClaims[i].PartitionKey {
			t.Errorf("partition[%d].PartitionKey: %s want %s", i, p.PartitionKey, subClaims[i].PartitionKey)
		}
		if p.SubClaimHandleID != subClaims[i].ClaimHandleID {
			t.Errorf("partition[%d].SubClaimHandleID mismatch", i)
		}
	}
}

func TestFanOutPartitions_NoDedup(t *testing.T) {
	subClaims := []SubClaim{
		{ClaimHandleID: shared.UUID(uuid.New()), PartitionKey: "dup"},
		{ClaimHandleID: shared.UUID(uuid.New()), PartitionKey: "dup"},
		{ClaimHandleID: shared.UUID(uuid.New()), PartitionKey: "p3"},
	}
	parts := FanOutPartitions(subClaims)
	if len(parts) != len(subClaims) {
		t.Fatalf("FanOutPartitions must be a 1:1 projection over its input — sub-claim "+
			"de-duplication, if any, happens upstream at acquisition time, not here; "+
			"got %d outputs for %d inputs", len(parts), len(subClaims))
	}
}

func TestFanOutPartitions_PreservesInputOrdering(t *testing.T) {
	subClaims := []SubClaim{
		{ClaimHandleID: shared.UUID(uuid.New()), PartitionKey: "z-last"},
		{ClaimHandleID: shared.UUID(uuid.New()), PartitionKey: "m-middle"},
		{ClaimHandleID: shared.UUID(uuid.New()), PartitionKey: "a-first"},
	}
	parts := FanOutPartitions(subClaims)
	for i, p := range parts {
		if p.PartitionKey != subClaims[i].PartitionKey {
			t.Errorf("parts[%d]: %s (want %s; ordering must match producer's SubScopes, not a sort)",
				i, p.PartitionKey, subClaims[i].PartitionKey)
		}
	}
}

func TestFanOutPartitions_EmptyReturnsEmpty(t *testing.T) {
	if parts := FanOutPartitions(nil); len(parts) != 0 {
		t.Errorf("empty sub-claims should produce empty partitions; got %d", len(parts))
	}
}

func TestChildExecutionInput_FieldsWireThrough(t *testing.T) {
	parentRun := shared.UUID(uuid.New())
	parentNode := shared.UUID(uuid.New())
	frameID := shared.UUID(uuid.New())
	subClaims := []SubClaim{
		{ClaimHandleID: shared.UUID(uuid.New()), PartitionKey: "us-east-1"},
		{ClaimHandleID: shared.UUID(uuid.New()), PartitionKey: "us-west-2"},
	}
	in := ChildExecutionInput{
		ParentNodeRunID: parentRun,
		FrameID:         frameID,
		Partitions:      FanOutPartitions(subClaims),
		Children: []ChildRunSpec{{
			NodeID:                 parentNode,
			Executor:               "my-loader",
			RequiredClaimProducers: []string{"parquet-store"},
		}},
	}
	if in.ParentNodeRunID != parentRun {
		t.Errorf("in.ParentNodeRunID: %s (want %s)", in.ParentNodeRunID, parentRun)
	}
	if in.FrameID != frameID {
		t.Errorf("in.FrameID: %s (want %s)", in.FrameID, frameID)
	}
	if len(in.Children) != 1 || in.Children[0].NodeID != parentNode || in.Children[0].Executor != "my-loader" {
		t.Fatalf("child spec did not wire through unchanged; got %+v", in.Children)
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
	third := make(chan error, 1)
	go func() { third <- sem.Acquire(context.Background()) }()
	awaited.Until(t, "the third acquire to park on a full cap=2 semaphore", func() bool {
		return sem.Waiting() == 1
	})
	if sem.InFlight() != 2 {
		t.Errorf("in-flight while a third acquire is parked: %d (want 2)", sem.InFlight())
	}
	sem.Release()
	if err := <-third; err != nil {
		t.Fatalf("the parked acquire must take the released slot: %v", err)
	}
	if sem.InFlight() != 2 {
		t.Errorf("in-flight after release+acquire: %d (want 2)", sem.InFlight())
	}
}

func TestFanOutParallelismSemaphore_Unbounded(t *testing.T) {
	sem := NewFanOutParallelismSemaphore(0)
	for i := 0; i < 1000; i++ {
		if err := sem.Acquire(context.Background()); err != nil {
			t.Fatalf("unbounded acquire[%d]: %v", i, err)
		}
	}
	if sem.InFlight() != 0 {
		t.Errorf("unbounded in-flight reports: %d (want 0)", sem.InFlight())
	}
	sem.Release()
}

func TestFanOutSemaphoreRegistry_PerParentIsolation(t *testing.T) {
	r := NewFanOutSemaphoreRegistry()
	p1 := shared.UUID(uuid.New())
	p2 := shared.UUID(uuid.New())
	s1 := r.GetOrCreate(p1, 1)
	s2 := r.GetOrCreate(p2, 1)

	if err := s1.Acquire(context.Background()); err != nil {
		t.Fatalf("s1.Acquire: %v", err)
	}
	if err := s2.Acquire(context.Background()); err != nil {
		t.Fatalf("s2.Acquire (independent parent): %v", err)
	}
}

func TestFanOutParallelismSemaphore_ConcurrentAcquireRespectsCap(t *testing.T) {
	const cap = 4
	const goroutines = 32
	sem := NewFanOutParallelismSemaphore(cap)

	var mu sync.Mutex
	maxConcurrent := 0
	current := 0
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := sem.Acquire(context.Background()); err != nil {
				t.Errorf("Acquire: %v", err)
				return
			}
			mu.Lock()
			current++
			if current > maxConcurrent {
				maxConcurrent = current
			}
			mu.Unlock()
			//nolint:testwallclock-pacing holds the slot so overlap is observable; the verdict is an upper bound on the observed concurrency, which more overlap only makes harder
			time.Sleep(5 * time.Millisecond)
			mu.Lock()
			current--
			mu.Unlock()
			sem.Release()
		}()
	}
	wg.Wait()
	if maxConcurrent > cap {
		t.Errorf("max concurrent: %d (exceeds cap=%d)", maxConcurrent, cap)
	}
}

func TestFanOutSemaphoreRegistry_GetOrCreateIsIdempotent(t *testing.T) {
	r := NewFanOutSemaphoreRegistry()
	parent := shared.UUID(uuid.New())
	s1 := r.GetOrCreate(parent, 4)
	s2 := r.GetOrCreate(parent, 999)
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

func TestFanOutParallelismSemaphore_WiredForFanOutChild(t *testing.T) {
	_, scopes := newFakes()
	parentRunID := newUUID()
	mainScope := scopes.makeRootScope("main", newUUID())
	subScope := scopes.makeChildScope(mainScope, parentRunID, "p1", "main")

	registry := NewFanOutSemaphoreRegistry()
	args := RunArgs{Persist: fakeScopeOnlyTables{scopes: scopes}, FanOutSemaphores: registry}
	acq := &acquisition{
		RunScopeID: subScope,
		NodeDef:    &node.TemplateNodeDef{FanOut: &spec.FanOutSpec{Parallelism: 3}},
	}
	dctx := dispatchContext{Args: args, Acquired: acq}

	sem := fanOutParallelismSemaphore(context.Background(), dctx)
	if sem == nil {
		t.Fatalf("dispatch of a fan-out child must acquire a real semaphore, not silently no-op")
	}
	if got := registry.GetOrCreate(parentRunID, 3); got != sem {
		t.Fatalf("semaphore must be keyed by the fan-out parent's node-run ID (the run-scope's ParentNodeRunID)")
	}
	if sem2 := fanOutParallelismSemaphore(context.Background(), dctx); sem2 != sem {
		t.Fatalf("repeated dispatch of siblings of the same fan-out must share one semaphore instance")
	}
}

func TestFanOutParallelismSemaphore_NilWhenNotFanOut(t *testing.T) {
	registry := NewFanOutSemaphoreRegistry()
	args := RunArgs{FanOutSemaphores: registry}
	acq := &acquisition{NodeDef: &node.TemplateNodeDef{}}
	dctx := dispatchContext{Args: args, Acquired: acq}
	if sem := fanOutParallelismSemaphore(context.Background(), dctx); sem != nil {
		t.Fatalf("a non-fan-out node must never acquire a parallelism semaphore")
	}
}

func TestFanOutParallelismSemaphore_NilWhenUnlimited(t *testing.T) {
	_, scopes := newFakes()
	parentRunID := newUUID()
	mainScope := scopes.makeRootScope("main", newUUID())
	subScope := scopes.makeChildScope(mainScope, parentRunID, "p1", "main")

	registry := NewFanOutSemaphoreRegistry()
	args := RunArgs{Persist: fakeScopeOnlyTables{scopes: scopes}, FanOutSemaphores: registry}
	acq := &acquisition{
		RunScopeID: subScope,
		NodeDef:    &node.TemplateNodeDef{FanOut: &spec.FanOutSpec{Parallelism: 0}},
	}
	dctx := dispatchContext{Args: args, Acquired: acq}
	if sem := fanOutParallelismSemaphore(context.Background(), dctx); sem != nil {
		t.Fatalf("parallelism=0 (unlimited) must not gate dispatch through a semaphore")
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
