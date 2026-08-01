// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

package enroll

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const Path = "/v1/enroll"

type Response struct {
	CertPEM   string    `json:"cert_pem"`
	KeyPEM    string    `json:"key_pem"`
	CARootPEM string    `json:"ca_root_pem"`
	NotAfter  time.Time `json:"not_after"`
}

type request struct {
	Label string `json:"label,omitempty"`
}

func Enroll(ctx context.Context, httpClient *http.Client, baseURL, apiKey, label string) (Response, error) {
	if httpClient == nil {
		return Response{}, fmt.Errorf("enroll: http client is required")
	}
	if apiKey == "" {
		return Response{}, fmt.Errorf("enroll: api key is required")
	}
	bodyBytes, err := json.Marshal(request{Label: label})
	if err != nil {
		return Response{}, fmt.Errorf("enroll: marshal request: %w", err)
	}
	url := strings.TrimRight(baseURL, "/") + Path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return Response{}, fmt.Errorf("enroll: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := httpClient.Do(req)
	if err != nil {
		return Response{}, fmt.Errorf("enroll: POST %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Response{}, fmt.Errorf("enroll: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return Response{}, fmt.Errorf("enroll: POST %s: status %d: %s", url, resp.StatusCode, strings.TrimSpace(string(payload)))
	}
	var out Response
	if err := json.Unmarshal(payload, &out); err != nil {
		return Response{}, fmt.Errorf("enroll: decode response: %w", err)
	}
	if out.CertPEM == "" || out.KeyPEM == "" || out.CARootPEM == "" {
		return Response{}, fmt.Errorf("enroll: response missing cert_pem, key_pem, or ca_root_pem")
	}
	return out, nil
}
