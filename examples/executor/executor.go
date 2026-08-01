// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// @concept: terminal-tag
const DeclaredTagName = "work_started"

const DeclaredErrorClass = "example/forbidden"

const ExpectedAttributesSchema = `{
  "type": "object",
  "properties": {
    "mode": {
      "type": "string",
      "enum": ["ok", "emit_event", "raise_error", "async_callback"],
      "default": "ok"
    },
    "count": {
      "type": "integer",
      "minimum": 0,
      "default": 0
    },
    "async_ack_id": {
      "type": "string",
      "default": ""
    }
  }
}`

type Executor struct {
	genv1.UnimplementedExecutorServer
	genv1.UnimplementedExecutorObservabilityServer
}

func (e *Executor) Execute(_ context.Context, req *genv1.ExecuteRequest) (*genv1.Outcome, error) {
	mode := stringAttr(req, "mode")

	switch mode {
	case "raise_error":
		// @concept: error-policy
		return &genv1.Outcome{Outcome: &genv1.Outcome_Error{Error: &genv1.Error{
			ErrorClass: DeclaredErrorClass,
		}}}, nil

	case "emit_event":
		// @concept: terminal-tag
		summary := "minimal example: tagged success"
		if count := intAttr(req, "count"); count > 0 {
			summary = fmt.Sprintf("minimal example: tagged success (count=%d)", count)
		}
		return &genv1.Outcome{Outcome: &genv1.Outcome_Success{Success: &genv1.Success{
			Changed:       false,
			ChangeSummary: summary,
			Tags:          []string{DeclaredTagName},
		}}}, nil

	case "async_callback":
		// @decision: async-callback-persistent-registry
		ackID := stringAttr(req, "async_ack_id")
		if ackID == "" {
			return &genv1.Outcome{Outcome: &genv1.Outcome_Error{Error: &genv1.Error{
				ErrorClass: DeclaredErrorClass,
			}}}, nil
		}
		if req.GetCallbackUrl() != "" {
			go postAsyncCallback(req.GetCallbackUrl(), ackID, req.GetCancelToken())
		}
		return &genv1.Outcome{Outcome: &genv1.Outcome_AwaitAsync{AwaitAsync: &genv1.AwaitAsyncCallback{
			AsyncAckId:           ackID,
			ExpectedCompletionMs: 0,
		}}}, nil
	}

	return &genv1.Outcome{Outcome: &genv1.Outcome_Success{Success: &genv1.Success{
		Changed:       false,
		ChangeSummary: "minimal example: success",
	}}}, nil
}

func postAsyncCallback(callbackURL, ackID, cancelToken string) {
	delay := 100 * time.Millisecond
	if raw := os.Getenv("EXAMPLE_EXECUTOR_ASYNC_CALLBACK_DELAY_MS"); raw != "" {
		if ms, err := time.ParseDuration(raw + "ms"); err == nil {
			delay = ms
		}
	}
	time.Sleep(delay)

	url := strings.TrimRight(callbackURL, "/") + "/v1/callback/" + ackID
	if override := os.Getenv("EXAMPLE_EXECUTOR_CALLBACK_HOST_OVERRIDE"); override != "" {
		url = strings.TrimRight(override, "/") + "/v1/callback/" + ackID
	}

	body := map[string]any{
		"success": map[string]any{
			"changed":        false,
			"change_summary": "minimal example: async-callback success",
		},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "example-executor: marshal async callback body: %v\n", err)
		return
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		fmt.Fprintf(os.Stderr, "example-executor: build async callback POST %s: %v\n", url, err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if cancelToken != "" {
		req.Header.Set("Authorization", "Bearer "+cancelToken)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "example-executor: async callback POST %s: %v\n", url, err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "example-executor: async callback POST %s returned %d\n", url, resp.StatusCode)
	}
}

func stringAttr(req *genv1.ExecuteRequest, name string) string {
	attrs := req.GetAttributes()
	if attrs == nil {
		return ""
	}
	fields := attrs.GetFields()
	v, ok := fields[name]
	if !ok || v == nil {
		return ""
	}
	return v.GetStringValue()
}

func intAttr(req *genv1.ExecuteRequest, name string) int {
	attrs := req.GetAttributes()
	if attrs == nil {
		return 0
	}
	fields := attrs.GetFields()
	v, ok := fields[name]
	if !ok || v == nil {
		return 0
	}
	return int(v.GetNumberValue())
}

func (e *Executor) Capabilities(_ context.Context, _ *genv1.ExecutorCapabilitiesRequest) (*genv1.ObservabilityCapabilities, error) {
	return &genv1.ObservabilityCapabilities{
		ExpectedAttributesSchema: []byte(ExpectedAttributesSchema),
		DeclaredTags:             []string{DeclaredTagName},
		DeclaredErrorClasses:     []string{DeclaredErrorClass},
	}, nil
}
