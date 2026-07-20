// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package cli

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

type Event struct {
	ID         int64          `json:"id"`
	InstanceID string         `json:"instance_id,omitempty"`
	NodeID     string         `json:"node_id,omitempty"`
	Kind       string         `json:"kind"`
	Payload    map[string]any `json:"payload"`
	OccurredAt string         `json:"occurred_at"`
}

type ListEventsQuery struct {
	InstanceID string
	NodeID     string
	Kind       string
	Since      string
	Until      string
	Cursor     string
	Limit      int
}

type ListEventsResponse struct {
	Events     []Event `json:"events"`
	NextCursor string  `json:"next_cursor,omitempty"`
}

func (c *Client) ListEvents(ctx context.Context, q ListEventsQuery) (*ListEventsResponse, error) {
	v := url.Values{}
	if q.InstanceID != "" {
		v.Set("instance_id", q.InstanceID)
	}
	if q.NodeID != "" {
		v.Set("node_id", q.NodeID)
	}
	if q.Kind != "" {
		v.Set("kind", q.Kind)
	}
	if q.Since != "" {
		v.Set("since", q.Since)
	}
	if q.Until != "" {
		v.Set("until", q.Until)
	}
	if q.Cursor != "" {
		v.Set("cursor", q.Cursor)
	}
	if q.Limit > 0 {
		v.Set("limit", strconv.Itoa(q.Limit))
	}
	path := "/v1/events"
	if encoded := v.Encode(); encoded != "" {
		path += "?" + encoded
	}
	req, err := c.request(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	var out ListEventsResponse
	if err := c.do(req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
