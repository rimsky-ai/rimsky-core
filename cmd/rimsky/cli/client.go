// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

var stdErrorsAs = errors.As

type Client struct {
	endpoint      string
	httpClient    *http.Client
	userAgent     string
	apiKey        string
	composeOrigin bool
}

func NewClient(endpoint string) *Client {
	return &Client{
		endpoint:   strings.TrimRight(endpoint, "/"),
		httpClient: &http.Client{Timeout: 30 * time.Second},
		userAgent:  "rimsky",
	}
}

func (c *Client) SetAPIKey(key string) { c.apiKey = key }

func (c *Client) SetComposeOrigin(v bool) { c.composeOrigin = v }

func (c *Client) SetTimeout(d time.Duration) {
	c.httpClient.Timeout = d
}

func NewClientWithKey(endpoint, key string) *Client {
	c := NewClient(endpoint)
	c.SetAPIKey(key)
	return c
}

func (c *Client) Endpoint() string { return c.endpoint }

func (c *Client) RawCall(ctx context.Context, method, path string, body any, out any) (int, error) {
	req, err := c.request(ctx, method, path, body)
	if err != nil {
		return 0, err
	}
	status, err := c.doStatus(req, out)
	if err != nil {
		var apiErr *APIError
		if stdErrorsAs(err, &apiErr) {
			return apiErr.Status, err
		}
		return status, err
	}
	return status, nil
}

func (c *Client) do(req *http.Request, out any) error {
	_, err := c.doStatus(req, out)
	return err
}

func (c *Client) doStatus(req *http.Request, out any) (int, error) {
	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	if c.composeOrigin {
		req.Header.Set("X-Rimsky-Compose-Origin", "1")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, err
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
		return resp.StatusCode, apiErr
	}
	if out == nil || resp.StatusCode == http.StatusNoContent || len(body) == 0 {
		return resp.StatusCode, nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return resp.StatusCode, fmt.Errorf("decode response: %w", err)
	}
	return resp.StatusCode, nil
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

type CreateInstanceRequest struct {
	Template          string                 `json:"template"`
	InstanceKey       *string                `json:"instance_key,omitempty"`
	Params            map[string]any         `json:"params,omitempty"`
	ServiceBindings   map[string]bindingSpec `json:"service_bindings,omitempty"`
	TerminateAfterRun bool                   `json:"terminate_after_run,omitempty"`
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

// @concept: node
type NodeRunSummary struct {
	ActiveCount  int `json:"active_count"`
	PendingCount int `json:"pending_count"`
	FreshCount   int `json:"fresh_count"`
	FailedCount  int `json:"failed_count"`
}

// @concept: node
type Node struct {
	ID         string          `json:"id"`
	InstanceID string          `json:"instance_id"`
	NodeType   string          `json:"node_type"`
	Executor   string          `json:"executor,omitempty"`
	FrameID    string          `json:"frame_id,omitempty"`
	CreatedAt  string          `json:"created_at"`
	UpdatedAt  string          `json:"updated_at"`
	RunSummary *NodeRunSummary `json:"run_summary,omitempty"`
}

// @concept: parked-state
type ParkedNodeEntry struct {
	InstanceID string     `json:"instance_id"`
	NodeID     string     `json:"node_id"`
	ParkedAt   time.Time  `json:"parked_at"`
	ResumeAt   *time.Time `json:"resume_at,omitempty"`
	Reason     string     `json:"reason,omitempty"`
	ReasonNote string     `json:"reason_note,omitempty"`
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

type HealthResponse struct {
	Status      string              `json:"status"`
	Supervisors []SupervisorSummary `json:"supervisors"`
	NodeCounts  map[string]int      `json:"node_counts"`
}

type SupervisorSummary struct {
	ID                string   `json:"id"`
	AcceptedExecutors []string `json:"accepted_executors"`
	Concurrency       int      `json:"concurrency"`
	ActiveNodeCount   int      `json:"active_node_count"`
}

func (c *Client) Health(ctx context.Context) (*HealthResponse, error) {
	req, err := c.request(ctx, http.MethodGet, "/v1/health", nil)
	if err != nil {
		return nil, err
	}
	var out HealthResponse
	if err := c.do(req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type MessageItem struct {
	ID          string          `json:"id"`
	InstanceID  string          `json:"instance_id"`
	Type        string          `json:"type"`
	Sender      string          `json:"sender"`
	SenderKind  string          `json:"sender_kind"`
	Payload     json.RawMessage `json:"payload,omitempty"`
	ReceivedAt  time.Time       `json:"received_at"`
	DeliveredAt *time.Time      `json:"delivered_at,omitempty"`
	FrameID     string          `json:"frame_id,omitempty"`
	Cancelled   bool            `json:"cancelled,omitempty"`
}

type ListMessagesQuery struct {
	Type            string
	SenderKind      string
	DeliveredAfter  string
	DeliveredBefore string
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
	Assets []AssetItem `json:"assets"`
}

func (c *Client) ListAssets(ctx context.Context, instanceID string) (*ListAssetsResponse, error) {
	req, err := c.request(ctx, http.MethodGet, "/v1/instances/"+url.PathEscape(instanceID)+"/assets", nil)
	if err != nil {
		return nil, err
	}
	var out ListAssetsResponse
	if err := c.do(req, &out); err != nil {
		return nil, err
	}
	return &out, nil
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
