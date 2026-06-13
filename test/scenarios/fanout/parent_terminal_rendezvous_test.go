// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// N2 scenario — parent_terminal_rendezvous.
//
// When all sub-claim handles of a fan-out parent have terminated, the
// parent's resolution fires. Aggregate outcome propagates:
//   - all sub-claims committed → parent Commits.
//   - any sub-claim aborted    → parent Aborts.
//
// Per spec
// .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
// §Recursive claim-tree resolution + §Fan-out template DSL step 7.
//
// The parent-terminal rendezvous logic lives in
// runtime/child_execution.go::SettleChildren. The persistence-
// touching paths are covered by the auto-terminal aggregate-outcome
// integration tests at test/scenarios/claim_stores/. This scenario
// exercises the parallelism-semaphore that bounds in-flight leaves
// during the wave — the operator-facing contract of `parallelism: N`
// on a fan-out node.
package fanout

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
)

func TestParentTerminalRendezvous_SemaphoreBoundsInFlightLeaves(t *testing.T) {
	t.Parallel()
	registry := runtime.NewFanOutSemaphoreRegistry()
	parent := shared.UUID(uuid.New())
	sem := registry.GetOrCreate(parent, 3) // cap=3 in-flight leaves

	// Hold 3 slots; the next Acquire must block.
	for i := 0; i < 3; i++ {
		if err := sem.Acquire(context.Background()); err != nil {
			t.Fatalf("acquire %d: %v", i, err)
		}
	}
	if sem.InFlight() != 3 {
		t.Fatalf("in-flight: %d (want 3)", sem.InFlight())
	}
	// A 4th Acquire blocks until a slot frees.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	err := sem.Acquire(ctx)
	if err == nil {
		t.Fatalf("expected timeout error on cap-bound acquire; got nil")
	}
	sem.Release()
	if err := sem.Acquire(context.Background()); err != nil {
		t.Fatalf("acquire after release should succeed; got %v", err)
	}
	registry.Drop(parent)
}

// The semaphore registry is per-parent-run. Two different parents'
// semaphores are independent — saturating one must not block the other.
func TestParentTerminalRendezvous_PerParentIsolation(t *testing.T) {
	t.Parallel()
	registry := runtime.NewFanOutSemaphoreRegistry()
	p1 := shared.UUID(uuid.New())
	p2 := shared.UUID(uuid.New())
	s1 := registry.GetOrCreate(p1, 1)
	s2 := registry.GetOrCreate(p2, 1)

	if err := s1.Acquire(context.Background()); err != nil {
		t.Fatalf("s1.Acquire: %v", err)
	}
	// s2 is independent — must succeed even though s1 is saturated.
	if err := s2.Acquire(context.Background()); err != nil {
		t.Fatalf("s2.Acquire (independent parent): %v", err)
	}
}

// Concurrent Acquires honor the cap exactly — bounded by a counter
// confirms the channel-backed semaphore is correct under contention.
func TestParentTerminalRendezvous_ConcurrentAcquireRespectsCap(t *testing.T) {
	t.Parallel()
	const cap = 4
	const goroutines = 32
	sem := runtime.NewFanOutParallelismSemaphore(cap)

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
			time.Sleep(5 * time.Millisecond) // simulate dispatch work
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
