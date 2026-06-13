// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package executor

import (
	"bufio"
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

// HTTP+JSON bridge wire format (§7.1):
//   POST <endpoint.URL>/v1/Execute
//     Content-Type: application/json
//     body: JSON form of ExecuteRequest
//   Response:
//     Content-Type: application/x-ndjson
//     body: one ExecuteEvent JSON per line, stream ends on connection close

type httpClient struct {
	client   *http.Client
	endpoint string
	tlsMode  string
}

// NewHTTPClient builds the HTTP-bridge client, honoring the entry's
// validated `tls:` mode exactly like the gRPC path: "required" → the
// endpoint URL must be https and the handshake verifies against system
// roots (or the test-injected pool); "off" / empty → dial whatever the
// scheme says. A `tls: required` entry with a plaintext http:// URL is
// rejected loudly here — the mode is never accepted-and-ignored
// (STORY-peer-tls-enforced falsifier).
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
	// Verified TLS with the same root-pool posture as the gRPC dial
	// sites (system roots, test-injectable). Cloning the default
	// transport keeps proxy/timeouts/HTTP2 behavior.
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = peer.TLSClientConfig()
	return &httpClient{
		client:   &http.Client{Transport: transport},
		endpoint: endpoint.URL,
		tlsMode:  endpoint.TLS,
	}, nil
}

func (c *httpClient) Execute(ctx context.Context, req *genv1.ExecuteRequest) (EventStream, error) {
	body, err := protojson.Marshal(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.endpoint+"/v1/Execute", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/x-ndjson")
	resp, err := c.client.Do(httpReq)
	if err != nil {
		// Mirror the gRPC TLSMode interceptors: failures under
		// `tls: required` name the peer and the mode so a handshake
		// failure surfaces loudly with the operator's intent attached.
		if c.tlsMode == peer.TLSModeRequired {
			return nil, fmt.Errorf("peer %q (tls: required): %w", c.endpoint, err)
		}
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("http bridge: %s: %s", resp.Status, string(b))
	}
	return &httpEventStream{r: bufio.NewReader(resp.Body), body: resp.Body}, nil
}
func (c *httpClient) Close() error { return nil }

type httpEventStream struct {
	r    *bufio.Reader
	body io.ReadCloser
}

func (e *httpEventStream) Recv() (*genv1.ExecuteEvent, error) {
	line, err := e.r.ReadBytes('\n')
	if len(line) == 0 && err != nil {
		if err == io.EOF {
			return nil, io.EOF
		}
		return nil, err
	}
	// Trim trailing newline.
	line = bytes.TrimRight(line, "\r\n")
	if len(line) == 0 {
		// Empty line: skip, try again.
		return e.Recv()
	}
	var ev genv1.ExecuteEvent
	if uerr := protojson.Unmarshal(line, &ev); uerr != nil {
		return nil, fmt.Errorf("http bridge: unmarshal: %w", uerr)
	}
	return &ev, nil
}
func (e *httpEventStream) Close() error { return e.body.Close() }
