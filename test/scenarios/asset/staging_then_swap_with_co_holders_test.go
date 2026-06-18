// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package asset

import (
	"context"
	"encoding/json"
	"testing"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/test/support/stores/stub/dataprocessing"
)

func TestStagingThenSwapWithCoHolders(t *testing.T) {
	t.Parallel()
	s := dataprocessing.New()
	ctx := context.Background()
	const claim = "asset/staging-swap"

	type stage struct {
		idem  string
		key   string
		bytes []byte
	}
	stages := []stage{}
	for _, key := range []string{"region-a", "region-b", "region-c"} {
		sub, _ := json.Marshal(map[string]string{"partition_key": key})
		stages = append(stages, stage{idem: key, key: key, bytes: sub})
	}
	candidates := make(map[string][]byte)
	for _, st := range stages {
		resp, err := s.BeginCandidate(ctx, &genv1.BeginCandidateRequest{
			ClaimHandleId:      claim,
			SubScopeDescriptor: st.bytes,
			IdempotencyKey:     st.idem,
		})
		if err != nil {
			t.Fatalf("BeginCandidate[%s]: %v", st.key, err)
		}
		candidates[st.key] = resp.GetCandidateHandle()
	}
	if s.CandidateCount() != len(stages) {
		t.Errorf("CandidateCount: got %d want %d", s.CandidateCount(), len(stages))
	}
	for key, handle := range candidates {
		if _, err := s.CommitCandidate(ctx, &genv1.CommitCandidateRequest{
			CandidateHandle: handle,
		}); err != nil {
			t.Errorf("CommitCandidate[%s]: %v", key, err)
		}
	}
	if s.CandidateCount() != 0 {
		t.Errorf("CandidateCount after commit: got %d want 0", s.CandidateCount())
	}
	versions, err := s.ListVersions(ctx, &genv1.ListVersionsRequest{ClaimHandleId: claim})
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(versions.GetVersions()) != len(stages) {
		t.Errorf("versions: got %d want %d", len(versions.GetVersions()), len(stages))
	}
}
