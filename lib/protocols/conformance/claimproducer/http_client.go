// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

package claimproducer

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/serverkit"
)

// @concept: claim-producer
type HTTPBridgeClaimProducer struct {
	client   *http.Client
	endpoint string
}

var _ claimproducer.ClaimProducer = (*HTTPBridgeClaimProducer)(nil)

func NewHTTPBridgeClaimProducer(endpoint string) *HTTPBridgeClaimProducer {
	return &HTTPBridgeClaimProducer{
		client:   &http.Client{},
		endpoint: strings.TrimRight(endpoint, "/"),
	}
}

func (p *HTTPBridgeClaimProducer) Name() string { return "conformance-target" }

// @decision: protojson-gateway
func (p *HTTPBridgeClaimProducer) call(ctx context.Context, verb string, reqMsg proto.Message, respMsg proto.Message) error {
	var bodyBytes []byte
	if reqMsg != nil {
		b, err := protojson.Marshal(reqMsg)
		if err != nil {
			return fmt.Errorf("http bridge %s: marshal request: %w", verb, err)
		}
		bodyBytes = b
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.endpoint+"/v1/"+verb, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("http bridge %s: build request: %w", verb, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("http bridge %s: %w", verb, err)
	}
	defer resp.Body.Close()
	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("http bridge %s: read response: %w", verb, err)
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("http bridge %s: %s: %s", verb, resp.Status, string(respBytes))
	}
	if respMsg == nil {
		return nil
	}
	if err := protojson.Unmarshal(respBytes, respMsg); err != nil {
		return fmt.Errorf("http bridge %s: unmarshal response: %w", verb, err)
	}
	return nil
}

func (p *HTTPBridgeClaimProducer) Capabilities(ctx context.Context) (claimproducer.Capabilities, error) {
	var resp genv1.CapabilitiesResponse
	if err := p.call(ctx, "capabilities", nil, &resp); err != nil {
		return claimproducer.Capabilities{}, err
	}
	return serverkit.ClaimProducerCapabilitiesFromProto(&resp)
}

func (p *HTTPBridgeClaimProducer) Open(ctx context.Context, claimID claimproducer.ClaimID, spec claimproducer.ClaimSpec) (claimproducer.OpenOutcome, error) {
	req := &genv1.OpenRequest{
		ClaimId:      string(claimID),
		ProducerName: spec.ProducerName,
		Selector:     spec.Selector,
		Intent:       string(spec.Intent),
		Alias:        spec.Alias,
		TemplateId:   spec.TemplateID,
		InstanceId:   spec.InstanceID,
		RunScopeId:   spec.RunScopeID,
	}
	var resp genv1.OpenResponse
	if err := p.call(ctx, "open", req, &resp); err != nil {
		return claimproducer.OpenOutcome{}, err
	}
	return serverkit.OpenOutcomeFromProto(&resp)
}

func (p *HTTPBridgeClaimProducer) Commit(ctx context.Context, claimID claimproducer.ClaimID, scope, address []byte, leaseToken string) (claimproducer.CommitResult, error) {
	req := &genv1.CommitRequest{ClaimId: string(claimID), ClaimScope: scope, Address: address, LeaseToken: leaseToken}
	var resp genv1.CommitResponse
	if err := p.call(ctx, "commit", req, &resp); err != nil {
		return claimproducer.CommitResult{}, err
	}
	return serverkit.CommitResultFromProto(&resp), nil
}

func (p *HTTPBridgeClaimProducer) Abandon(ctx context.Context, claimID claimproducer.ClaimID, scope, address []byte, leaseToken string) error {
	req := &genv1.AbandonRequest{ClaimId: string(claimID), ClaimScope: scope, Address: address, LeaseToken: leaseToken}
	return p.call(ctx, "abandon", req, &genv1.AbandonResponse{})
}

func (p *HTTPBridgeClaimProducer) Release(ctx context.Context, claimID claimproducer.ClaimID, scope, address []byte, leaseToken string) error {
	req := &genv1.ReleaseRequest{ClaimId: string(claimID), ClaimScope: scope, Address: address, LeaseToken: leaseToken}
	return p.call(ctx, "release", req, &genv1.ReleaseResponse{})
}

func (p *HTTPBridgeClaimProducer) SplitScope(ctx context.Context, req claimproducer.SplitClaimScopeRequest) (claimproducer.SplitClaimScopeResponse, error) {
	wire := &genv1.SplitScopeRequest{ClaimHandleId: req.ClaimHandleID, PartitionRequest: req.PartitionRequest}
	var resp genv1.SplitScopeResponse
	if err := p.call(ctx, "split_scope", wire, &resp); err != nil {
		return claimproducer.SplitClaimScopeResponse{}, err
	}
	return serverkit.SplitScopeResponseFromProto(&resp), nil
}

func (p *HTTPBridgeClaimProducer) ScopesConflict(context.Context, []byte, []byte) (bool, error) {
	return false, claimproducer.ErrScopesConflictUnsupported
}
