// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @story: single-process-all-in-one
// @story: local-orchestrator-zero-config
package scenarios

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

const persistedInstanceKey = "persisted-instance"

const persistedMessageType = "state/ping"

type deploymentSurvey struct {
	Templates  []string
	Instances  []string
	EventKinds []string
	Messages   []string
	NodeRuns   []string
}

func TestAllInOneStateVolumeCarriesTheDeploymentAcrossAContainerReplacement(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	stateDir := allInOneHostStateDir(t)

	first := harness.StartAllInOneZeroConfig(ctx, t, stateDir)
	templateID := deployScenarioTemplate(t, first.Endpoint, persistedDeploymentTemplate())
	instanceID := createScenarioInstance(t, first.Endpoint, templateID, persistedInstanceKey)
	sendPersistedMessage(t, first.Endpoint, instanceID)
	requireInstanceIdle(t, first.Endpoint, instanceID)

	before := surveyDeployment(t, first.Endpoint, instanceID)
	if len(before.EventKinds) == 0 {
		t.Fatalf("the first container ran the graph but recorded no events, so the replacement proves nothing")
	}
	if _, err := os.Stat(filepath.Join(stateDir, "state.db")); err != nil {
		entries, _ := os.ReadDir(stateDir)
		names := []string{}
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("no state.db under the mounted state volume %s: %v (found %v)", stateDir, err, names)
	}

	first.Stop(ctx)

	replacement := harness.StartAllInOneZeroConfig(ctx, t, stateDir)
	after := surveyDeployment(t, replacement.Endpoint, instanceID)
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("the replacement container over the same state volume read back a different deployment\n"+
			"before: %+v\nafter:  %+v", before, after)
	}

	unmounted := harness.StartAllInOneZeroConfig(ctx, t, "")
	if templates := listTemplateIDs(t, unmounted.Endpoint); len(templates) != 0 {
		t.Errorf("a container started with nothing mounted holds templates %v. The mount carries the "+
			"history, not the image", templates)
	}
	if instances := listInstanceKeys(t, unmounted.Endpoint); len(instances) != 0 {
		t.Errorf("a container started with nothing mounted holds instances %v. The mount carries the "+
			"history, not the image", instances)
	}
}

func allInOneHostStateDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(dir, 0o777); err != nil {
		t.Fatalf("create host state dir: %v", err)
	}
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatalf("open the host state dir to the container's nonroot user: %v", err)
	}
	return dir
}

func persistedDeploymentTemplate() map[string]any {
	return map[string]any{
		"spec": map[string]any{
			"name":     "all-in-one-state-persists",
			"version":  "1",
			"messages": []map[string]any{{"type": persistedMessageType}},
			"nodes": []map[string]any{
				{
					"type": "trigger",
					"kind": "loop_counter",
					"attributes": map[string]any{"schema": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"max":   map[string]any{"type": "integer", "default": 3},
							"count": map[string]any{"type": "integer"},
						},
					}},
				},
				{
					"type": "listener",
					"kind": "attribute_passthrough",
					"subscribes": []map[string]any{{
						"node":                   persistedMessageType,
						"type":                   "terminal/success",
						"force_upstream_refresh": false,
					}},
					"attributes": map[string]any{"schema": map[string]any{
						"type":       "object",
						"properties": map[string]any{"seen": map[string]any{"type": "integer", "default": 1}},
					}},
				},
			},
		},
	}
}

func sendPersistedMessage(t *testing.T, ep harness.RimskyEndpoint, instanceID string) {
	t.Helper()
	status, raw := ep.PostJSONWithHeaders(t, "/v1/instances/"+instanceID+"/messages",
		map[string]any{"type": persistedMessageType},
		map[string]string{"Idempotency-Key": "state-persists-" + instanceID})
	if status != http.StatusAccepted && status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("POST message %s: %d %s", persistedMessageType, status, string(raw))
	}
}

func surveyDeployment(t *testing.T, ep harness.RimskyEndpoint, instanceID string) deploymentSurvey {
	t.Helper()
	return deploymentSurvey{
		Templates:  listTemplateIDs(t, ep),
		Instances:  listInstanceKeys(t, ep),
		EventKinds: instanceEventKinds(t, ep, instanceID),
		Messages:   instanceMessageTypes(t, ep, instanceID),
		NodeRuns:   instanceNodeRunSummaries(t, ep, instanceID),
	}
}

func listTemplateIDs(t *testing.T, ep harness.RimskyEndpoint) []string {
	t.Helper()
	var resp struct {
		Templates []struct {
			ID string `json:"id"`
		} `json:"templates"`
	}
	readSurveyJSON(t, ep, "/v1/templates", &resp)
	ids := []string{}
	for _, tpl := range resp.Templates {
		ids = append(ids, tpl.ID)
	}
	sort.Strings(ids)
	return ids
}

func listInstanceKeys(t *testing.T, ep harness.RimskyEndpoint) []string {
	t.Helper()
	var resp struct {
		Instances []struct {
			ID          string  `json:"id"`
			InstanceKey *string `json:"instance_key"`
		} `json:"instances"`
	}
	readSurveyJSON(t, ep, "/v1/instances", &resp)
	pairs := []string{}
	for _, inst := range resp.Instances {
		key := ""
		if inst.InstanceKey != nil {
			key = *inst.InstanceKey
		}
		pairs = append(pairs, inst.ID+"|"+key)
	}
	sort.Strings(pairs)
	return pairs
}

func instanceEventKinds(t *testing.T, ep harness.RimskyEndpoint, instanceID string) []string {
	t.Helper()
	var resp struct {
		Events []struct {
			Kind string `json:"kind"`
		} `json:"events"`
	}
	readSurveyJSON(t, ep, "/v1/events?limit=1000&instance_id="+instanceID, &resp)
	kinds := []string{}
	for _, e := range resp.Events {
		kinds = append(kinds, e.Kind)
	}
	sort.Strings(kinds)
	return kinds
}

func instanceMessageTypes(t *testing.T, ep harness.RimskyEndpoint, instanceID string) []string {
	t.Helper()
	var resp struct {
		Messages []struct {
			Type string `json:"type"`
		} `json:"messages"`
	}
	readSurveyJSON(t, ep, "/v1/instances/"+instanceID+"/messages", &resp)
	types := []string{}
	for _, m := range resp.Messages {
		types = append(types, m.Type)
	}
	sort.Strings(types)
	return types
}

func instanceNodeRunSummaries(t *testing.T, ep harness.RimskyEndpoint, instanceID string) []string {
	t.Helper()
	var resp struct {
		Nodes []struct {
			NodeType   string          `json:"node_type"`
			RunSummary json.RawMessage `json:"run_summary"`
		} `json:"nodes"`
	}
	readSurveyJSON(t, ep, "/v1/instances/"+instanceID+"/nodes", &resp)
	summaries := []string{}
	for _, n := range resp.Nodes {
		summaries = append(summaries, n.NodeType+"|"+canonicalJSON(t, n.RunSummary))
	}
	sort.Strings(summaries)
	return summaries
}

func readSurveyJSON(t *testing.T, ep harness.RimskyEndpoint, path string, into any) {
	t.Helper()
	status, raw := ep.GetJSON(t, path, "")
	if status != http.StatusOK {
		t.Fatalf("GET %s: %d %s", path, status, string(raw))
	}
	if err := json.Unmarshal(raw, into); err != nil {
		t.Fatalf("decode %s: %v: %s", path, err, string(raw))
	}
}

func canonicalJSON(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	if len(raw) == 0 {
		return "null"
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("decode run_summary %s: %v", string(raw), err)
	}
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("re-encode run_summary %s: %v", string(raw), err)
	}
	return string(out)
}
