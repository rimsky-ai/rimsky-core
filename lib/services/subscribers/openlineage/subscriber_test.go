// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

func TestSubscriber_EndToEnd_PollsAndEmits(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	netName := harness.SharedNetworkName(ctx, t)
	fs := harness.StartFilesystemClaimProducer(ctx, t, netName, "claim-producer-filesystem",
		harness.FilesystemClaimProducerSpec{
			PickPolicies: map[string]harness.FilesystemPickPolicy{
				"@docs-ring": {
					Root:                     "docs",
					OnCommit:                 "recycle",
					OnGiveUp:                 "recycle",
					VisibilityTimeoutSeconds: 60,
					SyncStrategy:             "on_open",
				},
			},
			SeedFolders: [][]string{{"docs", "alpha"}, {"docs", "beta"}},
		})
	harness.StartExecutorStubOnNetwork(ctx, t, netName)
	ep := harness.BringUpRimsky(ctx, t,
		harness.WithExistingNetwork(netName),
		harness.WithClaimProducer("docs", fs.InternalEndpoint),
		harness.WithExecutor("stub", "executor-stub:9300"),
	)

	tplBody := map[string]any{
		"spec": map[string]any{
			"name":    "openlineage-e2e",
			"version": "1",
			"nodes": []map[string]any{
				{
					"type":     "acquire-and-execute",
					"executor": "stub",
					"claim_producers": []map[string]any{
						{"name": "docs", "selector": "@docs-ring", "intent": "rw"},
					},
				},
			},
		},
	}
	templateID := postTemplate(t, ep, tplBody)
	instanceID := postInstance(t, ep, templateID, "openlineage-e2e-1")

	waitNodeTerminal(t, ep, instanceID, "acquire-and-execute", 60*time.Second)
	waitForLineageRows(t, ep, 1, 30*time.Second)

	var (
		mu       sync.Mutex
		received []Event
	)
	marquez := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var ev Event
		if err := json.Unmarshal(body, &ev); err == nil {
			mu.Lock()
			received = append(received, ev)
			mu.Unlock()
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer marquez.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	sub, err := New(ctx, Config{
		RimskyDSN:    ep.HostDSN,
		StateDSN:     ep.HostDSN,
		BackendURL:   marquez.URL,
		Namespace:    "ns-e2e",
		PollInterval: 50 * time.Millisecond,
		BatchSize:    100,
	}, logger)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer sub.Close()

	if err := sub.tick(ctx); err != nil {
		t.Fatalf("tick: %v", err)
	}
	mu.Lock()
	got := len(received)
	mu.Unlock()
	if got < 1 {
		t.Fatalf("received %d events; want >= 1", got)
	}

	if err := sub.tick(ctx); err != nil {
		t.Fatalf("tick (idempotent): %v", err)
	}
	mu.Lock()
	gotAfter := len(received)
	mu.Unlock()
	if gotAfter != got {
		t.Errorf("second tick added rows; received %d (want stays at %d)", gotAfter, got)
	}

	mu.Lock()
	defer mu.Unlock()
	for i, ev := range received {
		if ev.EventType == "" {
			t.Errorf("event[%d] missing EventType", i)
		}
		if ev.EventTime == "" {
			t.Errorf("event[%d] missing EventTime", i)
		}
		if ev.ProducerURI == "" {
			t.Errorf("event[%d] missing ProducerURI", i)
		}
		if ev.Run.RunID == "" {
			t.Errorf("event[%d] missing Run.RunID", i)
		}
		if ev.Job.Namespace != "ns-e2e" {
			t.Errorf("event[%d] Job.Namespace = %q, want ns-e2e", i, ev.Job.Namespace)
		}
	}
}

func TestSubscriber_EmitFailureHaltsBatch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	netName := harness.SharedNetworkName(ctx, t)
	fs := harness.StartFilesystemClaimProducer(ctx, t, netName, "claim-producer-filesystem",
		harness.FilesystemClaimProducerSpec{
			PickPolicies: map[string]harness.FilesystemPickPolicy{
				"@docs-ring": {
					Root:                     "docs",
					OnCommit:                 "recycle",
					OnGiveUp:                 "recycle",
					VisibilityTimeoutSeconds: 60,
					SyncStrategy:             "on_open",
				},
			},
			SeedFolders: [][]string{{"docs", "alpha"}},
		})
	harness.StartExecutorStubOnNetwork(ctx, t, netName)
	ep := harness.BringUpRimsky(ctx, t,
		harness.WithExistingNetwork(netName),
		harness.WithClaimProducer("docs", fs.InternalEndpoint),
		harness.WithExecutor("stub", "executor-stub:9300"),
	)

	templateID := postTemplate(t, ep, map[string]any{
		"spec": map[string]any{
			"name":    "openlineage-fail",
			"version": "1",
			"nodes": []map[string]any{
				{
					"type":     "acquire-and-execute",
					"executor": "stub",
					"claim_producers": []map[string]any{
						{"name": "docs", "selector": "@docs-ring", "intent": "rw"},
					},
				},
			},
		},
	})
	instanceID := postInstance(t, ep, templateID, "openlineage-fail-1")
	waitNodeTerminal(t, ep, instanceID, "acquire-and-execute", 60*time.Second)

	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer failing.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	sub, err := New(ctx, Config{
		RimskyDSN:    ep.HostDSN,
		StateDSN:     ep.HostDSN,
		BackendURL:   failing.URL,
		Namespace:    "ns-fail",
		PollInterval: 50 * time.Millisecond,
		BatchSize:    100,
	}, logger)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer sub.Close()

	_ = sub.tick(ctx)
	if !sub.cursorAt.Equal(time.Unix(0, 0)) {
		t.Errorf("cursor advanced despite emit failure: %v", sub.cursorAt)
	}
}

func postTemplate(t *testing.T, ep harness.RimskyEndpoint, body map[string]any) string {
	t.Helper()
	status, raw := ep.PostJSON(t, "/v1/templates", body)
	if status != http.StatusCreated {
		t.Fatalf("POST /templates: %d %s", status, string(raw))
	}
	var resp struct {
		TemplateID string `json:"template_id"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode: %v: %s", err, string(raw))
	}
	deployStatus, deployRaw := ep.PostJSON(t,
		"/v1/templates/"+resp.TemplateID+"/deploy", map[string]any{})
	if deployStatus != http.StatusOK {
		t.Fatalf("POST /templates/%s/deploy: %d %s", resp.TemplateID, deployStatus, string(deployRaw))
	}
	return resp.TemplateID
}

// @decision: test-harness-create-instance-wakes-roots-after-create
func postInstance(t *testing.T, ep harness.RimskyEndpoint, templateID, key string) string {
	t.Helper()
	status, raw := ep.PostJSON(t, "/v1/instances", map[string]any{
		"template":     templateID,
		"instance_key": key,
		"params":       map[string]any{},
		"target_agent": "scenario-default-agent",
	})
	if status != http.StatusCreated {
		t.Fatalf("POST /instances: %d %s", status, string(raw))
	}
	var resp struct {
		InstanceID string `json:"instance_id"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode: %v: %s", err, string(raw))
	}
	if resp.InstanceID == "" {
		t.Fatalf("instance_id empty: %s", string(raw))
	}
	ep.EmptyWakeAfterCreate(t, resp.InstanceID, "openlineage-subscriber", key)
	return resp.InstanceID
}

func waitForLineageRows(t *testing.T, ep harness.RimskyEndpoint, want int, deadline time.Duration) {
	t.Helper()
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, ep.HostDSN)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	defer pool.Close()
	end := time.Now().Add(deadline)
	var n int
	for time.Now().Before(end) {
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM rimsky_lineage`).Scan(&n); err != nil {
			t.Fatalf("count rimsky_lineage: %v", err)
		}
		if n >= want {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("rimsky_lineage: want >= %d rows within %v, got %d", want, deadline, n)
}

func waitNodeTerminal(t *testing.T, ep harness.RimskyEndpoint, instanceID, nodeType string, deadline time.Duration) {
	t.Helper()
	end := time.Now().Add(deadline)
	var lastState string
	for time.Now().Before(end) {
		status, raw := ep.GetJSON(t,
			"/v1/observability/nodes/"+instanceID+"/"+nodeType, "")
		if status == http.StatusOK {
			var resp struct {
				RunSummary struct {
					ActiveCount  int `json:"active_count"`
					PendingCount int `json:"pending_count"`
					FreshCount   int `json:"fresh_count"`
					FailedCount  int `json:"failed_count"`
				} `json:"run_summary"`
			}
			if err := json.Unmarshal(raw, &resp); err == nil {
				s := resp.RunSummary
				switch {
				case s.FailedCount > 0:
					lastState = "failed"
				case s.ActiveCount > 0 || s.PendingCount > 0:
					lastState = "in-flight"
				case s.FreshCount > 0:
					lastState = "fresh"
				default:
					lastState = "idle"
				}
				if lastState == "fresh" || lastState == "failed" {
					return
				}
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("node %q on instance %s did not reach terminal within %v; last state=%q",
		nodeType, instanceID, deadline, lastState)
}

func TestLeafRunRecord_WireContract(t *testing.T) {
	t.Parallel()
	const writerJSON = `{
	  "run_id": "11111111-1111-1111-1111-111111111111",
	  "node_id": "22222222-2222-2222-2222-222222222222",
	  "frame_id": "33333333-3333-3333-3333-333333333333",
	  "child_key": "partition-0",
	  "node_alias": "stage",
	  "parent_run_id": "44444444-4444-4444-4444-444444444444",
	  "frame_trigger_kind": "invalidate",
	  "trigger_message_id": "55555555-5555-5555-5555-555555555555",
	  "held_claims": [
	    {"claim_handle_id":"c1","role":"acquire","producer_name":"p","claim_scope_data_hash":"s"}
	  ],
	  "executor_name": "claude-agent",
	  "template_hash": "sha256-aaa",
	  "template_node_alias": "stage",
	  "params_snapshot_hash": "sha256-bbb",
	  "attributes_hash": "sha256-ccc",
	  "claim_scope_data_hash": "sha256-ddd",
	  "state": "fresh",
	  "settling_signal_type": "terminal/success",
	  "changed": true,
	  "terminal_kind": "complete",
	  "error_class": "",
	  "substitution_refs": [
	    {"source_kind":"attribute","source_node_alias":"alias-a","source_version_or_id":"path-1"},
	    {"source_kind":"run","source_node_alias":"alias-a","source_version_or_id":"66666666-6666-6666-6666-666666666666"}
	  ],
	  "extra": {"k": "v"}
	}`
	var rec LeafRunRecord
	if err := json.Unmarshal([]byte(writerJSON), &rec); err != nil {
		t.Fatalf("decode writer JSON: %v", err)
	}
	if rec.RunID != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("RunID = %q", rec.RunID)
	}
	if rec.NodeID != "22222222-2222-2222-2222-222222222222" {
		t.Errorf("NodeID = %q", rec.NodeID)
	}
	if rec.FrameID != "33333333-3333-3333-3333-333333333333" {
		t.Errorf("FrameID = %q", rec.FrameID)
	}
	if rec.ScopeDataHash != "sha256-ddd" {
		t.Errorf("ScopeDataHash = %q", rec.ScopeDataHash)
	}
	if rec.State != "fresh" {
		t.Errorf("State = %q", rec.State)
	}
	if rec.SettlingSignalType != "terminal/success" {
		t.Errorf("SettlingSignalType = %q", rec.SettlingSignalType)
	}
	if rec.ErrorClass != "" {
		t.Errorf("ErrorClass = %q want empty", rec.ErrorClass)
	}
	if len(rec.SubstitutionRefs) != 2 {
		t.Fatalf("SubstitutionRefs length = %d want 2", len(rec.SubstitutionRefs))
	}
	if rec.SubstitutionRefs[0].SourceKind != "attribute" || rec.SubstitutionRefs[0].SourceNodeAlias != "alias-a" || rec.SubstitutionRefs[0].SourceVersionOrID != "path-1" {
		t.Errorf("SubstitutionRefs[0] = %+v", rec.SubstitutionRefs[0])
	}
	if rec.SubstitutionRefs[1].SourceKind != "run" || rec.SubstitutionRefs[1].SourceVersionOrID != "66666666-6666-6666-6666-666666666666" {
		t.Errorf("SubstitutionRefs[1] = %+v", rec.SubstitutionRefs[1])
	}
	if rec.Extra["k"] != "v" {
		t.Errorf("Extra = %+v", rec.Extra)
	}
}

func TestClaimTerminalRecord_WireContract(t *testing.T) {
	t.Parallel()
	const writerJSON = `{
	  "claim_handle_id": "11111111-1111-1111-1111-111111111111",
	  "run_id": "22222222-2222-2222-2222-222222222222",
	  "node_id": "33333333-3333-3333-3333-333333333333",
	  "frame_id": "44444444-4444-4444-4444-444444444444",
	  "parent_claim_handle_id": "55555555-5555-5555-5555-555555555555",
	  "open_lineage_run_ref": "22222222-2222-2222-2222-222222222222",
	  "sub_claim_handle_ids": ["c1", "c2"],
	  "committed_at": "2026-05-17T00:00:00Z",
	  "producer_name": "p",
	  "claim_scope_data_hash": "sha256-eee",
	  "version_id": "v-1",
	  "outcome": "committed",
	  "producer_metadata": {"region": "us-west-1", "shard": 7}
	}`
	var rec ClaimTerminalRecord
	if err := json.Unmarshal([]byte(writerJSON), &rec); err != nil {
		t.Fatalf("decode writer JSON: %v", err)
	}
	if rec.OpenLineageRunRef != "22222222-2222-2222-2222-222222222222" {
		t.Errorf("OpenLineageRunRef = %q", rec.OpenLineageRunRef)
	}
	if rec.ScopeDataHash != "sha256-eee" {
		t.Errorf("ScopeDataHash = %q", rec.ScopeDataHash)
	}
	if rec.Outcome != "committed" {
		t.Errorf("Outcome = %q", rec.Outcome)
	}
	if rec.FrameID != "44444444-4444-4444-4444-444444444444" {
		t.Errorf("FrameID = %q", rec.FrameID)
	}
	if rec.RunID != "22222222-2222-2222-2222-222222222222" {
		t.Errorf("RunID = %q", rec.RunID)
	}
	if rec.NodeID != "33333333-3333-3333-3333-333333333333" {
		t.Errorf("NodeID = %q", rec.NodeID)
	}
	if rec.ParentClaimHandleID != "55555555-5555-5555-5555-555555555555" {
		t.Errorf("ParentClaimHandleID = %q", rec.ParentClaimHandleID)
	}
	if rec.ProducerMetadata["region"] != "us-west-1" {
		t.Errorf("ProducerMetadata = %+v", rec.ProducerMetadata)
	}

	ev := MakeClaimTerminalEvent(rec, time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC), "ns-test")
	facets, ok := ev.Run.Facets["rimsky"].(map[string]any)
	if !ok {
		t.Fatalf("rimsky facet block missing or wrong type: %+v", ev.Run.Facets)
	}
	if facets["run_id"] != "22222222-2222-2222-2222-222222222222" {
		t.Errorf("facet run_id = %v", facets["run_id"])
	}
	if facets["node_id"] != "33333333-3333-3333-3333-333333333333" {
		t.Errorf("facet node_id = %v", facets["node_id"])
	}
	if facets["parent_claim_handle_id"] != "55555555-5555-5555-5555-555555555555" {
		t.Errorf("facet parent_claim_handle_id = %v", facets["parent_claim_handle_id"])
	}
	pm, ok := facets["producer_metadata"].(map[string]any)
	if !ok || pm["region"] != "us-west-1" {
		t.Errorf("facet producer_metadata = %+v", facets["producer_metadata"])
	}
}

func TestLeafRunRecord_TagDisciplineAndOrder(t *testing.T) {
	t.Parallel()
	want := []struct {
		field     string
		jsonTag   string
		omitempty bool
	}{
		{"RunID", "run_id", false},
		{"NodeID", "node_id", false},
		{"FrameID", "frame_id", false},
		{"ChildKey", "child_key", true},
		{"NodeAlias", "node_alias", true},
		{"ParentRunID", "parent_run_id", true},
		{"FrameTriggerKind", "frame_trigger_kind", true},
		{"TriggerMessageID", "trigger_message_id", true},
		{"HeldClaims", "held_claims", true},
		{"ExecutorName", "executor_name", true},
		{"TemplateHash", "template_hash", true},
		{"TemplateNodeAlias", "template_node_alias", true},
		{"ParamsSnapshotHash", "params_snapshot_hash", true},
		{"AttributesHash", "attributes_hash", true},
		{"ScopeDataHash", "claim_scope_data_hash", true},
		{"State", "state", false},
		{"SettlingSignalType", "settling_signal_type", false},
		{"Changed", "changed", true},
		{"TerminalKind", "terminal_kind", true},
		{"ErrorClass", "error_class", true},
		{"SubstitutionRefs", "substitution_refs", true},
		{"Extra", "extra", true},
	}
	assertStructJSONShape(t, LeafRunRecord{}, want)
}

func TestClaimTerminalRecord_TagDisciplineAndOrder(t *testing.T) {
	t.Parallel()
	want := []struct {
		field     string
		jsonTag   string
		omitempty bool
	}{
		{"ClaimHandleID", "claim_handle_id", false},
		{"RunID", "run_id", false},
		{"NodeID", "node_id", false},
		{"FrameID", "frame_id", false},
		{"ParentClaimHandleID", "parent_claim_handle_id", true},
		{"OpenLineageRunRef", "open_lineage_run_ref", true},
		{"SubClaimHandleIDs", "sub_claim_handle_ids", true},
		{"CommittedAt", "committed_at", true},
		{"ProducerName", "producer_name", true},
		{"ScopeDataHash", "claim_scope_data_hash", true},
		{"VersionID", "version_id", true},
		{"Outcome", "outcome", false},
		{"Cause", "cause", true},
		{"ProducerMetadata", "producer_metadata", true},
		{"TerminatingSupervisorID", "terminating_supervisor_id", true},
	}
	assertStructJSONShape(t, ClaimTerminalRecord{}, want)
}

func assertStructJSONShape(t *testing.T, v any, want []struct {
	field     string
	jsonTag   string
	omitempty bool
}) {
	t.Helper()
	rt := reflect.TypeOf(v)
	if rt.NumField() != len(want) {
		t.Fatalf("%s: NumField = %d, want %d (fields drifted; update both sides + this test)",
			rt.Name(), rt.NumField(), len(want))
	}
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		w := want[i]
		if f.Name != w.field {
			t.Errorf("%s field[%d]: name = %q, want %q (order drifted)", rt.Name(), i, f.Name, w.field)
			continue
		}
		tag := f.Tag.Get("json")
		gotName, gotOmit := parseJSONTag(tag)
		if gotName != w.jsonTag {
			t.Errorf("%s.%s json tag name = %q, want %q", rt.Name(), f.Name, gotName, w.jsonTag)
		}
		if gotOmit != w.omitempty {
			t.Errorf("%s.%s omitempty = %v, want %v", rt.Name(), f.Name, gotOmit, w.omitempty)
		}
	}
}

func parseJSONTag(tag string) (name string, omitempty bool) {
	parts := strings.Split(tag, ",")
	if len(parts) == 0 {
		return tag, false
	}
	name = parts[0]
	for _, opt := range parts[1:] {
		if opt == "omitempty" {
			omitempty = true
		}
	}
	return name, omitempty
}
