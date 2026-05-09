// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// tools.go registers the tool catalog wrapping the rimsky control-API.
// Each tool is a thin pass-through over the matching control-API
// endpoint; input is parsed into args, rendered into the appropriate
// HTTP request, and the response is forwarded as the MCP tool result.
//
// Per plan K3.

package controlapimcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// registerCoreTools attaches the standard tool set documented in plan K3.
// Each tool's input schema is kept minimal — operators can reference
// the rimsky control-API docs for the full body shape.
func (s *Server) registerCoreTools() {
	jsObj := []byte(`{"type":"object","additionalProperties":true}`)

	// Templates.
	s.RegisterTool(
		Tool{Name: "template_list", Description: "List registered templates.", InputSchema: jsObj},
		func(ctx context.Context, _ json.RawMessage) (any, error) {
			return s.callJSON(ctx, http.MethodGet, "/templates", nil)
		},
	)
	s.RegisterTool(
		Tool{Name: "template_get", Description: "Fetch a template by hash.", InputSchema: []byte(`{"type":"object","properties":{"hash":{"type":"string"}},"required":["hash"]}`)},
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var a struct {
				Hash string `json:"hash"`
			}
			if err := json.Unmarshal(args, &a); err != nil {
				return nil, err
			}
			return s.callJSON(ctx, http.MethodGet, "/templates/"+url.PathEscape(a.Hash), nil)
		},
	)
	s.RegisterTool(
		Tool{Name: "template_register", Description: "Register a new template.", InputSchema: jsObj},
		func(ctx context.Context, args json.RawMessage) (any, error) {
			return s.callJSON(ctx, http.MethodPost, "/templates", args)
		},
	)
	s.RegisterTool(
		Tool{Name: "template_deploy", Description: "Mark a template deployed.", InputSchema: []byte(`{"type":"object","properties":{"hash":{"type":"string"}},"required":["hash"]}`)},
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var a struct {
				Hash string `json:"hash"`
			}
			if err := json.Unmarshal(args, &a); err != nil {
				return nil, err
			}
			return s.callJSON(ctx, http.MethodPost, "/templates/"+url.PathEscape(a.Hash)+"/deploy", nil)
		},
	)
	s.RegisterTool(
		Tool{Name: "template_undeploy", Description: "Mark a template undeployed.", InputSchema: []byte(`{"type":"object","properties":{"hash":{"type":"string"}},"required":["hash"]}`)},
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var a struct {
				Hash string `json:"hash"`
			}
			if err := json.Unmarshal(args, &a); err != nil {
				return nil, err
			}
			return s.callJSON(ctx, http.MethodPost, "/templates/"+url.PathEscape(a.Hash)+"/undeploy", nil)
		},
	)
	s.RegisterTool(
		Tool{Name: "template_deregister", Description: "Delete a template by hash.", InputSchema: []byte(`{"type":"object","properties":{"hash":{"type":"string"}},"required":["hash"]}`)},
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var a struct {
				Hash string `json:"hash"`
			}
			if err := json.Unmarshal(args, &a); err != nil {
				return nil, err
			}
			return s.callJSON(ctx, http.MethodDelete, "/templates/"+url.PathEscape(a.Hash), nil)
		},
	)

	// Tags.
	s.RegisterTool(
		Tool{Name: "tag_list", Description: "List tags.", InputSchema: jsObj},
		func(ctx context.Context, _ json.RawMessage) (any, error) {
			return s.callJSON(ctx, http.MethodGet, "/tags", nil)
		},
	)
	s.RegisterTool(
		Tool{Name: "tag_set", Description: "Set a tag to point at a template hash.", InputSchema: []byte(`{"type":"object","properties":{"name":{"type":"string"},"hash":{"type":"string"}},"required":["name","hash"]}`)},
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var a struct {
				Name string `json:"name"`
				Hash string `json:"hash"`
			}
			if err := json.Unmarshal(args, &a); err != nil {
				return nil, err
			}
			body, _ := json.Marshal(map[string]string{"hash": a.Hash})
			return s.callJSON(ctx, http.MethodPut, "/tags/"+url.PathEscape(a.Name), body)
		},
	)
	s.RegisterTool(
		Tool{Name: "tag_delete", Description: "Delete a tag.", InputSchema: []byte(`{"type":"object","properties":{"name":{"type":"string"}},"required":["name"]}`)},
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var a struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(args, &a); err != nil {
				return nil, err
			}
			return s.callJSON(ctx, http.MethodDelete, "/tags/"+url.PathEscape(a.Name), nil)
		},
	)

	// Instances.
	s.RegisterTool(
		Tool{Name: "instance_list", Description: "List instances.", InputSchema: jsObj},
		func(ctx context.Context, _ json.RawMessage) (any, error) {
			return s.callJSON(ctx, http.MethodGet, "/instances", nil)
		},
	)
	s.RegisterTool(
		Tool{Name: "instance_get", Description: "Get instance by id.", InputSchema: []byte(`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"]}`)},
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var a struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(args, &a); err != nil {
				return nil, err
			}
			return s.callJSON(ctx, http.MethodGet, "/instances/"+url.PathEscape(a.ID), nil)
		},
	)
	s.RegisterTool(
		Tool{
			Name:        "instance_create",
			Description: "Create a new instance, optionally with userdata_overrides.",
			InputSchema: []byte(`{"type":"object","properties":{"template":{"type":"string"},"instance_key":{"type":"string"},"params":{"type":"object"},"userdata_overrides":{"type":"object"}},"required":["template"]}`),
		},
		func(ctx context.Context, args json.RawMessage) (any, error) {
			return s.callJSON(ctx, http.MethodPost, "/instances", args)
		},
	)
	s.RegisterTool(
		Tool{Name: "instance_terminate", Description: "Terminate an instance.", InputSchema: []byte(`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"]}`)},
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var a struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(args, &a); err != nil {
				return nil, err
			}
			return s.callJSON(ctx, http.MethodPost, "/instances/"+url.PathEscape(a.ID)+"/terminate", nil)
		},
	)

	// Nodes.
	s.RegisterTool(
		Tool{Name: "node_get", Description: "Get a node by (instance, node_id).", InputSchema: []byte(`{"type":"object","properties":{"instance":{"type":"string"},"node_id":{"type":"string"}},"required":["instance","node_id"]}`)},
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var a struct {
				Instance string `json:"instance"`
				NodeID   string `json:"node_id"`
			}
			if err := json.Unmarshal(args, &a); err != nil {
				return nil, err
			}
			return s.callJSON(ctx, http.MethodGet, "/instances/"+url.PathEscape(a.Instance)+"/nodes/"+url.PathEscape(a.NodeID), nil)
		},
	)
	s.RegisterTool(
		Tool{Name: "node_invalidate", Description: "Invalidate a node (resumes if parked, fresh-invalidates otherwise).", InputSchema: []byte(`{"type":"object","properties":{"instance":{"type":"string"},"node_id":{"type":"string"}},"required":["instance","node_id"]}`)},
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var a struct {
				Instance string `json:"instance"`
				NodeID   string `json:"node_id"`
			}
			if err := json.Unmarshal(args, &a); err != nil {
				return nil, err
			}
			return s.callJSON(ctx, http.MethodPost,
				"/admin/instances/"+url.PathEscape(a.Instance)+"/nodes/"+url.PathEscape(a.NodeID)+"/invalidate", nil)
		},
	)
	s.RegisterTool(
		Tool{Name: "force_fire_scheduled", Description: "Force-fire a scheduled node.", InputSchema: []byte(`{"type":"object","properties":{"node_id":{"type":"string"}},"required":["node_id"]}`)},
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var a struct {
				NodeID string `json:"node_id"`
			}
			if err := json.Unmarshal(args, &a); err != nil {
				return nil, err
			}
			return s.callJSON(ctx, http.MethodPost, "/admin/scheduled-nodes/"+url.PathEscape(a.NodeID)+"/force-fire", nil)
		},
	)

	// Diagnostics.
	s.RegisterTool(
		Tool{Name: "held_frames_list", Description: "List frames with parked nodes.", InputSchema: jsObj},
		func(ctx context.Context, _ json.RawMessage) (any, error) {
			return s.callJSON(ctx, http.MethodGet, "/admin/diagnostics/held-frames", nil)
		},
	)
	s.RegisterTool(
		Tool{Name: "parked_nodes_list", Description: "List currently parked nodes; optional reason filter.", InputSchema: []byte(`{"type":"object","properties":{"reason":{"type":"string"}}}`)},
		func(ctx context.Context, args json.RawMessage) (any, error) {
			var a struct {
				Reason string `json:"reason"`
			}
			_ = json.Unmarshal(args, &a)
			path := "/admin/diagnostics/parked-nodes"
			if a.Reason != "" {
				path += "?reason=" + url.QueryEscape(a.Reason)
			}
			return s.callJSON(ctx, http.MethodGet, path, nil)
		},
	)
}

// callJSON does the HTTP round-trip to the configured control-API base
// URL. body may be nil, []byte, or json.RawMessage. Returns the parsed
// JSON body when the response Content-Type is JSON; otherwise returns
// the raw bytes as a string.
func (s *Server) callJSON(ctx context.Context, method, path string, body any) (any, error) {
	url := s.cfg.ControlAPIURL + path
	var reader io.Reader
	switch b := body.(type) {
	case nil:
	case []byte:
		if len(b) > 0 {
			reader = bytes.NewReader(b)
		}
	case json.RawMessage:
		if len(b) > 0 {
			reader = bytes.NewReader(b)
		}
	default:
		bs, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal body: %w", err)
		}
		reader = bytes.NewReader(bs)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return nil, err
	}
	if reader != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if s.cfg.ControlAPIToken != "" {
		req.Header.Set("Authorization", "Bearer "+s.cfg.ControlAPIToken)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("control-api %s %s: %d %s", method, path, resp.StatusCode, string(respBody))
	}
	if len(respBody) == 0 {
		return map[string]any{"status": resp.StatusCode}, nil
	}
	var parsed any
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return string(respBody), nil
	}
	return parsed, nil
}
