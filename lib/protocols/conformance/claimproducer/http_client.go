// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

package claimproducer

import (
	"bytes"
	"context"
	"encoding/json"
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

func (p *HTTPBridgeClaimProducer) call(ctx context.Context, verb string, reqBody any, respMsg proto.Message) error {
	var bodyBytes []byte
	if reqBody != nil {
		b, err := json.Marshal(reqBody)
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

type httpOpenBody struct {
	ClaimID      string `json:"claim_id"`
	ProducerName string `json:"producer_name"`
	Selector     string `json:"selector"`
	Intent       string `json:"intent"`
	Alias        string `json:"alias"`
	TemplateID   string `json:"template_id"`
	InstanceID   string `json:"instance_id"`
	RunScopeID   string `json:"run_scope_id,omitempty"`
}

type httpActionBody struct {
	ClaimID    string `json:"claim_id"`
	Scope      []byte `json:"scope"`
	Address    []byte `json:"address"`
	LeaseToken string `json:"lease_token,omitempty"`
}

type httpSplitScopeBody struct {
	ClaimHandleID    string `json:"claim_handle_id"`
	PartitionRequest []byte `json:"partition_request"`
}

func (p *HTTPBridgeClaimProducer) Open(ctx context.Context, claimID claimproducer.ClaimID, spec claimproducer.ClaimSpec) (claimproducer.OpenOutcome, error) {
	body := httpOpenBody{
		ClaimID:      string(claimID),
		ProducerName: spec.ProducerName,
		Selector:     spec.Selector,
		Intent:       string(spec.Intent),
		Alias:        spec.Alias,
		TemplateID:   spec.TemplateID,
		InstanceID:   spec.InstanceID,
		RunScopeID:   spec.RunScopeID,
	}
	var resp genv1.OpenResponse
	if err := p.call(ctx, "open", body, &resp); err != nil {
		return claimproducer.OpenOutcome{}, err
	}
	return serverkit.OpenOutcomeFromProto(&resp)
}

func (p *HTTPBridgeClaimProducer) Commit(ctx context.Context, claimID claimproducer.ClaimID, scope, address []byte, leaseToken string) (claimproducer.CommitResult, error) {
	body := httpActionBody{ClaimID: string(claimID), Scope: scope, Address: address, LeaseToken: leaseToken}
	var resp genv1.CommitResponse
	if err := p.call(ctx, "commit", body, &resp); err != nil {
		return claimproducer.CommitResult{}, err
	}
	return serverkit.CommitResultFromProto(&resp), nil
}

func (p *HTTPBridgeClaimProducer) Abandon(ctx context.Context, claimID claimproducer.ClaimID, scope, address []byte, leaseToken string) error {
	body := httpActionBody{ClaimID: string(claimID), Scope: scope, Address: address, LeaseToken: leaseToken}
	return p.call(ctx, "abandon", body, &genv1.AbandonResponse{})
}

func (p *HTTPBridgeClaimProducer) Release(ctx context.Context, claimID claimproducer.ClaimID, scope, address []byte, leaseToken string) error {
	body := httpActionBody{ClaimID: string(claimID), Scope: scope, Address: address, LeaseToken: leaseToken}
	return p.call(ctx, "release", body, &genv1.ReleaseResponse{})
}

func (p *HTTPBridgeClaimProducer) SplitScope(ctx context.Context, req claimproducer.SplitClaimScopeRequest) (claimproducer.SplitClaimScopeResponse, error) {
	body := httpSplitScopeBody{ClaimHandleID: req.ClaimHandleID, PartitionRequest: req.PartitionRequest}
	var resp genv1.SplitScopeResponse
	if err := p.call(ctx, "split_scope", body, &resp); err != nil {
		return claimproducer.SplitClaimScopeResponse{}, err
	}
	return serverkit.SplitScopeResponseFromProto(&resp), nil
}

func (p *HTTPBridgeClaimProducer) ScopesConflict(context.Context, []byte, []byte) (bool, error) {
	return false, claimproducer.ErrScopesConflictUnsupported
}
