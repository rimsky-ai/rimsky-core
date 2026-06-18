// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package claimproducer

import (
	"context"
	"fmt"
	"strings"
	"time"

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
		wait := time.Duration(opts.RetentionTestSeconds+1) * time.Second
		logf("observability: retention probe — sleeping %v before re-querying\n", wait)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
		detail, err := client.GetClaim(ctx, &genv1.GetClaimRequest{ClaimId: probeID})
		if err != nil {
			return fmt.Errorf("GetClaim post-retention: %w", err)
		}
		if detail.GetState() != genv1.ClaimState_UNKNOWN {
			return fmt.Errorf("GetClaim post-retention state=%v, want UNKNOWN", detail.GetState())
		}
		logf("observability: retention probe ok (UNKNOWN preserved)\n")
	}
	return nil
}

func stripScheme(s string) string {
	for _, prefix := range []string{"grpc://", "http://", "https://"} {
		if strings.HasPrefix(s, prefix) {
			return s[len(prefix):]
		}
	}
	return s
}
