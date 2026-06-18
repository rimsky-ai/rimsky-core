// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package executor

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"google.golang.org/protobuf/encoding/protojson"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/peer"
)

type httpClient struct {
	client   *http.Client
	endpoint string
	tlsMode  string
}

func NewHTTPClient(endpoint Endpoint) (Client, error) {
	if endpoint.Transport != "http" {
		return nil, fmt.Errorf("executor.NewHTTPClient: transport=%q not http", endpoint.Transport)
	}
	if endpoint.TLS != peer.TLSModeRequired {
		return &httpClient{client: &http.Client{}, endpoint: endpoint.URL, tlsMode: endpoint.TLS}, nil
	}
	u, err := url.Parse(endpoint.URL)
	if err != nil {
		return nil, fmt.Errorf("executor.NewHTTPClient: peer %q (tls: required): invalid endpoint URL: %w", endpoint.URL, err)
	}
	if u.Scheme != "https" {
		return nil, fmt.Errorf("executor.NewHTTPClient: peer %q (tls: required): endpoint scheme %q is not https — a tls: required HTTP-bridge executor must use an https:// URL", endpoint.URL, u.Scheme)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = peer.TLSClientConfig()
	return &httpClient{
		client:   &http.Client{Transport: transport},
		endpoint: endpoint.URL,
		tlsMode:  endpoint.TLS,
	}, nil
}

func (c *httpClient) Execute(ctx context.Context, req *genv1.ExecuteRequest) (*genv1.Outcome, error) {
	body, err := protojson.Marshal(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.endpoint+"/v1/Execute", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	resp, err := c.client.Do(httpReq)
	if err != nil {
		if c.tlsMode == peer.TLSModeRequired {
			return nil, fmt.Errorf("peer %q (tls: required): %w", c.endpoint, err)
		}
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("http bridge: %s: %s", resp.Status, string(b))
	}
	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("http bridge: read body: %w", err)
	}
	var outcome genv1.Outcome
	if err := protojson.Unmarshal(respBytes, &outcome); err != nil {
		return nil, fmt.Errorf("http bridge: unmarshal outcome: %w", err)
	}
	return &outcome, nil
}
func (c *httpClient) Close() error { return nil }
