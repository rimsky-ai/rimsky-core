// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// client.go — typed HTTP client over the rimsky control-api. One method
// per endpoint; pure pass-through (no business logic). The control-api
// uses bare paths (no /v1/ prefix); methods here issue requests against
// the configured endpoint with those bare paths.
//
// Field names on request/response structs match the JSON shapes returned
// by the corresponding handlers under control/controlapi/. Field shapes were
// extracted from the live handlers — do not invent fields.
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/fallguy/rimsky/graph/node"
)

// Client issues requests against a single control-api endpoint. Safe for
// concurrent use; the underlying http.Client is shared.
type Client struct {
	endpoint   string
	httpClient *http.Client
	userAgent  string
}

// NewClient constructs a Client targeting the given endpoint URL (e.g.
// "http://localhost:8080"). The endpoint is stored verbatim; trailing
// slashes are stripped so path joining stays predictable.
func NewClient(endpoint string) *Client {
	return &Client{
		endpoint:   strings.TrimRight(endpoint, "/"),
		httpClient: &http.Client{Timeout: 30 * time.Second},
		userAgent:  "rimsky-cli",
	}
}

// Endpoint returns the configured endpoint URL.
func (c *Client) Endpoint() string { return c.endpoint }

// do executes req and decodes the JSON body into out (which may be nil
// for 204 responses or when the caller does not care). Non-2xx responses
// are returned as *APIError carrying the status code and the decoded body.
func (c *Client) do(req *http.Request, out any) error {
	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		apiErr := &APIError{
			Status: resp.StatusCode,
			URL:    req.URL.String(),
			Method: req.Method,
		}
		if len(body) > 0 {
			_ = json.Unmarshal(body, &apiErr.Body)
		}
		return apiErr
	}
	if out == nil || resp.StatusCode == http.StatusNoContent || len(body) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func (c *Client) request(ctx context.Context, method, path string, body any) (*http.Request, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.endpoint+path, reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	return req, nil
}

// ---------------------------------------------------------------------
// Templates
// ---------------------------------------------------------------------

// RegisterTemplateRequest is the wrapped POST /templates body shape per
// control-plane v1 spec §1.5: `{spec: {...}, tag, source}`. Spec is the
// typed template spec; tag and source are optional.
type RegisterTemplateRequest struct {
	Spec   node.TemplateSpec `json:"spec"`
	Tag    string            `json:"tag,omitempty"`
	Source string            `json:"source,omitempty"`
}

// Template is the response shape returned by POST /templates and
// GET /templates/{ref}. POST returns {template_id, tags}; GET returns
// {id, state, registered_at, source, tags, spec}. Both shapes share
// the fields below; absent fields decode to zero values.
type Template struct {
	// POST /templates returns this as "template_id"; GET as "id".
	TemplateID   string         `json:"template_id,omitempty"`
	ID           string         `json:"id,omitempty"`
	State        string         `json:"state,omitempty"`
	RegisteredAt string         `json:"registered_at,omitempty"`
	Source       string         `json:"source,omitempty"`
	Tags         []string       `json:"tags,omitempty"`
	Spec         map[string]any `json:"spec,omitempty"`
	// Deploy/undeploy responses include these:
	NoOp bool `json:"no_op,omitempty"`
}

// Hash returns the template's content hash regardless of which field the
// control-api populated (POST uses TemplateID, GET uses ID).
func (t *Template) Hash() string {
	if t.TemplateID != "" {
		return t.TemplateID
	}
	return t.ID
}

// ListTemplatesQuery captures the supported query parameters for
// GET /templates: state filter and cursor pagination.
type ListTemplatesQuery struct {
	State  string
	Cursor string
	Limit  int
}

// ListTemplatesResponse is the GET /templates body shape.
type ListTemplatesResponse struct {
	Templates  []Template `json:"templates"`
	NextCursor string     `json:"next_cursor,omitempty"`
}

// RegisterTemplate calls POST /templates.
func (c *Client) RegisterTemplate(ctx context.Context, body RegisterTemplateRequest) (*Template, error) {
	req, err := c.request(ctx, http.MethodPost, "/templates", body)
	if err != nil {
		return nil, err
	}
	var out Template
	if err := c.do(req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListTemplates calls GET /templates with optional state filter and cursor.
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
	path := "/templates"
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

// GetTemplate calls GET /templates/{ref} (tag or hash).
func (c *Client) GetTemplate(ctx context.Context, ref string) (*Template, error) {
	req, err := c.request(ctx, http.MethodGet, "/templates/"+url.PathEscape(ref), nil)
	if err != nil {
		return nil, err
	}
	var out Template
	if err := c.do(req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeployTemplate calls POST /templates/{ref}/deploy.
func (c *Client) DeployTemplate(ctx context.Context, ref string) (*Template, error) {
	req, err := c.request(ctx, http.MethodPost, "/templates/"+url.PathEscape(ref)+"/deploy", nil)
	if err != nil {
		return nil, err
	}
	var out Template
	if err := c.do(req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UndeployTemplate calls POST /templates/{ref}/undeploy.
func (c *Client) UndeployTemplate(ctx context.Context, ref string) (*Template, error) {
	req, err := c.request(ctx, http.MethodPost, "/templates/"+url.PathEscape(ref)+"/undeploy", nil)
	if err != nil {
		return nil, err
	}
	var out Template
	if err := c.do(req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteTemplate calls DELETE /templates/{ref}.
func (c *Client) DeleteTemplate(ctx context.Context, ref string) error {
	req, err := c.request(ctx, http.MethodDelete, "/templates/"+url.PathEscape(ref), nil)
	if err != nil {
		return err
	}
	return c.do(req, nil)
}

// ---------------------------------------------------------------------
// Tags
// ---------------------------------------------------------------------

// CreateTagRequest is the POST /tags body shape.
type CreateTagRequest struct {
	Tag      string `json:"tag"`
	Template string `json:"template"`
}

// MoveTagRequest is the PUT /tags/{tag} body shape.
type MoveTagRequest struct {
	Template string `json:"template"`
}

// Tag is the GET /tags row shape (and the response of POST/PUT).
type Tag struct {
	Tag        string `json:"tag"`
	TemplateID string `json:"template_id"`
	UpdatedAt  string `json:"updated_at,omitempty"`
}

// ListTagsQuery is the GET /tags query parameters.
type ListTagsQuery struct {
	Cursor string
	Limit  int
}

// ListTagsResponse is the GET /tags response shape.
type ListTagsResponse struct {
	Tags       []Tag  `json:"tags"`
	NextCursor string `json:"next_cursor,omitempty"`
}

// CreateTag calls POST /tags.
func (c *Client) CreateTag(ctx context.Context, body CreateTagRequest) (*Tag, error) {
	req, err := c.request(ctx, http.MethodPost, "/tags", body)
	if err != nil {
		return nil, err
	}
	var out Tag
	if err := c.do(req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListTags calls GET /tags.
func (c *Client) ListTags(ctx context.Context, q ListTagsQuery) (*ListTagsResponse, error) {
	v := url.Values{}
	if q.Cursor != "" {
		v.Set("cursor", q.Cursor)
	}
	if q.Limit > 0 {
		v.Set("limit", strconv.Itoa(q.Limit))
	}
	path := "/tags"
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

// MoveTag calls PUT /tags/{tag}.
func (c *Client) MoveTag(ctx context.Context, tag string, body MoveTagRequest) (*Tag, error) {
	req, err := c.request(ctx, http.MethodPut, "/tags/"+url.PathEscape(tag), body)
	if err != nil {
		return nil, err
	}
	var out Tag
	if err := c.do(req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteTag calls DELETE /tags/{tag}.
func (c *Client) DeleteTag(ctx context.Context, tag string) error {
	req, err := c.request(ctx, http.MethodDelete, "/tags/"+url.PathEscape(tag), nil)
	if err != nil {
		return err
	}
	return c.do(req, nil)
}

// ---------------------------------------------------------------------
// Instances
// ---------------------------------------------------------------------

// CreateInstanceRequest is the POST /instances body shape per
// control-plane v1 spec §2.2.
type CreateInstanceRequest struct {
	Template    string         `json:"template"`
	InstanceKey *string        `json:"instance_key,omitempty"`
	Params      map[string]any `json:"params,omitempty"`
}

// Instance is the unified shape of POST /instances, GET /instances row,
// and GET /instances/{idOrKey}. The control-api responds slightly
// differently across these (POST returns {instance_id, template_hash,
// instance_key, node_count}; GET returns {id, template_hash,
// instance_key, params, created_at, terminated_at}). Both shapes share
// the fields below; absent fields decode to zero values.
type Instance struct {
	// POST returns InstanceID; GET returns ID.
	InstanceID   string         `json:"instance_id,omitempty"`
	ID           string         `json:"id,omitempty"`
	TemplateHash string         `json:"template_hash,omitempty"`
	InstanceKey  *string        `json:"instance_key,omitempty"`
	Params       map[string]any `json:"params,omitempty"`
	CreatedAt    string         `json:"created_at,omitempty"`
	TerminatedAt *string        `json:"terminated_at,omitempty"`
	NodeCount    int            `json:"node_count,omitempty"`
}

// UUID returns the instance UUID regardless of which field the
// control-api populated (POST uses InstanceID, GET uses ID).
func (i *Instance) UUID() string {
	if i.InstanceID != "" {
		return i.InstanceID
	}
	return i.ID
}

// ListInstancesQuery is GET /instances query params. The control-api
// does not filter by instance_key on this endpoint — instance-key
// lookups go through GET /instances/{idOrKey}.
type ListInstancesQuery struct {
	TemplateHash string
	Cursor       string
	Limit        int
}

// ListInstancesResponse is GET /instances response shape.
type ListInstancesResponse struct {
	Instances  []Instance `json:"instances"`
	NextCursor string     `json:"next_cursor,omitempty"`
}

// ListInstanceNodesResponse is GET /instances/{idOrKey}/nodes shape.
type ListInstanceNodesResponse struct {
	Nodes      []Node `json:"nodes"`
	NextCursor string `json:"next_cursor,omitempty"`
}

// CreateInstance calls POST /instances.
func (c *Client) CreateInstance(ctx context.Context, body CreateInstanceRequest) (*Instance, error) {
	req, err := c.request(ctx, http.MethodPost, "/instances", body)
	if err != nil {
		return nil, err
	}
	var out Instance
	if err := c.do(req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListInstances calls GET /instances.
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
	path := "/instances"
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

// GetInstance calls GET /instances/{idOrKey}.
func (c *Client) GetInstance(ctx context.Context, idOrKey string) (*Instance, error) {
	req, err := c.request(ctx, http.MethodGet, "/instances/"+url.PathEscape(idOrKey), nil)
	if err != nil {
		return nil, err
	}
	var out Instance
	if err := c.do(req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteInstance calls DELETE /instances/{idOrKey}.
func (c *Client) DeleteInstance(ctx context.Context, idOrKey string) error {
	req, err := c.request(ctx, http.MethodDelete, "/instances/"+url.PathEscape(idOrKey), nil)
	if err != nil {
		return err
	}
	return c.do(req, nil)
}

// ListInstanceNodes calls GET /instances/{idOrKey}/nodes.
func (c *Client) ListInstanceNodes(ctx context.Context, idOrKey string) (*ListInstanceNodesResponse, error) {
	req, err := c.request(ctx, http.MethodGet, "/instances/"+url.PathEscape(idOrKey)+"/nodes", nil)
	if err != nil {
		return nil, err
	}
	var out ListInstanceNodesResponse
	if err := c.do(req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ---------------------------------------------------------------------
// Nodes
// ---------------------------------------------------------------------

// Node is the shape returned by GET /nodes/{id}.
type Node struct {
	ID                   string   `json:"id"`
	InstanceID           string   `json:"instance_id"`
	NodeType             string   `json:"node_type"`
	Executor             string   `json:"executor,omitempty"`
	ScheduleCron         string   `json:"schedule_cron,omitempty"`
	State                string   `json:"state"`
	Dependencies         []string `json:"dependencies"`
	CurrentErrorClass    string   `json:"current_error_class,omitempty"`
	RetryCounter         int      `json:"retry_counter"`
	ActionIndex          int      `json:"action_index"`
	LastHeartbeatAt      *string  `json:"last_heartbeat_at,omitempty"`
	AssignedSupervisorID string   `json:"assigned_supervisor_id,omitempty"`
	FrameID              string   `json:"frame_id,omitempty"`
	CreatedAt            string   `json:"created_at"`
	UpdatedAt            string   `json:"updated_at"`
}

// InvalidateNodeRequest is the POST /nodes/{id}/invalidate body.
type InvalidateNodeRequest struct {
	Reason string `json:"reason,omitempty"`
	// Frame controls the per-emit frame discipline ("" | "in" | "next").
	// Default "" → "next". See the reactive-loops + lifecycle-handlers
	// spec §5.
	Frame string `json:"frame,omitempty"`
}

// GetNode calls GET /nodes/{id}.
func (c *Client) GetNode(ctx context.Context, id string) (*Node, error) {
	req, err := c.request(ctx, http.MethodGet, "/nodes/"+url.PathEscape(id), nil)
	if err != nil {
		return nil, err
	}
	var out Node
	if err := c.do(req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// InvalidateNode calls POST /nodes/{id}/invalidate.
func (c *Client) InvalidateNode(ctx context.Context, id string, body InvalidateNodeRequest) error {
	req, err := c.request(ctx, http.MethodPost, "/nodes/"+url.PathEscape(id)+"/invalidate", body)
	if err != nil {
		return err
	}
	return c.do(req, nil)
}

// ResetNode calls POST /nodes/{id}/reset.
func (c *Client) ResetNode(ctx context.Context, id string) error {
	req, err := c.request(ctx, http.MethodPost, "/nodes/"+url.PathEscape(id)+"/reset", nil)
	if err != nil {
		return err
	}
	return c.do(req, nil)
}

// ---------------------------------------------------------------------
// Events
// ---------------------------------------------------------------------

// Event is one row of GET /events.
type Event struct {
	ID         int64          `json:"id"`
	InstanceID string         `json:"instance_id,omitempty"`
	NodeID     string         `json:"node_id,omitempty"`
	Kind       string         `json:"kind"`
	Payload    map[string]any `json:"payload"`
	OccurredAt string         `json:"occurred_at"`
}

// ListEventsQuery is the GET /events query params.
type ListEventsQuery struct {
	InstanceID string
	NodeID     string
	Kind       string
	Since      string
	Until      string
	Cursor     string
	Limit      int
}

// ListEventsResponse is the GET /events response.
type ListEventsResponse struct {
	Events     []Event `json:"events"`
	NextCursor string  `json:"next_cursor,omitempty"`
}

// ListEvents calls GET /events.
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
	path := "/events"
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

// ---------------------------------------------------------------------
// Admin
// ---------------------------------------------------------------------

// AdminForceFire calls POST /admin/scheduled-nodes/{node_id}/force-fire
// and returns nil on 204.
func (c *Client) AdminForceFire(ctx context.Context, nodeID string) error {
	req, err := c.request(ctx, http.MethodPost, "/admin/scheduled-nodes/"+url.PathEscape(nodeID)+"/force-fire", nil)
	if err != nil {
		return err
	}
	return c.do(req, nil)
}

// ---------------------------------------------------------------------
// Health
// ---------------------------------------------------------------------

// HealthResponse mirrors GET /health.
type HealthResponse struct {
	Status      string              `json:"status"`
	Supervisors []SupervisorSummary `json:"supervisors"`
	NodeCounts  map[string]int      `json:"node_counts"`
}

// SupervisorSummary is one entry of HealthResponse.Supervisors.
type SupervisorSummary struct {
	ID                string   `json:"id"`
	AcceptedExecutors []string `json:"accepted_executors"`
	Concurrency       int      `json:"concurrency"`
	ActiveNodeCount   int      `json:"active_node_count"`
	LastHeartbeatAt   string   `json:"last_heartbeat_at"`
}

// Health calls GET /health.
func (c *Client) Health(ctx context.Context) (*HealthResponse, error) {
	req, err := c.request(ctx, http.MethodGet, "/health", nil)
	if err != nil {
		return nil, err
	}
	var out HealthResponse
	if err := c.do(req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
