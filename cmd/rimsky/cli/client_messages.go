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

type MessageItem struct {
	ID            string          `json:"id"`
	InstanceID    string          `json:"instance_id"`
	Type          string          `json:"type"`
	Sender        string          `json:"sender"`
	SenderKind    string          `json:"sender_kind"`
	SenderSubject string          `json:"sender_subject"`
	Payload       json.RawMessage `json:"payload,omitempty"`
	ReceivedAt    time.Time       `json:"received_at"`
	DeliveredAt   *time.Time      `json:"delivered_at,omitempty"`
	FrameID       string          `json:"frame_id,omitempty"`
	Cancelled     bool            `json:"cancelled,omitempty"`
}

type ListMessagesQuery struct {
	Type            string
	SenderKind      string
	DeliveredAfter  string
	DeliveredBefore string
	Pending         *bool
	Cursor          string
	Limit           int
}

type ListMessagesResponse struct {
	Messages   []MessageItem `json:"messages"`
	NextCursor string        `json:"next_cursor,omitempty"`
}

func (c *Client) ListInstanceMessages(ctx context.Context, instanceID string, q ListMessagesQuery) (*ListMessagesResponse, error) {
	v := url.Values{}
	if q.Type != "" {
		v.Set("type", q.Type)
	}
	if q.SenderKind != "" {
		v.Set("sender_kind", q.SenderKind)
	}
	if q.DeliveredAfter != "" {
		v.Set("delivered_after", q.DeliveredAfter)
	}
	if q.DeliveredBefore != "" {
		v.Set("delivered_before", q.DeliveredBefore)
	}
	if q.Pending != nil {
		v.Set("pending", strconv.FormatBool(*q.Pending))
	}
	if q.Cursor != "" {
		v.Set("cursor", q.Cursor)
	}
	if q.Limit > 0 {
		v.Set("limit", strconv.Itoa(q.Limit))
	}
	path := "/v1/instances/" + url.PathEscape(instanceID) + "/messages"
	if encoded := v.Encode(); encoded != "" {
		path += "?" + encoded
	}
	req, err := c.request(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	var out ListMessagesResponse
	if err := c.do(req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type FrameItem struct {
	FrameID             string `json:"frame_id"`
	State               string `json:"state"`
	TriggeringMessageID string `json:"triggering_message_id"`
	MessageType         string `json:"message_type,omitempty"`
}

type ListFramesResponse struct {
	Frames     []FrameItem `json:"frames"`
	NextCursor string      `json:"next_cursor,omitempty"`
}

func (c *Client) ListInstanceFrames(ctx context.Context, instanceID, state string) (*ListFramesResponse, error) {
	path := "/v1/instances/" + url.PathEscape(instanceID) + "/frames"
	if state != "" {
		v := url.Values{}
		v.Set("state", state)
		path += "?" + v.Encode()
	}
	req, err := c.request(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	var out ListFramesResponse
	if err := c.do(req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) GetMessage(ctx context.Context, id string) (*MessageItem, error) {
	req, err := c.request(ctx, http.MethodGet, "/v1/messages/"+url.PathEscape(id), nil)
	if err != nil {
		return nil, err
	}
	var out MessageItem
	if err := c.do(req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type CreateInstanceMessageRequest struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

type CreateInstanceMessageResponse struct {
	MessageID string `json:"message_id"`
}

// @decision: empty-message-as-root-trigger
// @story: empty-message-wakes-roots
func (c *Client) CreateInstanceMessage(ctx context.Context, instanceID string, idempotencyKey string, body CreateInstanceMessageRequest) (*CreateInstanceMessageResponse, error) {
	req, err := c.request(ctx, http.MethodPost, "/v1/instances/"+url.PathEscape(instanceID)+"/messages", body)
	if err != nil {
		return nil, err
	}
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	var out CreateInstanceMessageResponse
	if err := c.do(req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
