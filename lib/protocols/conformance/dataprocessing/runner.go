// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package dataprocessing

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"google.golang.org/protobuf/types/known/emptypb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

type CheckResult struct {
	Name string
	Err  error
}

func Run(ctx context.Context, c genv1.DataProcessingClient) []CheckResult {
	results := make([]CheckResult, 0, 10)

	caps, err := c.Capabilities(ctx, &emptypb.Empty{})
	if err != nil {
		results = append(results, CheckResult{Name: "Capabilities", Err: err})
		return results
	}
	if len(caps.GetDataShapes()) == 0 {
		results = append(results, CheckResult{
			Name: "Capabilities",
			Err:  fmt.Errorf("data_shapes is empty"),
		})
		return results
	}
	if len(caps.GetMaterializations()) == 0 {
		results = append(results, CheckResult{
			Name: "Capabilities",
			Err:  fmt.Errorf("materializations is empty"),
		})
		return results
	}
	results = append(results, CheckResult{Name: "Capabilities"})

	results = append(results, runBeginCommitPerMaterialization(ctx, c, caps)...)
	results = append(results, checkBeginCandidateIdempotent(ctx, c))
	results = append(results, checkAbandonCandidate(ctx, c)...)
	results = append(results, checkListVersionsSmoke(ctx, c))
	results = append(results, checkListPartitionsSmoke(ctx, c))
	results = append(results, checkGetVersionSchemaSmoke(ctx, c))
	results = append(results, checkConcurrentWrites(ctx, c))
	return results
}

func runBeginCommitPerMaterialization(ctx context.Context, c genv1.DataProcessingClient, caps *genv1.DataProcessingCapabilities) []CheckResult {
	out := make([]CheckResult, 0, len(caps.GetMaterializations()))
	for _, mat := range caps.GetMaterializations() {
		name := "BeginCommit/" + mat
		claimHandleID := fmt.Sprintf("rimsky/conformance/dataproc/%s/main", mat)
		subScope, _ := json.Marshal(map[string]any{
			"partition_key":   "conformance-region",
			"materialization": mat,
		})
		begin, err := c.BeginCandidate(ctx, &genv1.BeginCandidateRequest{
			ClaimHandleId:      claimHandleID,
			SubScopeDescriptor: subScope,
			IdempotencyKey:     "conformance-" + mat,
		})
		if err != nil {
			out = append(out, CheckResult{Name: name, Err: fmt.Errorf("BeginCandidate: %w", err)})
			continue
		}
		if len(begin.GetCandidateHandle()) == 0 {
			out = append(out, CheckResult{Name: name, Err: fmt.Errorf("BeginCandidate returned empty candidate_handle")})
			continue
		}
		commit, err := c.CommitCandidate(ctx, &genv1.CommitCandidateRequest{
			CandidateHandle: begin.GetCandidateHandle(),
		})
		if err != nil {
			out = append(out, CheckResult{Name: name, Err: fmt.Errorf("CommitCandidate: %w", err)})
			continue
		}
		_ = commit
		out = append(out, CheckResult{Name: name})
	}
	return out
}

func checkBeginCandidateIdempotent(ctx context.Context, c genv1.DataProcessingClient) CheckResult {
	req := &genv1.BeginCandidateRequest{
		ClaimHandleId:  "rimsky/conformance/dataproc/idempotency",
		IdempotencyKey: "idem-stable",
	}
	first, err := c.BeginCandidate(ctx, req)
	if err != nil {
		return CheckResult{Name: "BeginCandidateIdempotent", Err: fmt.Errorf("BeginCandidate #1: %w", err)}
	}
	second, err := c.BeginCandidate(ctx, req)
	if err != nil {
		return CheckResult{Name: "BeginCandidateIdempotent", Err: fmt.Errorf("BeginCandidate #2: %w", err)}
	}
	if !bytes.Equal(first.GetCandidateHandle(), second.GetCandidateHandle()) {
		return CheckResult{
			Name: "BeginCandidateIdempotent",
			Err: fmt.Errorf("retried BeginCandidate returned different candidate_handle (%q vs %q)",
				string(first.GetCandidateHandle()), string(second.GetCandidateHandle())),
		}
	}
	_, _ = c.AbandonCandidate(ctx, &genv1.AbandonCandidateRequest{CandidateHandle: first.GetCandidateHandle()})
	return CheckResult{Name: "BeginCandidateIdempotent"}
}

func checkAbandonCandidate(ctx context.Context, c genv1.DataProcessingClient) []CheckResult {
	claimHandleID := "rimsky/conformance/dataproc/abandon"

	begin, err := c.BeginCandidate(ctx, &genv1.BeginCandidateRequest{
		ClaimHandleId:  claimHandleID,
		IdempotencyKey: "abandon-1",
	})
	if err != nil {
		errResult := fmt.Errorf("BeginCandidate: %w", err)
		return []CheckResult{
			{Name: "AbandonCandidateExcludedFromListVersions", Err: errResult},
			{Name: "AbandonCandidateRejectsCommitAfterAbandon", Err: errResult},
			checkAbandonCandidateUnknownHandleFailsCleanly(ctx, c),
		}
	}

	if _, err := c.AbandonCandidate(ctx, &genv1.AbandonCandidateRequest{
		CandidateHandle: begin.GetCandidateHandle(),
	}); err != nil {
		errResult := fmt.Errorf("AbandonCandidate: %w", err)
		return []CheckResult{
			{Name: "AbandonCandidateExcludedFromListVersions", Err: errResult},
			{Name: "AbandonCandidateRejectsCommitAfterAbandon", Err: errResult},
			checkAbandonCandidateUnknownHandleFailsCleanly(ctx, c),
		}
	}

	results := make([]CheckResult, 0, 3)

	listResp, err := c.ListVersions(ctx, &genv1.ListVersionsRequest{ClaimHandleId: claimHandleID})
	switch {
	case err != nil:
		results = append(results, CheckResult{Name: "AbandonCandidateExcludedFromListVersions", Err: fmt.Errorf("ListVersions: %w", err)})
	case len(listResp.GetVersions()) != 0:
		results = append(results, CheckResult{
			Name: "AbandonCandidateExcludedFromListVersions",
			Err: fmt.Errorf("ListVersions returned %d version(s) for a claim_handle_id whose only candidate was abandoned, want 0",
				len(listResp.GetVersions())),
		})
	default:
		results = append(results, CheckResult{Name: "AbandonCandidateExcludedFromListVersions"})
	}

	if _, commitErr := c.CommitCandidate(ctx, &genv1.CommitCandidateRequest{
		CandidateHandle: begin.GetCandidateHandle(),
	}); commitErr == nil {
		results = append(results, CheckResult{
			Name: "AbandonCandidateRejectsCommitAfterAbandon",
			Err:  fmt.Errorf("CommitCandidate succeeded on a candidate_handle already abandoned; the producer must GC an abandoned candidate and reject a later commit"),
		})
	} else {
		results = append(results, CheckResult{Name: "AbandonCandidateRejectsCommitAfterAbandon"})
	}

	results = append(results, checkAbandonCandidateUnknownHandleFailsCleanly(ctx, c))
	return results
}

func checkAbandonCandidateUnknownHandleFailsCleanly(ctx context.Context, c genv1.DataProcessingClient) CheckResult {
	_, err := c.AbandonCandidate(ctx, &genv1.AbandonCandidateRequest{
		CandidateHandle: []byte("conformance-unknown-candidate-handle-never-issued-by-begincandidate"),
	})
	if err == nil {
		return CheckResult{
			Name: "AbandonCandidateUnknownHandleFailsCleanly",
			Err:  fmt.Errorf("AbandonCandidate on a candidate_handle never returned by BeginCandidate returned nil error; producers must reject an unrecognized handle rather than silently succeeding"),
		}
	}
	return CheckResult{Name: "AbandonCandidateUnknownHandleFailsCleanly"}
}

func checkListVersionsSmoke(ctx context.Context, c genv1.DataProcessingClient) CheckResult {
	claimHandleID := "rimsky/conformance/dataproc/list-versions"
	begin, err := c.BeginCandidate(ctx, &genv1.BeginCandidateRequest{
		ClaimHandleId:  claimHandleID,
		IdempotencyKey: "list-versions-1",
	})
	if err != nil {
		return CheckResult{Name: "ListVersionsSmoke", Err: fmt.Errorf("BeginCandidate: %w", err)}
	}
	if _, err := c.CommitCandidate(ctx, &genv1.CommitCandidateRequest{
		CandidateHandle: begin.GetCandidateHandle(),
	}); err != nil {
		return CheckResult{Name: "ListVersionsSmoke", Err: fmt.Errorf("CommitCandidate: %w", err)}
	}
	resp, err := c.ListVersions(ctx, &genv1.ListVersionsRequest{ClaimHandleId: claimHandleID})
	if err != nil {
		return CheckResult{Name: "ListVersionsSmoke", Err: fmt.Errorf("ListVersions: %w", err)}
	}
	if len(resp.GetVersions()) == 0 {
		return CheckResult{Name: "ListVersionsSmoke", Err: fmt.Errorf("ListVersions returned zero versions after a successful commit")}
	}
	for i, v := range resp.GetVersions() {
		if v.GetVersionId() == "" {
			return CheckResult{Name: "ListVersionsSmoke", Err: fmt.Errorf("versions[%d].version_id empty", i)}
		}
	}
	return CheckResult{Name: "ListVersionsSmoke"}
}

func checkListPartitionsSmoke(ctx context.Context, c genv1.DataProcessingClient) CheckResult {
	claimHandleID := "rimsky/conformance/dataproc/list-partitions"
	subScope, _ := json.Marshal(map[string]string{"partition_key": "conformance-p1"})
	begin, err := c.BeginCandidate(ctx, &genv1.BeginCandidateRequest{
		ClaimHandleId:      claimHandleID,
		SubScopeDescriptor: subScope,
		IdempotencyKey:     "list-partitions-1",
	})
	if err != nil {
		return CheckResult{Name: "ListPartitionsSmoke", Err: fmt.Errorf("BeginCandidate: %w", err)}
	}
	if _, err := c.CommitCandidate(ctx, &genv1.CommitCandidateRequest{
		CandidateHandle: begin.GetCandidateHandle(),
	}); err != nil {
		return CheckResult{Name: "ListPartitionsSmoke", Err: fmt.Errorf("CommitCandidate: %w", err)}
	}
	resp, err := c.ListPartitions(ctx, &genv1.ListPartitionsRequest{ClaimHandleId: claimHandleID})
	if err != nil {
		return CheckResult{Name: "ListPartitionsSmoke", Err: fmt.Errorf("ListPartitions: %w", err)}
	}
	if len(resp.GetPartitions()) == 0 {
		return CheckResult{Name: "ListPartitionsSmoke", Err: fmt.Errorf("ListPartitions returned zero partitions after a successful commit")}
	}
	return CheckResult{Name: "ListPartitionsSmoke"}
}

func checkGetVersionSchemaSmoke(ctx context.Context, c genv1.DataProcessingClient) CheckResult {
	claimHandleID := "rimsky/conformance/dataproc/get-schema"
	begin, err := c.BeginCandidate(ctx, &genv1.BeginCandidateRequest{
		ClaimHandleId:  claimHandleID,
		IdempotencyKey: "get-schema-1",
	})
	if err != nil {
		return CheckResult{Name: "GetVersionSchemaSmoke", Err: fmt.Errorf("BeginCandidate: %w", err)}
	}
	if _, err := c.CommitCandidate(ctx, &genv1.CommitCandidateRequest{
		CandidateHandle: begin.GetCandidateHandle(),
	}); err != nil {
		return CheckResult{Name: "GetVersionSchemaSmoke", Err: fmt.Errorf("CommitCandidate: %w", err)}
	}
	versions, err := c.ListVersions(ctx, &genv1.ListVersionsRequest{ClaimHandleId: claimHandleID})
	if err != nil {
		return CheckResult{Name: "GetVersionSchemaSmoke", Err: fmt.Errorf("ListVersions: %w", err)}
	}
	if len(versions.GetVersions()) == 0 {
		return CheckResult{Name: "GetVersionSchemaSmoke", Err: fmt.Errorf("ListVersions returned zero versions")}
	}
	versionID := versions.GetVersions()[0].GetVersionId()
	resp, err := c.GetVersionSchema(ctx, &genv1.GetVersionSchemaRequest{
		ClaimHandleId: claimHandleID,
		VersionId:     versionID,
	})
	if err != nil {
		return CheckResult{Name: "GetVersionSchemaSmoke", Err: fmt.Errorf("GetVersionSchema: %w", err)}
	}
	if len(resp.GetSchema()) == 0 {
		return CheckResult{Name: "GetVersionSchemaSmoke", Err: fmt.Errorf("GetVersionSchema returned empty schema bytes")}
	}
	return CheckResult{Name: "GetVersionSchemaSmoke"}
}

func checkConcurrentWrites(ctx context.Context, c genv1.DataProcessingClient) CheckResult {
	const n = 8
	claimHandleID := "rimsky/conformance/dataproc/concurrent"
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			begin, err := c.BeginCandidate(ctx, &genv1.BeginCandidateRequest{
				ClaimHandleId:  claimHandleID,
				IdempotencyKey: fmt.Sprintf("concurrent-%d", i),
			})
			if err != nil {
				errs[i] = fmt.Errorf("BeginCandidate[%d]: %w", i, err)
				return
			}
			if _, err := c.CommitCandidate(ctx, &genv1.CommitCandidateRequest{
				CandidateHandle: begin.GetCandidateHandle(),
			}); err != nil {
				errs[i] = fmt.Errorf("CommitCandidate[%d]: %w", i, err)
			}
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return CheckResult{Name: "ConcurrentWrites", Err: err}
		}
	}
	resp, err := c.ListVersions(ctx, &genv1.ListVersionsRequest{ClaimHandleId: claimHandleID})
	if err != nil {
		return CheckResult{Name: "ConcurrentWrites", Err: fmt.Errorf("ListVersions: %w", err)}
	}
	if len(resp.GetVersions()) != n {
		return CheckResult{
			Name: "ConcurrentWrites",
			Err:  fmt.Errorf("expected %d versions after concurrent commits, got %d", n, len(resp.GetVersions())),
		}
	}
	seen := make(map[string]bool, n)
	for _, v := range resp.GetVersions() {
		if seen[v.GetVersionId()] {
			return CheckResult{
				Name: "ConcurrentWrites",
				Err:  fmt.Errorf("duplicate version_id %q under concurrent commits", v.GetVersionId()),
			}
		}
		seen[v.GetVersionId()] = true
	}
	return CheckResult{Name: "ConcurrentWrites"}
}
