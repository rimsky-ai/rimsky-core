// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package cli

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// @concept: node
type NodeRunSummary struct {
	ActiveCount  int `json:"active_count"`
	PendingCount int `json:"pending_count"`
	FreshCount   int `json:"fresh_count"`
	FailedCount  int `json:"failed_count"`
}

// @concept: node
type Node struct {
	ID                 string          `json:"id"`
	InstanceID         string          `json:"instance_id"`
	NodeType           string          `json:"node_type"`
	Executor           string          `json:"executor,omitempty"`
	Tags               []string        `json:"tags,omitempty"`
	FrameID            string          `json:"frame_id,omitempty"`
	CreatedAt          string          `json:"created_at"`
	UpdatedAt          string          `json:"updated_at"`
	RunSummary         *NodeRunSummary `json:"run_summary,omitempty"`
	SettlingSignalType string          `json:"settling_signal_type,omitempty"`
}

// @concept: parked-state
type ParkedNodeEntry struct {
	InstanceID string     `json:"instance_id"`
	NodeID     string     `json:"node_id"`
	ParkedAt   time.Time  `json:"parked_at"`
	ResumeAt   *time.Time `json:"resume_at,omitempty"`
}

type ParkedNodesResponse struct {
	ParkedNodes []ParkedNodeEntry `json:"parked_nodes"`
}

func (c *Client) GetParkedNodes(ctx context.Context, path string) (*ParkedNodesResponse, error) {
	req, err := c.request(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	var out ParkedNodesResponse
	if err := c.do(req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) GetNode(ctx context.Context, id string) (*Node, error) {
	req, err := c.request(ctx, http.MethodGet, "/v1/nodes/"+url.PathEscape(id), nil)
	if err != nil {
		return nil, err
	}
	var out Node
	if err := c.do(req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) ResetNode(ctx context.Context, id string) error {
	req, err := c.request(ctx, http.MethodPost, "/v1/nodes/"+url.PathEscape(id)+"/reset", nil)
	if err != nil {
		return err
	}
	return c.do(req, nil)
}
