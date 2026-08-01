// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package main

import (
	"context"
	"testing"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

func TestDecodePartitionKeys_MalformedJSONReturnsError(t *testing.T) {
	if _, err := decodePartitionKeys([]byte("not json")); err == nil {
		t.Fatal("decodePartitionKeys: expected an error for malformed JSON, got nil " +
			"(a silent nil would let SplitScope answer with zero sub-scopes as success)")
	}
}

func TestDecodePartitionKeys_ValidJSONReturnsKeys(t *testing.T) {
	keys, err := decodePartitionKeys([]byte(`{"partition_keys":["a","b"]}`))
	if err != nil {
		t.Fatalf("decodePartitionKeys: %v", err)
	}
	if len(keys) != 2 || keys[0] != "a" || keys[1] != "b" {
		t.Fatalf("keys = %v, want [a b]", keys)
	}
}

func TestSplitScope_MalformedPartitionRequestErrors(t *testing.T) {
	p := &overlapProducer{}
	_, err := p.SplitScope(context.Background(), &genv1.SplitScopeRequest{
		PartitionRequest: []byte("not json"),
	})
	if err == nil {
		t.Fatal("SplitScope: expected an error for a malformed partition_request, got nil " +
			"(a template typo must not silently pass as zero sub-scopes)")
	}
}

func TestSplitScope_ValidPartitionRequestSucceeds(t *testing.T) {
	p := &overlapProducer{}
	resp, err := p.SplitScope(context.Background(), &genv1.SplitScopeRequest{
		PartitionRequest: []byte(`{"partition_keys":["a","b","c"]}`),
	})
	if err != nil {
		t.Fatalf("SplitScope: %v", err)
	}
	if len(resp.GetSubScopes()) != 3 {
		t.Fatalf("SubScopes = %d, want 3", len(resp.GetSubScopes()))
	}
}
