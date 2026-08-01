// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type LineageRecordItem struct {
	ID         string          `json:"id"`
	RecordKind string          `json:"record_kind"`
	InstanceID string          `json:"instance_id"`
	FrameID    string          `json:"frame_id"`
	ObservedAt time.Time       `json:"observed_at"`
	Record     json.RawMessage `json:"record"`
}

type LineageAncestorsResponse struct {
	Ancestors []LineageRecordItem `json:"ancestors"`
	Depth     int                 `json:"depth"`
}

func (c *Client) GetClaimAncestors(ctx context.Context, claimHandleID string, depth int) (*LineageAncestorsResponse, error) {
	v := url.Values{}
	if depth > 0 {
		v.Set("depth", strconv.Itoa(depth))
	}
	path := "/v1/lineage/claims/" + url.PathEscape(claimHandleID) + "/ancestors"
	if encoded := v.Encode(); encoded != "" {
		path += "?" + encoded
	}
	req, err := c.request(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	var out LineageAncestorsResponse
	if err := c.do(req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type PruneLineageRequest struct {
	Before string `json:"before"`
}

func (c *Client) PruneLineage(ctx context.Context, before string) (map[string]any, error) {
	req, err := c.request(ctx, http.MethodPost, "/v1/admin/lineage/prune", PruneLineageRequest{Before: before})
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := c.do(req, &out); err != nil {
		return nil, err
	}
	return out, nil
}
