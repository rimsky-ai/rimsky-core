// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package executor

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"google.golang.org/protobuf/encoding/protojson"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
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
}

func NewHTTPClient(endpoint Endpoint) (Client, error) {
	if endpoint.Transport != "http" {
		return nil, fmt.Errorf("executor.NewHTTPClient: transport=%q not http", endpoint.Transport)
	}
	return &httpClient{client: &http.Client{}, endpoint: endpoint.URL}, nil
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

var _ = json.Marshal // keep encoding/json import for future use
