// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package cli

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

type CreateInstanceRequest struct {
	Template        string                 `json:"template"`
	InstanceKey     *string                `json:"instance_key,omitempty"`
	Params          map[string]any         `json:"params,omitempty"`
	ServiceBindings map[string]bindingSpec `json:"service_bindings,omitempty"`
	// @concept: anonymous-mode
	TargetAgent string `json:"target_agent,omitempty"`
}

type bindingSpec struct {
	Path string `json:"path"`
}

type Instance struct {
	InstanceID   string         `json:"instance_id,omitempty"`
	ID           string         `json:"id,omitempty"`
	TemplateHash string         `json:"template_hash,omitempty"`
	InstanceKey  *string        `json:"instance_key,omitempty"`
	Params       map[string]any `json:"params,omitempty"`
	CreatedAt    string         `json:"created_at,omitempty"`
	TerminatedAt *string        `json:"terminated_at,omitempty"`
	NodeCount    int            `json:"node_count,omitempty"`
}

func (i *Instance) UUID() string {
	if i.InstanceID != "" {
		return i.InstanceID
	}
	return i.ID
}

type ListInstancesQuery struct {
	TemplateHash string
	Cursor       string
	Limit        int
}

type ListInstancesResponse struct {
	Instances  []Instance `json:"instances"`
	NextCursor string     `json:"next_cursor,omitempty"`
}

type ListInstanceNodesResponse struct {
	Nodes      []Node `json:"nodes"`
	NextCursor string `json:"next_cursor,omitempty"`
}

func (c *Client) CreateInstance(ctx context.Context, body CreateInstanceRequest) (*Instance, error) {
	req, err := c.request(ctx, http.MethodPost, "/v1/instances", body)
	if err != nil {
		return nil, err
	}
	var out Instance
	if err := c.do(req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) ListInstances(ctx context.Context, q ListInstancesQuery) (*ListInstancesResponse, error) {
	v := url.Values{}
	if q.TemplateHash != "" {
		v.Set("template_hash", q.TemplateHash)
	}
	if q.Cursor != "" {
		v.Set("cursor", q.Cursor)
	}
	if q.Limit > 0 {
		v.Set("limit", strconv.Itoa(q.Limit))
	}
	path := "/v1/instances"
	if encoded := v.Encode(); encoded != "" {
		path += "?" + encoded
	}
	req, err := c.request(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	var out ListInstancesResponse
	if err := c.do(req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) GetInstance(ctx context.Context, idOrKey string) (*Instance, error) {
	req, err := c.request(ctx, http.MethodGet, "/v1/instances/"+url.PathEscape(idOrKey), nil)
	if err != nil {
		return nil, err
	}
	var out Instance
	if err := c.do(req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) DeleteInstance(ctx context.Context, idOrKey string) error {
	req, err := c.request(ctx, http.MethodDelete, "/v1/instances/"+url.PathEscape(idOrKey), nil)
	if err != nil {
		return err
	}
	return c.do(req, nil)
}

func (c *Client) TerminateInstance(ctx context.Context, idOrKey string, reason string) (*Instance, error) {
	body := map[string]string{"reason": reason}
	req, err := c.request(ctx, http.MethodPost, "/v1/instances/"+url.PathEscape(idOrKey)+"/terminate", body)
	if err != nil {
		return nil, err
	}
	var out Instance
	if err := c.do(req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) ListInstanceNodes(ctx context.Context, idOrKey string) (*ListInstanceNodesResponse, error) {
	req, err := c.request(ctx, http.MethodGet, "/v1/instances/"+url.PathEscape(idOrKey)+"/nodes", nil)
	if err != nil {
		return nil, err
	}
	var out ListInstanceNodesResponse
	if err := c.do(req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type BreakpointHitsResponse struct {
	Hits      []map[string]any `json:"hits"`
	NextSince int64            `json:"next_since"`
	Truncated bool             `json:"truncated"`
}

func (c *Client) ListBreakpointHits(ctx context.Context, idOrKey string, since int64, limit int) (*BreakpointHitsResponse, error) {
	v := url.Values{}
	if since > 0 {
		v.Set("since", strconv.FormatInt(since, 10))
	}
	if limit > 0 {
		v.Set("limit", strconv.Itoa(limit))
	}
	path := "/v1/instances/" + url.PathEscape(idOrKey) + "/breakpoint-hits"
	if encoded := v.Encode(); encoded != "" {
		path += "?" + encoded
	}
	req, err := c.request(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	var out BreakpointHitsResponse
	if err := c.do(req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
