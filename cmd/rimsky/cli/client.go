// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// client.go — typed HTTP client over the rimsky control-api. One method
// per endpoint; pure pass-through (no business logic). The control-api
// is mounted under the /v1/ version prefix (see
// tension:control-api-version-prefix); every URL constructed here
// starts with /v1/.
//
// Field names on request/response structs match the JSON shapes returned
// by the corresponding handlers under control/controlapi/. Field shapes were
// extracted from the live handlers — do not invent fields.
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

// stdErrorsAs is a file-scope alias for errors.As; kept as a thin
// indirection so the call sites read cleanly.
var stdErrorsAs = errors.As

// Client issues requests against a single control-api endpoint. Safe for
// concurrent use; the underlying http.Client is shared.
type Client struct {
	endpoint   string
	httpClient *http.Client
	userAgent  string
	apiKey     string
	// composeOrigin, when set, stamps the trusted compose-origin marker
	// (`X-Rimsky-Compose-Origin: 1`) on every request. The compose engine
	// sets this on the client it builds so its writes — which legitimately
	// own the reserved `compose:` tag / instance_key prefix — pass the
	// control-api's server-side reserved-prefix guard. Off by default: a
	// plain CLI client never claims compose origin.
	composeOrigin bool
}

// NewClient constructs a Client targeting the given endpoint URL (e.g.
// "http://localhost:8080"). The endpoint is stored verbatim; trailing
// slashes are stripped so path joining stays predictable.
func NewClient(endpoint string) *Client {
	return &Client{
		endpoint:   strings.TrimRight(endpoint, "/"),
		httpClient: &http.Client{Timeout: 30 * time.Second},
		userAgent:  "rimsky",
	}
}

// SetAPIKey installs the Bearer token forwarded on every request as
// `Authorization: Bearer <key>`. Empty string clears the header.
func (c *Client) SetAPIKey(key string) { c.apiKey = key }

// SetComposeOrigin toggles the trusted compose-origin marker stamped on
// every request. The compose engine calls this with true on the client
// it builds so its reserved-prefix (`compose:`) tag / instance_key writes
// pass the control-api's server-side reserved-prefix guard. Off by
// default.
func (c *Client) SetComposeOrigin(v bool) { c.composeOrigin = v }

// SetTimeout overrides the per-request HTTP timeout. The default 30s
// is conservative for an over-the-network deployed-stack client; tighter
// per-request timeouts are appropriate for callers polling a loopback
// endpoint (e.g., the `compose run` verb's terminal-wait loop, where a
// downed control-api should surface as a fast connection-refused rather
// than waiting on the OS dial timeout). Pass d <= 0 to disable the
// per-request timeout entirely.
func (c *Client) SetTimeout(d time.Duration) {
	c.httpClient.Timeout = d
}

// NewClientWithKey is a convenience constructor that installs the
// Bearer token in one call. Equivalent to NewClient(endpoint) +
// SetAPIKey(key).
func NewClientWithKey(endpoint, key string) *Client {
	c := NewClient(endpoint)
	c.SetAPIKey(key)
	return c
}

// Endpoint returns the configured endpoint URL.
func (c *Client) Endpoint() string { return c.endpoint }

// RawCall is a generic "issue a request, JSON-decode the response
// into out" verb that the per-endpoint Client methods don't cover.
// Used by `cmd/rimsky/auth_*` so the auth subcommands can drop their
// bespoke httpRequest / doRequest helpers and share the Client's
// transport, user-agent, and Bearer-injection.
//
// @agent-contract RawCall: typed HTTP verb returning JSON. Pass
// (method, path, body, out) — body may be nil; out may be nil for
// fire-and-forget; the returned status code mirrors the underlying
// HTTP response (or 0 on transport failure). On 2xx the response body
// is unmarshalled into out; on non-2xx returns *APIError with the
// decoded body fields preserved. Does NOT handle streaming responses,
// multipart bodies, or non-JSON content types (the response body is
// JSON-decoded unconditionally when out is non-nil and the body is
// non-empty). Safe for concurrent use; the underlying http.Client is
// shared.
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

// do executes req and decodes the JSON body into out (which may be nil
// for 204 responses or when the caller does not care). Non-2xx responses
// are returned as *APIError carrying the status code and the decoded body.
//
// Kept as a thin wrapper around doStatus so existing per-endpoint
// methods (which don't surface the status code) continue to compile
// without per-call edits.
func (c *Client) do(req *http.Request, out any) error {
	_, err := c.doStatus(req, out)
	return err
}

// doStatus is the workhorse: executes req, decodes the JSON body into
// out, and returns the underlying HTTP response status alongside any
// error. Returns status=0 on transport failure (the response never
// came back). On non-2xx the error is *APIError; the status is
// returned alongside for consumers that want both.
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
	// @constraint: POST /templates returns this as "template_id"; GET as "id".
	TemplateID   string         `json:"template_id,omitempty"`
	ID           string         `json:"id,omitempty"`
	State        string         `json:"state,omitempty"`
	RegisteredAt string         `json:"registered_at,omitempty"`
	Source       string         `json:"source,omitempty"`
	Tags         []string       `json:"tags,omitempty"`
	Spec         map[string]any `json:"spec,omitempty"`
	// @constraint: deploy/undeploy responses include these.
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

// ValidationFinding is one entry in a validate response — the unified
// {path, msg} projection the control-api flattens both static and
// pipeline findings into.
type ValidationFinding struct {
	Path string `json:"path"`
	Msg  string `json:"msg"`
}

// ValidateResult is the POST /templates/validate body shape. Validation
// always runs (HTTP 200); Ok carries the verdict and the two finding
// slices carry detail.
type ValidateResult struct {
	Ok                 bool                `json:"ok"`
	ValidationErrors   []ValidationFinding `json:"validation_errors"`
	ValidationWarnings []ValidationFinding `json:"validation_warnings"`
}

// ValidateTemplate calls POST /templates/validate: run the full
// registration validation pipeline without persisting. The endpoint
// returns HTTP 200 even for an invalid spec, so a non-nil error here
// signals a transport/request-level failure, not a lint failure — the
// caller reads ValidateResult.Ok for the verdict. When warningsAsErrors
// is set, the server folds any warnings into the Ok=false verdict (the
// `?warnings_as_errors=true` query param).
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

// GetTemplate calls GET /templates/{ref} (tag or hash).
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

// DeployTemplate calls POST /templates/{ref}/deploy.
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

// UndeployTemplate calls POST /templates/{ref}/undeploy.
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

// DeleteTemplate calls DELETE /templates/{ref}.
func (c *Client) DeleteTemplate(ctx context.Context, ref string) error {
	req, err := c.request(ctx, http.MethodDelete, "/v1/templates/"+url.PathEscape(ref), nil)
	if err != nil {
		return err
	}
	return c.do(req, nil)
}

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

// ListTags calls GET /tags.
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

// MoveTag calls PUT /tags/{tag}.
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

// DeleteTag calls DELETE /tags/{tag}.
func (c *Client) DeleteTag(ctx context.Context, tag string) error {
	req, err := c.request(ctx, http.MethodDelete, "/v1/tags/"+url.PathEscape(tag), nil)
	if err != nil {
		return err
	}
	return c.do(req, nil)
}

// CreateInstanceRequest is the POST /instances body shape per
// control-plane v1 spec §2.2.
type CreateInstanceRequest struct {
	Template    string         `json:"template"`
	InstanceKey *string        `json:"instance_key,omitempty"`
	Params      map[string]any `json:"params,omitempty"`
	// ServiceBindings is the per-instance late-bound service catalog,
	// shape {<name>: {"path": "<binary-path>"}}. Opaque to the control-api
	// (stored verbatim as JSONB); consumed by the host-agent-proxy at
	// dispatch time. Omitted from the body when no --service flags are
	// supplied. Per spec 2026-05-24-host-agent-and-proxy-design.md.
	ServiceBindings map[string]bindingSpec `json:"service_bindings,omitempty"`
	// TerminateAfterRun opts the instance into self-termination once its
	// nodes settle. Default false → durable-by-default (the instance
	// stays alive across dispatches and only terminates on explicit
	// DELETE). Set true for one-shot dev-loop invocations where the
	// caller needs `terminated_at` to flip so `rimsky watch` (and any
	// other polled-on-terminal client) can exit cleanly.
	TerminateAfterRun bool `json:"terminate_after_run,omitempty"`
}

// bindingSpec is the CLI's view of one service binding: the path the agent
// exec()s. Other fields (args, env, cwd) are additive and unknown JSON
// fields are ignored on the proxy side. Mirrors the proxy's binding shape
// without coupling to its package (the proxy is package main).
//
// @source: cmd/rimsky-host-agent-proxy/state.go::bindingSpec
type bindingSpec struct {
	Path string `json:"path"`
}

// Instance is the unified shape of POST /instances, GET /instances row,
// and GET /instances/{idOrKey}. The control-api responds slightly
// differently across these (POST returns {instance_id, template_hash,
// instance_key, node_count}; GET returns {id, template_hash,
// instance_key, params, created_at, terminated_at}). Both shapes share
// the fields below; absent fields decode to zero values.
type Instance struct {
	// @constraint: POST returns InstanceID; GET returns ID.
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

// GetInstance calls GET /instances/{idOrKey}.
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

// DeleteInstance calls DELETE /instances/{idOrKey}.
func (c *Client) DeleteInstance(ctx context.Context, idOrKey string) error {
	req, err := c.request(ctx, http.MethodDelete, "/v1/instances/"+url.PathEscape(idOrKey), nil)
	if err != nil {
		return err
	}
	return c.do(req, nil)
}

// TerminateInstance calls POST /instances/{idOrKey}/terminate, force-
// terminating the instance (marking it terminal and force-failing its
// resource-holding node-runs). The optional reason is recorded on the
// administrative audit event. The handler responds with the updated
// instance projection (200), which decodes into *Instance. Terminate is
// idempotent: an already-terminal instance returns its current
// projection unchanged.
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

// ListInstanceNodes calls GET /instances/{idOrKey}/nodes.
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

// BreakpointHitsResponse is the GET /instances/{idOrKey}/breakpoint-hits
// body shape. Each hit is a flat object (the row-identity fields — seq,
// hit_id, breakpoint_id, … — alongside the snapshot map's top-level keys),
// so it is decoded as an untyped map rather than a fixed struct: the
// snapshot contents vary by checkpoint and are surfaced verbatim for the
// status/watch aggregators. NextSince is the highest seq on the page (a
// since-cursor for the next poll); Truncated reports whether a row exists
// beyond the requested page.
type BreakpointHitsResponse struct {
	Hits      []map[string]any `json:"hits"`
	NextSince int64            `json:"next_since"`
	Truncated bool             `json:"truncated"`
}

// ListBreakpointHits calls GET /instances/{idOrKey}/breakpoint-hits with
// the `?since=<seq>&limit=<n>` pagination cursor. since=0 starts from the
// beginning; limit<=0 omits the param so the server applies its default.
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

// Node is the shape returned by GET /nodes/{id}.
type Node struct {
	ID                   string  `json:"id"`
	InstanceID           string  `json:"instance_id"`
	NodeType             string  `json:"node_type"`
	Executor             string  `json:"executor,omitempty"`
	State                string  `json:"state"`
	SettlingSignalType   string  `json:"settling_signal_type,omitempty"`
	CurrentErrorClass    string  `json:"current_error_class,omitempty"`
	RetryCounter         int     `json:"retry_counter"`
	ActionIndex          int     `json:"action_index"`
	LastHeartbeatAt      *string `json:"last_heartbeat_at,omitempty"`
	AssignedSupervisorID string  `json:"assigned_supervisor_id,omitempty"`
	FrameID              string  `json:"frame_id,omitempty"`
	CreatedAt            string  `json:"created_at"`
	UpdatedAt            string  `json:"updated_at"`
}

// InvalidateNodeRequest is the POST /nodes/{id}/invalidate body.
type InvalidateNodeRequest struct {
	Reason string `json:"reason,omitempty"`
	// Frame controls the per-emit frame discipline ("" | "in" | "next").
	// Default "" → "next". See the reactive-loops + lifecycle-handlers
	// spec §5.
	Frame string `json:"frame,omitempty"`
}

// ParkedNodeEntry mirrors controlapi.ParkedNodeEntry on the wire.
//
//	@concept: parked-state
type ParkedNodeEntry struct {
	InstanceID string     `json:"instance_id"`
	NodeID     string     `json:"node_id"`
	ParkedAt   time.Time  `json:"parked_at"`
	ResumeAt   *time.Time `json:"resume_at,omitempty"`
	Reason     string     `json:"reason,omitempty"`
	ReasonNote string     `json:"reason_note,omitempty"`
}

// ParkedNodesResponse is the response body of the parked-nodes
// diagnostics endpoint (spec-named `/diagnostics/parked`; admin
// alias `/admin/diagnostics/parked-nodes`).
type ParkedNodesResponse struct {
	ParkedNodes []ParkedNodeEntry `json:"parked_nodes"`
}

// GetParkedNodes issues GET against the path the caller supplied
// (spec-named `/diagnostics/parked` per F7 with optional `?reason=`,
// or the admin alias `/admin/diagnostics/parked-nodes`).
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

// GetNode calls GET /nodes/{id}.
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

// InvalidateNode calls POST /nodes/{id}/invalidate.
func (c *Client) InvalidateNode(ctx context.Context, id string, body InvalidateNodeRequest) error {
	req, err := c.request(ctx, http.MethodPost, "/v1/nodes/"+url.PathEscape(id)+"/invalidate", body)
	if err != nil {
		return err
	}
	return c.do(req, nil)
}

// ResetNode calls POST /nodes/{id}/reset.
func (c *Client) ResetNode(ctx context.Context, id string) error {
	req, err := c.request(ctx, http.MethodPost, "/v1/nodes/"+url.PathEscape(id)+"/reset", nil)
	if err != nil {
		return err
	}
	return c.do(req, nil)
}

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

// MessageItem mirrors the JSON projection of `persistence.MessageRow`
// returned by GET /instances/{id}/messages and GET /messages/{id}.
// Payload bytes are forwarded verbatim per `@blessed-invariant 21`.
type MessageItem struct {
	ID                  string          `json:"id"`
	InstanceID          string          `json:"instance_id"`
	Kind                string          `json:"kind"`
	Sender              string          `json:"sender"`
	SenderKind          string          `json:"sender_kind"`
	Target              string          `json:"target,omitempty"`
	Payload             json.RawMessage `json:"payload,omitempty"`
	BackfillOperationID string          `json:"backfill_operation_id,omitempty"`
	ReceivedAt          time.Time       `json:"received_at"`
	DeliveredAt         *time.Time      `json:"delivered_at,omitempty"`
	FrameID             string          `json:"frame_id,omitempty"`
	Cancelled           bool            `json:"cancelled,omitempty"`
}

// ListMessagesQuery is the GET /instances/{id}/messages filter shape.
// Mirrors `persistence.MessageListFilter` plus the pagination knobs.
type ListMessagesQuery struct {
	Kind                string
	SenderKind          string
	Target              string
	BackfillOperationID string
	DeliveredAfter      string
	DeliveredBefore     string
	Cursor              string
	Limit               int
}

// ListMessagesResponse is the GET /instances/{id}/messages body shape.
type ListMessagesResponse struct {
	Messages   []MessageItem `json:"messages"`
	NextCursor string        `json:"next_cursor,omitempty"`
}

// ListInstanceMessages calls GET /instances/{id}/messages.
func (c *Client) ListInstanceMessages(ctx context.Context, instanceID string, q ListMessagesQuery) (*ListMessagesResponse, error) {
	v := url.Values{}
	if q.Kind != "" {
		v.Set("kind", q.Kind)
	}
	if q.SenderKind != "" {
		v.Set("sender_kind", q.SenderKind)
	}
	if q.Target != "" {
		v.Set("target", q.Target)
	}
	if q.BackfillOperationID != "" {
		v.Set("backfill_operation_id", q.BackfillOperationID)
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

// GetMessage calls GET /messages/{id}.
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

// CreateBackfillRequest is the POST /instances/{id}/backfills body.
type CreateBackfillRequest struct {
	TargetNode               string          `json:"target_node"`
	PartitionRequestOverride json.RawMessage `json:"partition_request_override,omitempty"`
	Reason                   string          `json:"reason,omitempty"`
}

// CreateBackfillResponse is the POST response.
type CreateBackfillResponse struct {
	MessageID           string `json:"message_id"`
	BackfillOperationID string `json:"backfill_operation_id"`
}

// BackfillItem is the projection of a backfill-class message.
type BackfillItem struct {
	OperationID string     `json:"operation_id"`
	MessageID   string     `json:"message_id"`
	TargetNode  string     `json:"target_node"`
	Reason      string     `json:"reason,omitempty"`
	ReceivedAt  time.Time  `json:"received_at"`
	DeliveredAt *time.Time `json:"delivered_at,omitempty"`
	FrameID     string     `json:"frame_id,omitempty"`
	Cancelled   bool       `json:"cancelled,omitempty"`
}

// ListBackfillsResponse is the GET /instances/{id}/backfills body.
type ListBackfillsResponse struct {
	Backfills  []BackfillItem `json:"backfills"`
	NextCursor string         `json:"next_cursor,omitempty"`
}

// BackfillPartitionRow is one element of GET /backfills/{op}/partitions.
type BackfillPartitionRow struct {
	RunID              string `json:"run_id"`
	NodeID             string `json:"node_id"`
	ChildKey           string `json:"child_key,omitempty"`
	State              string `json:"state"`
	SettlingSignalType string `json:"settling_signal_type,omitempty"`
}

// BackfillPartitionsResponse is the GET /backfills/{op}/partitions body.
type BackfillPartitionsResponse struct {
	Partitions []BackfillPartitionRow `json:"partitions"`
}

// CreateBackfill calls POST /instances/{id}/backfills.
func (c *Client) CreateBackfill(ctx context.Context, instanceID string, body CreateBackfillRequest) (*CreateBackfillResponse, error) {
	req, err := c.request(ctx, http.MethodPost, "/v1/instances/"+url.PathEscape(instanceID)+"/backfills", body)
	if err != nil {
		return nil, err
	}
	var out CreateBackfillResponse
	if err := c.do(req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListBackfills calls GET /instances/{id}/backfills.
func (c *Client) ListBackfills(ctx context.Context, instanceID string) (*ListBackfillsResponse, error) {
	req, err := c.request(ctx, http.MethodGet, "/v1/instances/"+url.PathEscape(instanceID)+"/backfills", nil)
	if err != nil {
		return nil, err
	}
	var out ListBackfillsResponse
	if err := c.do(req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetBackfill calls GET /backfills/{op_id}.
func (c *Client) GetBackfill(ctx context.Context, opID string) (*BackfillItem, error) {
	req, err := c.request(ctx, http.MethodGet, "/v1/backfills/"+url.PathEscape(opID), nil)
	if err != nil {
		return nil, err
	}
	var out BackfillItem
	if err := c.do(req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetBackfillPartitions calls GET /backfills/{op_id}/partitions.
func (c *Client) GetBackfillPartitions(ctx context.Context, opID string) (*BackfillPartitionsResponse, error) {
	req, err := c.request(ctx, http.MethodGet, "/v1/backfills/"+url.PathEscape(opID)+"/partitions", nil)
	if err != nil {
		return nil, err
	}
	var out BackfillPartitionsResponse
	if err := c.do(req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CancelBackfill calls POST /backfills/{op_id}/cancel.
func (c *Client) CancelBackfill(ctx context.Context, opID string) (map[string]any, error) {
	req, err := c.request(ctx, http.MethodPost, "/v1/backfills/"+url.PathEscape(opID)+"/cancel", nil)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := c.do(req, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// AssetItem is one element of GET /instances/{id}/assets. Mirrors the
// server-side envelope at `code:control/controlapi/assets.go::assetItem`:
// post-Stage-4 of the claim-handle state-column refactor the wire shape
// surfaces `state` + `lifetime` instead of the pre-Stage-4 `held_durable`
// bool. Both fields are always `"committed"` / `"durable"` for asset
// queries by construction (the listing predicate is
// `ListByInstanceAndState(committed, durable)`), but the explicit
// fields are surfaced for forward compatibility with operator tooling
// that wants to filter by state.
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

// ListAssetsResponse is the GET /instances/{id}/assets body.
type ListAssetsResponse struct {
	Assets []AssetItem `json:"assets"`
}

// ListAssets calls GET /instances/{id}/assets.
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

// GetAsset calls GET /instances/{id}/assets/{alias}.
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

// AssetVersionsResponse is the GET /instances/{id}/assets/{alias}/versions
// body. Live-server response is 501 until M-section wires DataProcessing;
// we accept whatever shape the response carries (defensively).
type AssetVersionsResponse struct {
	Versions []map[string]any `json:"versions,omitempty"`
	Error    string           `json:"error,omitempty"`
}

// GetAssetVersions calls GET /instances/{id}/assets/{alias}/versions.
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

// MaterializeAssetRequest is the POST .../materialize body shape.
type MaterializeAssetRequest struct {
	Payload json.RawMessage `json:"payload,omitempty"`
	Reason  string          `json:"reason,omitempty"`
}

// MaterializeAsset calls POST /instances/{id}/assets/{alias}/materialize.
func (c *Client) MaterializeAsset(ctx context.Context, instanceID, alias string, body MaterializeAssetRequest) (map[string]any, error) {
	path := "/v1/instances/" + url.PathEscape(instanceID) + "/assets/" + url.PathEscape(alias) + "/materialize"
	req, err := c.request(ctx, http.MethodPost, path, body)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := c.do(req, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// DeleteAsset calls DELETE /instances/{id}/assets/{alias}.
func (c *Client) DeleteAsset(ctx context.Context, instanceID, alias string) error {
	path := "/v1/instances/" + url.PathEscape(instanceID) + "/assets/" + url.PathEscape(alias)
	req, err := c.request(ctx, http.MethodDelete, path, nil)
	if err != nil {
		return err
	}
	return c.do(req, nil)
}

// AssetMaterializationHistoryResponse is the GET .../materialization-history body.
type AssetMaterializationHistoryResponse struct {
	MaterializationHistory []LineageRecordItem `json:"materialization_history"`
}

// GetAssetMaterializationHistory calls GET .../materialization-history.
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

// LineageRecordItem mirrors the JSON projection of persistence.LineageRow.
type LineageRecordItem struct {
	ID         string          `json:"id"`
	RecordKind string          `json:"record_kind"`
	InstanceID string          `json:"instance_id"`
	FrameID    string          `json:"frame_id"`
	ObservedAt time.Time       `json:"observed_at"`
	Record     json.RawMessage `json:"record"`
}

// LineageAncestorsResponse is GET /lineage/{run|claim}/{id}/ancestors body.
type LineageAncestorsResponse struct {
	Ancestors []LineageRecordItem `json:"ancestors"`
	Depth     int                 `json:"depth"`
}

// GetClaimAncestors calls GET /lineage/claims/{claim_handle_id}/ancestors.
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

// PruneLineageRequest is the POST /admin/lineage/prune body.
type PruneLineageRequest struct {
	Before string `json:"before"`
}

// PruneLineage calls POST /admin/lineage/prune. Returns the affected-row
// summary the server emits.
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

// RegisterTemplateOptions extends RegisterTemplateRequest with the
// runtime-only `warnings_as_errors` query parameter introduced by F9.
// Stored in this struct rather than the request body since the
// control-api reads it from the URL query, not JSON.
type RegisterTemplateOptions struct {
	WarningsAsErrors bool
}

// RegisterTemplateWithOptions calls POST /templates with the
// `?warnings_as_errors=true` query param when set. Mirrors
// RegisterTemplate; kept as a separate method to avoid breaking the
// existing single-body call shape.
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
