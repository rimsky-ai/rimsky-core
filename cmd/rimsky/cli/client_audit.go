// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package cli

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

// @concept: event-log
type ListAuditQuery struct {
	Kind         string
	KeyID        string
	KeyName      string
	Action       string
	ActionPrefix string
	Target       string
	Mode         string
	Status       int
	Since        string
	Until        string
	Cursor       string
	Limit        int
}

type ListAuditResponse struct {
	Audit      []Event `json:"audit"`
	NextCursor string  `json:"next_cursor,omitempty"`
}

// @story: audit-log-read
func (c *Client) ListAudit(ctx context.Context, q ListAuditQuery) (*ListAuditResponse, error) {
	v := url.Values{}
	for name, value := range map[string]string{
		"kind":          q.Kind,
		"key_id":        q.KeyID,
		"key_name":      q.KeyName,
		"action":        q.Action,
		"action_prefix": q.ActionPrefix,
		"target":        q.Target,
		"mode":          q.Mode,
		"since":         q.Since,
		"until":         q.Until,
		"cursor":        q.Cursor,
	} {
		if value != "" {
			v.Set(name, value)
		}
	}
	if q.Status > 0 {
		v.Set("status", strconv.Itoa(q.Status))
	}
	if q.Limit > 0 {
		v.Set("limit", strconv.Itoa(q.Limit))
	}
	path := "/v1/audit"
	if encoded := v.Encode(); encoded != "" {
		path += "?" + encoded
	}
	req, err := c.request(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	var out ListAuditResponse
	if err := c.do(req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
