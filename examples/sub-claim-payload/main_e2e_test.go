// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// @story: sub-claim-payload-substitution
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

func TestSubClaimPayloadSubstitutionE2E(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	netName := harness.SharedNetworkName(ctx, t)

	fs := harness.StartFilesystemClaimProducer(ctx, t, netName, "producer-fs",
		harness.FilesystemClaimProducerSpec{
			PickPolicies: map[string]harness.FilesystemPickPolicy{
				"@seed-queue": {
					Root:                     "queue/available",
					OnCommit:                 "recycle",
					OnGiveUp:                 "recycle",
					VisibilityTimeoutSeconds: 60,
					SyncStrategy:             "on_open",
				},
			},
			SeedFolders: [][]string{
				{"queue", "available", "example-job"},
			},
		})

	ep := harness.BringUpRimsky(ctx, t,
		harness.WithExistingNetwork(netName),
		harness.WithClaimProducer("claim-producer-filesystem", fs.InternalEndpoint),
	)

	tid := deploySubClaimPayloadTemplate(t, ep)
	iid := createSubClaimPayloadInstance(t, ep, tid, "ck-sub-claim-payload")
	postFanoutSeed(t, ep, iid)

	waitForProcessedValues(t, ep, iid, map[float64]struct{}{1: {}, 2: {}, 3: {}})
}

func deploySubClaimPayloadTemplate(t *testing.T, ep harness.RimskyEndpoint) string {
	t.Helper()
	body := map[string]any{
		"spec": map[string]any{
			"name":    "sub-claim-payload",
			"version": "1",
			"messages": []map[string]any{
				{
					"type": "fanout/seed",
					"body_schema": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"items": map[string]any{"type": "array"},
						},
						"required": []string{"items"},
					},
				},
			},
			"nodes": []map[string]any{
				{
					"type": "triage",
					"kind": "attribute_passthrough",
					"claim_producers": []map[string]any{
						{"name": "claim-producer-filesystem", "selector": "@seed-queue", "intent": "rw", "alias": "fs_queue"},
					},
					"error_types": map[string]any{
						"acquire/unavailable": map[string]any{"action": "retry"},
					},
					"subscribes": []map[string]any{
						{"node": "fanout/seed", "type": "terminal/success", "force_upstream_refresh": false},
					},
					"fan_out": map[string]any{
						"claim":             "fs_queue",
						"partition_request": `{"list":{{messages.fanout/seed.items}}}`,
						"error_policy":      map[string]any{"kind": "best_effort"},
					},
					"attributes": map[string]any{
						"schema": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"partition_key":   map[string]any{"type": "string", "source": "{{child.partition_key}}"},
								"processed_value": map[string]any{"type": "number", "source": "{{claim.fs_queue.payload.v}}"},
							},
							"required": []string{"partition_key", "processed_value"},
						},
					},
				},
			},
		},
	}
	status, raw := ep.PostJSON(t, "/v1/templates", body)
	if status != http.StatusCreated {
		t.Fatalf("POST /v1/templates: %d %s", status, string(raw))
	}
	var resp struct {
		TemplateID string `json:"template_id"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode template response: %v: %s", err, string(raw))
	}
	if resp.TemplateID == "" {
		t.Fatalf("template_id empty: %s", string(raw))
	}
	deployStatus, deployRaw := ep.PostJSON(t, "/v1/templates/"+resp.TemplateID+"/deploy", map[string]any{})
	if deployStatus != http.StatusOK {
		t.Fatalf("POST /v1/templates/%s/deploy: %d %s", resp.TemplateID, deployStatus, string(deployRaw))
	}
	return resp.TemplateID
}

// @decision: test-harness-create-instance-wakes-roots-after-create
func createSubClaimPayloadInstance(t *testing.T, ep harness.RimskyEndpoint, templateID, instanceKey string) string {
	t.Helper()
	status, raw := ep.PostJSON(t, "/v1/instances", map[string]any{
		"template":     templateID,
		"instance_key": instanceKey,
		"params":       map[string]any{},
		"target_agent": "example-default-agent",
	})
	if status != http.StatusCreated {
		t.Fatalf("POST /v1/instances: %d %s", status, string(raw))
	}
	var resp struct {
		InstanceID string `json:"instance_id"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode instance response: %v: %s", err, string(raw))
	}
	if resp.InstanceID == "" {
		t.Fatalf("instance_id empty: %s", string(raw))
	}
	ep.EmptyWakeAfterCreate(t, resp.InstanceID, "sub-claim-payload", instanceKey)
	return resp.InstanceID
}

func postFanoutSeed(t *testing.T, ep harness.RimskyEndpoint, instanceID string) {
	t.Helper()
	body := map[string]any{
		"type": "fanout/seed",
		"payload": map[string]any{
			"items": []map[string]any{
				{"key": "a", "payload": map[string]any{"v": 1}},
				{"key": "b", "payload": map[string]any{"v": 2}},
				{"key": "c", "payload": map[string]any{"v": 3}},
			},
		},
	}
	status, raw := ep.PostJSONWithHeaders(t, "/v1/instances/"+instanceID+"/messages", body,
		map[string]string{"Idempotency-Key": "scp-" + instanceID})
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("POST /v1/instances/%s/messages: %d %s", instanceID, status, string(raw))
	}
}

func waitForProcessedValues(t *testing.T, ep harness.RimskyEndpoint, instanceID string, want map[float64]struct{}) {
	t.Helper()
	for {
		status, raw := ep.GetJSON(t, "/v1/events?instance_id="+instanceID+"&limit=500", "")
		if status == http.StatusOK {
			var resp struct {
				Events []struct {
					Payload struct {
						AttributesDelta map[string]any `json:"attributes_delta"`
					} `json:"payload"`
				} `json:"events"`
			}
			if err := json.Unmarshal(raw, &resp); err == nil {
				seen := make(map[float64]bool, len(want))
				for _, e := range resp.Events {
					if v, ok := e.Payload.AttributesDelta["processed_value"].(float64); ok {
						seen[v] = true
					}
				}
				allSeen := true
				for v := range want {
					if !seen[v] {
						allSeen = false
						break
					}
				}
				if allSeen {
					return
				}
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
}
