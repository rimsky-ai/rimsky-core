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

type AssetItem struct {
	Alias        string          `json:"alias"`
	ClaimID      string          `json:"claim_id"`
	ProducerName string          `json:"producer_name"`
	Scope        json.RawMessage `json:"scope,omitempty"`
	VersionID    string          `json:"version_id,omitempty"`
	State        string          `json:"state"`
	Lifetime     string          `json:"lifetime"`
	ClaimedAt    time.Time       `json:"claimed_at"`
	HolderNodeID string          `json:"holder_node_id"`
	NodeType     string          `json:"node_type,omitempty"`
}

type ListAssetsResponse struct {
	Assets     []AssetItem `json:"assets"`
	NextCursor string      `json:"next_cursor"`
}

type ListAssetsQuery struct {
	Cursor string
	Limit  int
}

func (c *Client) ListAssets(ctx context.Context, instanceID string, q ListAssetsQuery) (*ListAssetsResponse, error) {
	v := url.Values{}
	if q.Cursor != "" {
		v.Set("cursor", q.Cursor)
	}
	if q.Limit > 0 {
		v.Set("limit", strconv.Itoa(q.Limit))
	}
	path := "/v1/instances/" + url.PathEscape(instanceID) + "/assets"
	if encoded := v.Encode(); encoded != "" {
		path += "?" + encoded
	}
	req, err := c.request(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	var out ListAssetsResponse
	if err := c.do(req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func PagedListAssets(ctx context.Context, c *Client, instanceID string, q ListAssetsQuery) ([]AssetItem, error) {
	return PageAll(func(cursor string) ([]AssetItem, string, error) {
		q.Cursor = cursor
		page, err := c.ListAssets(ctx, instanceID, q)
		if err != nil {
			return nil, "", err
		}
		return page.Assets, page.NextCursor, nil
	})
}

func (c *Client) GetAsset(ctx context.Context, instanceID, alias string) (*AssetItem, error) {
	path := "/v1/instances/" + url.PathEscape(instanceID) + "/assets/" + url.PathEscape(alias)
	req, err := c.request(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	var out AssetItem
	if err := c.do(req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type AssetVersionsResponse struct {
	Versions []map[string]any `json:"versions,omitempty"`
	Error    string           `json:"error,omitempty"`
}

func (c *Client) GetAssetVersions(ctx context.Context, instanceID, alias string) (*AssetVersionsResponse, error) {
	path := "/v1/instances/" + url.PathEscape(instanceID) + "/assets/" + url.PathEscape(alias) + "/versions"
	req, err := c.request(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	var out AssetVersionsResponse
	if err := c.do(req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) DeleteAsset(ctx context.Context, instanceID, alias string) error {
	path := "/v1/instances/" + url.PathEscape(instanceID) + "/assets/" + url.PathEscape(alias)
	req, err := c.request(ctx, http.MethodDelete, path, nil)
	if err != nil {
		return err
	}
	return c.do(req, nil)
}

type AssetMaterializationHistoryResponse struct {
	MaterializationHistory []LineageRecordItem `json:"materialization_history"`
}

func (c *Client) GetAssetMaterializationHistory(ctx context.Context, instanceID, alias string) (*AssetMaterializationHistoryResponse, error) {
	path := "/v1/instances/" + url.PathEscape(instanceID) + "/assets/" + url.PathEscape(alias) + "/materialization-history"
	req, err := c.request(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	var out AssetMaterializationHistoryResponse
	if err := c.do(req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
