// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

package claimproducer

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

const nineBSelectorPrefix = "rimsky_conformance_9b_"

type fake9bProducer struct {
	dishonestSerializeReaders bool

	mu         sync.Mutex
	writerOpen bool
	writerID   claimproducer.ClaimID
	unlock     chan struct{}
	parked     chan struct{}
}

func newFake9bProducer(dishonestSerializeReaders bool) *fake9bProducer {
	return &fake9bProducer{
		dishonestSerializeReaders: dishonestSerializeReaders,
		unlock:                    make(chan struct{}),
		parked:                    make(chan struct{}, 8),
	}
}

func (f *fake9bProducer) Name() string { return "conformance-target" }

func (f *fake9bProducer) Capabilities(context.Context) (claimproducer.Capabilities, error) {
	return claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsStagedAsync},
	}, nil
}

func (f *fake9bProducer) Open(ctx context.Context, claimID claimproducer.ClaimID, spec claimproducer.ClaimSpec) (claimproducer.OpenOutcome, error) {
	scope, _ := json.Marshal(spec.Selector)
	result := claimproducer.ClaimResult{
		Address:                scope,
		ClaimScope:             scope,
		RealizedWriteSemantics: claimproducer.WriteSemanticsStagedAsync,
	}

	if spec.Intent == claimproducer.IntentReadWrite {
		f.mu.Lock()
		f.writerOpen = true
		f.writerID = claimID
		f.mu.Unlock()
		return claimproducer.OpenOutcome{Available: true, Result: result}, nil
	}

	if f.dishonestSerializeReaders && strings.HasPrefix(spec.Selector, nineBSelectorPrefix) {
		f.mu.Lock()
		blocked := f.writerOpen
		f.mu.Unlock()
		if blocked {
			f.parked <- struct{}{}
			select {
			case <-f.unlock:
			case <-ctx.Done():
				return claimproducer.OpenOutcome{}, ctx.Err()
			}
		}
	}
	return claimproducer.OpenOutcome{Available: true, Result: result}, nil
}

func (f *fake9bProducer) Commit(context.Context, claimproducer.ClaimID, []byte, []byte, string) (claimproducer.CommitResult, error) {
	return claimproducer.CommitResult{}, nil
}

func (f *fake9bProducer) Abandon(context.Context, claimproducer.ClaimID, []byte, []byte, string) error {
	return nil
}

func (f *fake9bProducer) Release(_ context.Context, claimID claimproducer.ClaimID, _ []byte, _ []byte, leaseToken string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.writerOpen && claimID == f.writerID {
		f.writerOpen = false
		close(f.unlock)
	}
	return nil
}

func (f *fake9bProducer) SplitScope(context.Context, claimproducer.SplitClaimScopeRequest) (claimproducer.SplitClaimScopeResponse, error) {
	return claimproducer.SplitClaimScopeResponse{}, claimproducer.ErrSplitScopeUnsupported
}

func (f *fake9bProducer) ScopesConflict(_ context.Context, a, b []byte) (bool, error) {
	return string(a) == string(b), nil
}

func TestCheckSerialization9b_DetectsReaderLeaseSerialization(t *testing.T) {
	f := newFake9bProducer(true)
	caps, err := f.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("fake Capabilities: %v", err)
	}

	readerCtx, endTheBound := context.WithCancel(context.Background())
	defer endTheBound()
	checked := make(chan CheckResult, 1)
	go func() {
		checked <- checkSerialization9b(context.Background(), f, caps,
			func(context.Context) (context.Context, context.CancelFunc) { return readerCtx, func() {} })
	}()

	<-f.parked
	<-f.parked
	endTheBound()

	r := <-checked
	if r.Err == nil {
		t.Fatal("Serialization9b must fail when the producer blocks a concurrent read Open behind an open writer on the byte-equal scope (reader-lease pattern), got PASS")
	}
	if !strings.Contains(r.Err.Error(), "reader Open did not return") {
		t.Fatalf("Serialization9b error should name the reader-lease detection, got: %v", r.Err)
	}
}

func TestCheckSerialization9b_PassesHonestMVCCPassThrough(t *testing.T) {
	results := Run(context.Background(), newFake9bProducer(false))
	r := findRow(t, results, "Serialization9b")
	if r.Err != nil {
		t.Fatalf("Serialization9b must pass when concurrent reads proceed without waiting on an open writer, got Err: %v", r.Err)
	}
}

type cancellingReadProducer struct {
	fake9bProducer
}

func (p *cancellingReadProducer) Open(ctx context.Context, claimID claimproducer.ClaimID, spec claimproducer.ClaimSpec) (claimproducer.OpenOutcome, error) {
	if spec.Intent == claimproducer.IntentRead && strings.HasPrefix(spec.Selector, nineBSelectorPrefix) {
		return claimproducer.OpenOutcome{}, status.Error(codes.Canceled, "the server cancelled this read")
	}
	return p.fake9bProducer.Open(ctx, claimID, spec)
}

func TestCheckSerialization9b_ReportsAServerCancellationAsAnInconclusiveProbe(t *testing.T) {
	p := &cancellingReadProducer{fake9bProducer: *newFake9bProducer(false)}
	caps, err := p.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("fake Capabilities: %v", err)
	}

	r := checkSerialization9b(context.Background(), p, caps, boundReaderOpenByTimeout)
	if r.Err == nil {
		t.Fatal("a reader Open that fails outright must not pass Serialization9b")
	}
	if strings.Contains(r.Err.Error(), "reader Open did not return") {
		t.Fatalf("a server-side cancellation, with the reader's bound still live, is not the reader-lease "+
			"pattern; the probe must report itself inconclusive, got: %v", r.Err)
	}
	if !strings.Contains(r.Err.Error(), "cannot conclude serialization") {
		t.Fatalf("Serialization9b must report the probe inconclusive, got: %v", r.Err)
	}
}
