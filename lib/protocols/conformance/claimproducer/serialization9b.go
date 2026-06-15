// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package claimproducer

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

// readerOpenTimeout bounds each concurrent reader Open in the 9b probe.
// An honest snapshot-delegating producer returns a reader Open promptly
// even while a writer is open; a producer that internally serializes on a
// lock-shaped predicate blocks the reader until the writer terminal-s, so
// the reader Open does not return within this window. The value is the
// detection threshold, not a correctness budget — an honest producer
// returns in microseconds, well inside it.
const readerOpenTimeout = 2 * time.Second

// checkSerialization9b actively probes for the reader-lease serialization
// pattern @blessed-invariant 9b forbids for staged_async producers.
//
// 9b requires that a staged_async producer NOT internally serialize a
// reader against an open writer on the byte-equal scope: honest support
// is snapshot delegation or native MVCC pass-through, where a reader Open
// coexists with an open writer and returns promptly. A producer that
// answers "is anyone holding a writer on X?" itself — blocking a reader
// Open until the writer's claim is terminal-ed — is the lock-shaped
// serialization 9b forbids.
//
// The probe SKIPs for any producer that does not advertise staged_async
// (the invariant is staged_async-specific). For a staged_async producer
// it opens a writer claim (IntentReadWrite) on a synthetic scope and,
// with the writer still open, fires two concurrent reader Opens
// (IntentRead) on the SAME byte-equal scope under a bounded timeout. If
// either reader Open fails to return within the timeout while the writer
// is open, the producer serialized reader behind writer → FAIL naming
// invariant 9b. If both readers return promptly → ok.
//
// The probe drives every claim to a clean terminal (Release) regardless
// of outcome so the producer's state is left consistent — including
// releasing the writer, which unblocks any reader a dishonest producer
// parked on its internal gate even after this probe's own bounded reader
// contexts have already expired.
func checkSerialization9b(ctx context.Context, c claimproducer.ClaimProducer, caps claimproducer.Capabilities) CheckResult {
	if !caps.Contains(claimproducer.WriteSemanticsStagedAsync) {
		// @constraint: 9b is a staged_async-specific invariant — a producer
		// that does not advertise staged_async cannot violate it; SKIP.
		return CheckResult{Name: "Serialization9bSkipped"}
	}

	// @constraint: a byte-equal scope shared by the writer and both readers —
	// identical Selector yields identical ClaimScope bytes from a conformant
	// producer, which is the precondition 9b governs.
	selector := "rimsky/conformance/9b/" + uuid.New().String()
	writerSpec := claimproducer.ClaimSpec{
		ProducerName: "conformance-target",
		Selector:     selector,
		Intent:       claimproducer.IntentReadWrite,
		Alias:        "conformance-9b-writer",
	}

	writerID := claimproducer.ClaimID(uuid.New().String())
	writerOut, err := c.Open(ctx, writerID, writerSpec)
	if err != nil {
		return CheckResult{Name: "Serialization9b", Err: fmt.Errorf("writer Open failed: %w", err)}
	}
	if !writerOut.Available {
		// @constraint: the producer could not give us a writer claim on the
		// synthetic scope, so we cannot exercise the writer-open precondition
		// — treat as SKIP rather than a violation; the probe needs an open
		// writer to detect reader serialization, and there is nothing to
		// terminal-release.
		return CheckResult{Name: "Serialization9bSkipped"}
	}

	// @constraint: always drive the writer to a clean terminal so producer
	// state is consistent and any reader parked on a dishonest producer's
	// internal gate is unblocked, even after this probe's bounded reader
	// contexts expired. Use the (untimed) parent context so Release is not
	// itself cancelled by a reader's deadline.
	defer func() {
		_ = c.Release(ctx, writerID, writerOut.Result.ClaimScope, writerOut.Result.Address)
	}()

	// @constraint: with the writer still open, fire two concurrent reader
	// Opens on the byte-equal scope — each gets its own bounded context. An
	// honest producer returns well inside the window; a serializing producer
	// blocks until the writer terminal-s, so its reader context expires and
	// the Open returns a deadline error — the 9b signal.
	blocked, readerCleanup := openConcurrentReaders(ctx, c, selector)

	// @constraint: release any reader claim that did return (honest path)
	// before the writer is released, so producer state is fully torn down.
	readerCleanup(ctx, c)

	if blocked {
		return CheckResult{
			Name: "Serialization9b",
			Err: fmt.Errorf(
				"reader Open did not return within %s while a writer was open on the byte-equal scope: "+
					"producer internally serializes reader behind writer (the reader-lease pattern "+
					"@blessed-invariant 9b forbids for staged_async — honest support requires snapshot "+
					"delegation or native MVCC pass-through)",
				readerOpenTimeout),
		}
	}
	return CheckResult{Name: "Serialization9b"}
}

// readerOutcome records one concurrent reader Open's result so the caller
// can both judge whether it blocked and tear down a claim it acquired.
type readerOutcome struct {
	claimID claimproducer.ClaimID
	out     claimproducer.OpenOutcome
	err     error
}

// openConcurrentReaders fires two reader Opens (IntentRead) on the
// byte-equal scope concurrently, each under its own readerOpenTimeout
// context, while the writer is still open. It returns whether either
// reader blocked past the timeout (the 9b violation signal) and a cleanup
// closure that releases any reader claim that actually acquired.
//
// Each reader's bounded context is derived from the parent so a reader
// that a serializing producer parks on its internal gate returns a
// deadline error (freeing the goroutine) rather than hanging until the
// writer terminal-s — the conformance runner must never wedge on a
// dishonest producer.
func openConcurrentReaders(ctx context.Context, c claimproducer.ClaimProducer, selector string) (bool, func(context.Context, claimproducer.ClaimProducer)) {
	const readerCount = 2
	outcomes := make([]readerOutcome, readerCount)
	var wg sync.WaitGroup
	wg.Add(readerCount)
	for i := range outcomes {
		go func(idx int) {
			defer wg.Done()
			readerCtx, cancel := context.WithTimeout(ctx, readerOpenTimeout)
			defer cancel()
			readerID := claimproducer.ClaimID(uuid.New().String())
			spec := claimproducer.ClaimSpec{
				ProducerName: "conformance-target",
				Selector:     selector,
				Intent:       claimproducer.IntentRead,
				Alias:        "conformance-9b-reader",
			}
			out, err := c.Open(readerCtx, readerID, spec)
			outcomes[idx] = readerOutcome{claimID: readerID, out: out, err: err}
		}(i)
	}
	wg.Wait()

	blocked := false
	for _, o := range outcomes {
		// @constraint: a reader whose Open did not return successfully
		// within the bounded window (deadline exceeded or context
		// cancelled) was serialized behind the open writer — the 9b signal.
		if o.err != nil {
			blocked = true
		}
	}

	cleanup := func(cleanupCtx context.Context, producer claimproducer.ClaimProducer) {
		for _, o := range outcomes {
			if o.err == nil && o.out.Available {
				_ = producer.Release(cleanupCtx, o.claimID, o.out.Result.ClaimScope, o.out.Result.Address)
			}
		}
	}
	return blocked, cleanup
}
