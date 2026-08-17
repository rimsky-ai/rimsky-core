// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package cli

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

type RegisterTemplateRequest struct {
	Spec   node.TemplateSpec `json:"spec"`
	Tag    string            `json:"tag,omitempty"`
	Source string            `json:"source,omitempty"`
}

type Template struct {
	TemplateID   string         `json:"template_id,omitempty"`
	ID           string         `json:"id,omitempty"`
	State        string         `json:"state,omitempty"`
	RegisteredAt string         `json:"registered_at,omitempty"`
	Source       string         `json:"source,omitempty"`
	Tags         []string       `json:"tags,omitempty"`
	Spec         map[string]any `json:"spec,omitempty"`
	NoOp         bool           `json:"no_op,omitempty"`

	// @story: validation-warnings-surfaced
	ValidationWarnings []ValidationFinding `json:"validation_warnings,omitempty"`
}

func (t *Template) Hash() string {
	if t.TemplateID != "" {
		return t.TemplateID
	}
	return t.ID
}

type ListTemplatesQuery struct {
	State  string
	Cursor string
	Limit  int
}

type ListTemplatesResponse struct {
	Templates  []Template `json:"templates"`
	NextCursor string     `json:"next_cursor,omitempty"`
}

func (c *Client) RegisterTemplate(ctx context.Context, body RegisterTemplateRequest) (*Template, error) {
	req, err := c.request(ctx, http.MethodPost, "/v1/templates", body)
	if err != nil {
		return nil, err
	}
	var out Template
	if err := c.do(req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type ValidationFinding struct {
	Path string `json:"path"`
	Msg  string `json:"msg"`
}

type ValidateResult struct {
	Ok                 bool                `json:"ok"`
	ValidationErrors   []ValidationFinding `json:"validation_errors"`
	ValidationWarnings []ValidationFinding `json:"validation_warnings"`
}

func (c *Client) ValidateTemplate(ctx context.Context, body RegisterTemplateRequest, warningsAsErrors bool) (*ValidateResult, error) {
	path := "/v1/templates/validate"
	if warningsAsErrors {
		path += "?warnings_as_errors=true"
	}
	req, err := c.request(ctx, http.MethodPost, path, body)
	if err != nil {
		return nil, err
	}
	var out ValidateResult
	if err := c.do(req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) ListTemplates(ctx context.Context, q ListTemplatesQuery) (*ListTemplatesResponse, error) {
	v := url.Values{}
	if q.State != "" {
		v.Set("state", q.State)
	}
	if q.Cursor != "" {
		v.Set("cursor", q.Cursor)
	}
	if q.Limit > 0 {
		v.Set("limit", strconv.Itoa(q.Limit))
	}
	path := "/v1/templates"
	if encoded := v.Encode(); encoded != "" {
		path += "?" + encoded
	}
	req, err := c.request(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	var out ListTemplatesResponse
	if err := c.do(req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) GetTemplate(ctx context.Context, ref string) (*Template, error) {
	req, err := c.request(ctx, http.MethodGet, "/v1/templates/"+url.PathEscape(ref), nil)
	if err != nil {
		return nil, err
	}
	var out Template
	if err := c.do(req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) DeployTemplate(ctx context.Context, ref string) (*Template, error) {
	req, err := c.request(ctx, http.MethodPost, "/v1/templates/"+url.PathEscape(ref)+"/deploy", nil)
	if err != nil {
		return nil, err
	}
	var out Template
	if err := c.do(req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) UndeployTemplate(ctx context.Context, ref string) (*Template, error) {
	req, err := c.request(ctx, http.MethodPost, "/v1/templates/"+url.PathEscape(ref)+"/undeploy", nil)
	if err != nil {
		return nil, err
	}
	var out Template
	if err := c.do(req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) DeleteTemplate(ctx context.Context, ref string) error {
	req, err := c.request(ctx, http.MethodDelete, "/v1/templates/"+url.PathEscape(ref), nil)
	if err != nil {
		return err
	}
	return c.do(req, nil)
}

type RegisterTemplateOptions struct {
	WarningsAsErrors bool
}

func (c *Client) RegisterTemplateWithOptions(ctx context.Context, body RegisterTemplateRequest, opts RegisterTemplateOptions) (*Template, error) {
	path := "/v1/templates"
	if opts.WarningsAsErrors {
		path += "?warnings_as_errors=true"
	}
	req, err := c.request(ctx, http.MethodPost, path, body)
	if err != nil {
		return nil, err
	}
	var out Template
	if err := c.do(req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
