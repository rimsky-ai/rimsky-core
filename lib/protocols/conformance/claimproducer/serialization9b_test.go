// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

package claimproducer

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

const nineBSelectorPrefix = "rimsky_conformance_9b_"

type fake9bProducer struct {
	dishonestSerializeReaders bool

	mu         sync.Mutex
	writerOpen bool
	writerID   claimproducer.ClaimID
	unlock     chan struct{}
}

func newFake9bProducer(dishonestSerializeReaders bool) *fake9bProducer {
	return &fake9bProducer{
		dishonestSerializeReaders: dishonestSerializeReaders,
		unlock:                    make(chan struct{}),
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
	results := Run(context.Background(), newFake9bProducer(true))
	r := findRow(t, results, "Serialization9b")
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
