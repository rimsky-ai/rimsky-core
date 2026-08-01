// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package cli

import (
	"context"
	"net/http"
)

type HealthResponse struct {
	Status      string              `json:"status"`
	Supervisors []SupervisorSummary `json:"supervisors"`
	NodeCounts  map[string]int      `json:"node_counts"`
}

type SupervisorSummary struct {
	ID              string `json:"id"`
	Concurrency     int    `json:"concurrency"`
	ActiveNodeCount int    `json:"active_node_count"`
}

func (c *Client) Health(ctx context.Context) (*HealthResponse, error) {
	req, err := c.request(ctx, http.MethodGet, "/v1/health", nil)
	if err != nil {
		return nil, err
	}
	var out HealthResponse
	if err := c.do(req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
