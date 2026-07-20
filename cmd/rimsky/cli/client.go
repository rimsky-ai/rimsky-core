// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	endpoint      string
	httpClient    *http.Client
	userAgent     string
	apiKey        string
	composeOrigin bool
}

func NewClient(endpoint string) *Client {
	return &Client{
		endpoint:   strings.TrimRight(endpoint, "/"),
		httpClient: &http.Client{Timeout: 30 * time.Second},
		userAgent:  "rimsky",
	}
}

func (c *Client) SetAPIKey(key string) { c.apiKey = key }

func (c *Client) SetComposeOrigin(v bool) { c.composeOrigin = v }

func (c *Client) SetTimeout(d time.Duration) {
	c.httpClient.Timeout = d
}

func NewClientWithKey(endpoint, key string) *Client {
	c := NewClient(endpoint)
	c.SetAPIKey(key)
	return c
}

func (c *Client) Endpoint() string { return c.endpoint }

func (c *Client) RawCall(ctx context.Context, method, path string, body any, out any) (int, error) {
	req, err := c.request(ctx, method, path, body)
	if err != nil {
		return 0, err
	}
	status, err := c.doStatus(req, out)
	if err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) {
			return apiErr.Status, err
		}
		return status, err
	}
	return status, nil
}

func (c *Client) do(req *http.Request, out any) error {
	_, err := c.doStatus(req, out)
	return err
}

func (c *Client) doStatus(req *http.Request, out any) (int, error) {
	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	if c.composeOrigin {
		req.Header.Set("X-Rimsky-Compose-Origin", "1")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return resp.StatusCode, fmt.Errorf("read response body: %w", readErr)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		apiErr := &APIError{
			Status: resp.StatusCode,
			URL:    req.URL.String(),
			Method: req.Method,
		}
		if len(body) > 0 {
			_ = json.Unmarshal(body, &apiErr.Body)
		}
		return resp.StatusCode, apiErr
	}
	if out == nil || resp.StatusCode == http.StatusNoContent || len(body) == 0 {
		return resp.StatusCode, nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return resp.StatusCode, fmt.Errorf("decode response: %w", err)
	}
	return resp.StatusCode, nil
}

func (c *Client) request(ctx context.Context, method, path string, body any) (*http.Request, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.endpoint+path, reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	return req, nil
}
