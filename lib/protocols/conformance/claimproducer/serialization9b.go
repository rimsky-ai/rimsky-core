// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package claimproducer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

const readerOpenTimeout = 2 * time.Second

func checkSerialization9b(ctx context.Context, c claimproducer.ClaimProducer, caps claimproducer.Capabilities) CheckResult {
	if !caps.Contains(claimproducer.WriteSemanticsStagedAsync) {
		return CheckResult{Name: "Serialization9bSkipped"}
	}

	selector := identSafeSelector("rimsky_conformance_9b_")
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
		return CheckResult{Name: "Serialization9bSkipped"}
	}

	defer func() {
		_ = c.Abandon(ctx, writerID, writerOut.Result.ClaimScope, writerOut.Result.Address, "")
	}()

	blocked, probeErr, readerCleanup := openConcurrentReaders(ctx, c, selector, writerOut.Result.ClaimScope)

	readerCleanup(ctx, c)

	if probeErr != nil {
		return CheckResult{Name: "Serialization9b", Err: probeErr}
	}
	if blocked {
		return CheckResult{
			Name: "Serialization9b",
			Err: fmt.Errorf(
				"reader Open did not return Available within %s (or returned Available=false) "+
					"while a writer was open on the byte-equal scope: producer internally serializes "+
					"reader behind writer (the reader-lease pattern is forbidden for staged_async — "+
					"honest support requires snapshot delegation or native MVCC pass-through)",
				readerOpenTimeout),
		}
	}
	return CheckResult{Name: "Serialization9b"}
}

type readerOutcome struct {
	claimID claimproducer.ClaimID
	out     claimproducer.OpenOutcome
	err     error
}

func openConcurrentReaders(
	ctx context.Context, c claimproducer.ClaimProducer, selector string, writerScope []byte,
) (blocked bool, probeErr error, cleanup func(context.Context, claimproducer.ClaimProducer)) {
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

	for _, o := range outcomes {
		switch {
		case errors.Is(o.err, context.DeadlineExceeded) || status.Code(o.err) == codes.DeadlineExceeded:
			blocked = true
		case o.err != nil:
			if probeErr == nil {
				probeErr = fmt.Errorf(
					"reader Open failed for a reason other than blocking behind the writer "+
						"(cannot conclude serialization from this outcome): %w", o.err)
			}
		case !o.out.Available:
			blocked = true
		case !bytes.Equal(o.out.Result.ClaimScope, writerScope):
			if probeErr == nil {
				probeErr = errors.New(
					"reader realized a ClaimScope different from the writer's; the probe's premise " +
						"(reader and writer target the byte-equal scope) does not hold for this producer/selector")
			}
		}
	}

	cleanup = func(cleanupCtx context.Context, producer claimproducer.ClaimProducer) {
		for _, o := range outcomes {
			if o.err == nil && o.out.Available {
				_ = producer.Release(cleanupCtx, o.claimID, o.out.Result.ClaimScope, o.out.Result.Address, "")
			}
		}
	}
	return blocked, probeErr, cleanup
}
