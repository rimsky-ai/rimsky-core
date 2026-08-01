// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package cli

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

type CreateTagRequest struct {
	Tag      string `json:"tag"`
	Template string `json:"template"`
}

type MoveTagRequest struct {
	Template string `json:"template"`
}

type Tag struct {
	Tag        string `json:"tag"`
	TemplateID string `json:"template_id"`
	UpdatedAt  string `json:"updated_at,omitempty"`
}

type ListTagsQuery struct {
	Cursor string
	Limit  int
}

type ListTagsResponse struct {
	Tags       []Tag  `json:"tags"`
	NextCursor string `json:"next_cursor,omitempty"`
}

func (c *Client) CreateTag(ctx context.Context, body CreateTagRequest) (*Tag, error) {
	req, err := c.request(ctx, http.MethodPost, "/v1/tags", body)
	if err != nil {
		return nil, err
	}
	var out Tag
	if err := c.do(req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) ListTags(ctx context.Context, q ListTagsQuery) (*ListTagsResponse, error) {
	v := url.Values{}
	if q.Cursor != "" {
		v.Set("cursor", q.Cursor)
	}
	if q.Limit > 0 {
		v.Set("limit", strconv.Itoa(q.Limit))
	}
	path := "/v1/tags"
	if encoded := v.Encode(); encoded != "" {
		path += "?" + encoded
	}
	req, err := c.request(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	var out ListTagsResponse
	if err := c.do(req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) MoveTag(ctx context.Context, tag string, body MoveTagRequest) (*Tag, error) {
	req, err := c.request(ctx, http.MethodPut, "/v1/tags/"+url.PathEscape(tag), body)
	if err != nil {
		return nil, err
	}
	var out Tag
	if err := c.do(req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) DeleteTag(ctx context.Context, tag string) error {
	req, err := c.request(ctx, http.MethodDelete, "/v1/tags/"+url.PathEscape(tag), nil)
	if err != nil {
		return err
	}
	return c.do(req, nil)
}
