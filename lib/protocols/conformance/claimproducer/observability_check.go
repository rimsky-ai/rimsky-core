// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package claimproducer

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

type ObservabilityCheckOpts struct {
	Endpoint             string
	RetentionTestSeconds int
}

func RunObservabilityCheck(ctx context.Context, opts ObservabilityCheckOpts, logf func(format string, args ...any)) error {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	conn, err := grpc.NewClient(stripScheme(opts.Endpoint),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()
	client := genv1.NewClaimProducerObservabilityClient(conn)
	caps, err := client.Capabilities(ctx, &genv1.GetClaimProducerCapabilitiesRequest{})
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.Unimplemented {
			logf("observability: Capabilities Unimplemented (store declares no observability)\n")
			return nil
		}
		return fmt.Errorf("Capabilities: %w", err)
	}
	logf("observability: capabilities = supports_claim_get=%v supports_claim_stream=%v supports_list_claims=%v admin_views=%d http_bridge_url=%q\n",
		caps.GetSupportsClaimGet(), caps.GetSupportsClaimStream(),
		caps.GetSupportsListClaims(), len(caps.GetAdminViews()),
		caps.GetHttpBridgeUrl())

	const probeID = "conformance-probe-no-claim"

	if caps.GetSupportsClaimGet() {
		detail, err := client.GetClaim(ctx, &genv1.GetClaimRequest{ClaimId: probeID})
		if err != nil {
			return fmt.Errorf("GetClaim probe: %w", err)
		}
		if detail.GetState() != genv1.ClaimState_UNKNOWN {
			return fmt.Errorf("GetClaim on missing claim returned state=%v, want UNKNOWN (spec §3.6)", detail.GetState())
		}
		logf("observability: GetClaim missing-claim shape OK\n")
	}

	if caps.GetSupportsClaimStream() {
		streamCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		stream, err := client.StreamClaim(streamCtx, &genv1.StreamClaimRequest{ClaimId: probeID})
		if err != nil {
			return fmt.Errorf("StreamClaim open: %w", err)
		}
		count := 0
		for {
			_, rerr := stream.Recv()
			if rerr != nil {
				break
			}
			count++
		}
		logf("observability: StreamClaim missing-claim received %d events\n", count)
	}

	if caps.GetSupportsListClaims() {
		list, err := client.ListClaims(ctx, &genv1.ListClaimsRequest{Limit: 1})
		if err != nil {
			return fmt.Errorf("ListClaims probe: %w", err)
		}
		logf("observability: ListClaims returned %d claim summaries (next_cursor=%q)\n",
			len(list.GetClaims()), list.GetNextCursor())
	}

	for _, v := range caps.GetAdminViews() {
		hasRequired := false
		for _, p := range v.GetParams() {
			if p.GetRequired() {
				hasRequired = true
				break
			}
		}
		if hasRequired {
			logf("observability: admin view %q skipped (requires params)\n", v.GetName())
			continue
		}
		empty, _ := structpb.NewStruct(map[string]any{})
		view, err := client.GetAdminView(ctx, &genv1.GetAdminViewRequest{ViewName: v.GetName(), Params: empty})
		if err != nil {
			return fmt.Errorf("GetAdminView %q: %w", v.GetName(), err)
		}
		if view.GetRenderHint() == "" {
			return fmt.Errorf("GetAdminView %q returned empty render_hint", v.GetName())
		}
	}

	if opts.RetentionTestSeconds > 0 && caps.GetSupportsClaimGet() {
		claimClient := genv1.NewClaimProducerClient(conn)
		if err := runRetentionProbe(ctx, claimClient, client, opts.RetentionTestSeconds, logf, sleepRealtime); err != nil {
			return err
		}
	}
	return nil
}

func runRetentionProbe(
	ctx context.Context,
	claimClient genv1.ClaimProducerClient,
	obsClient genv1.ClaimProducerObservabilityClient,
	retentionSeconds int,
	logf func(format string, args ...any),
	wait func(ctx context.Context, d time.Duration) error,
) error {
	claimID := "conformance-retention-probe-" + uuid.New().String()

	openResp, err := claimClient.Open(ctx, &genv1.OpenRequest{
		ClaimId:      claimID,
		ProducerName: "conformance-target",
		Selector:     "rimsky/conformance/retention-probe/" + claimID,
		Intent:       "r",
		Alias:        "conformance-retention-probe",
	})
	if err != nil {
		return fmt.Errorf("retention probe: Open: %w", err)
	}
	acquired := openResp.GetAcquired()
	if acquired == nil {
		return fmt.Errorf("retention probe: producer returned Unavailable for a fresh synthetic selector — cannot drive a canned claim to test retention")
	}

	preCommit, err := obsClient.GetClaim(ctx, &genv1.GetClaimRequest{ClaimId: claimID})
	if err != nil {
		return fmt.Errorf("retention probe: GetClaim after Open: %w", err)
	}
	if preCommit.GetState() == genv1.ClaimState_UNKNOWN {
		return fmt.Errorf("retention probe: GetClaim after Open returned UNKNOWN for a just-opened claim; the driven claim must be visible before the retention window expires")
	}

	if _, err := claimClient.Commit(ctx, &genv1.CommitRequest{
		ClaimId:    claimID,
		ClaimScope: acquired.GetClaimScope(),
		Address:    acquired.GetAddress(),
	}); err != nil {
		return fmt.Errorf("retention probe: Commit: %w", err)
	}

	postCommit, err := obsClient.GetClaim(ctx, &genv1.GetClaimRequest{ClaimId: claimID})
	if err != nil {
		return fmt.Errorf("retention probe: GetClaim after Commit: %w", err)
	}
	if postCommit.GetState() == genv1.ClaimState_UNKNOWN {
		return fmt.Errorf("retention probe: GetClaim immediately after Commit returned UNKNOWN; a just-terminated claim must remain visible until the retention window expires")
	}

	waitFor := time.Duration(retentionSeconds+1) * time.Second
	logf("observability: retention probe — driven claim %q terminal, sleeping %v before re-querying\n", claimID, waitFor)
	if err := wait(ctx, waitFor); err != nil {
		return err
	}

	detail, err := obsClient.GetClaim(ctx, &genv1.GetClaimRequest{ClaimId: claimID})
	if err != nil {
		return fmt.Errorf("retention probe: GetClaim post-retention: %w", err)
	}
	if detail.GetState() != genv1.ClaimState_UNKNOWN {
		return fmt.Errorf("retention probe: GetClaim post-retention state=%v, want UNKNOWN (producer must evict claim data after its retention window)", detail.GetState())
	}
	logf("observability: retention probe ok (driven claim visible pre-eviction, UNKNOWN after the retention window)\n")
	return nil
}

func sleepRealtime(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

func stripScheme(s string) string {
	for _, prefix := range []string{"grpc://", "http://", "https://"} {
		if strings.HasPrefix(s, prefix) {
			return s[len(prefix):]
		}
	}
	return s
}
