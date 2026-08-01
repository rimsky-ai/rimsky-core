// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: lineage
// @concept: lineage-record
// @story: subscriber-lineage-receiver
package scenarios

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcnet "github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

const openLineageSubscriberImage = "rimsky-subscriber-openlineage"

const openLineageNamespace = "rimsky-e2e-namespace"

const openLineagePollInterval = 250 * time.Millisecond

func TestSubscriberOpenlineage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	var (
		mu       sync.Mutex
		received []olArrival
	)
	marquez := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		a := olArrival{Path: r.URL.Path, Body: body}
		decoded := map[string]any{}
		if err := json.Unmarshal(body, &decoded); err != nil {
			a.ValidateErr = fmt.Sprintf("json decode: %v", err)
		} else {
			a.Decoded = decoded
			a.ValidateErr = validateOpenLineageEnvelope(decoded)
		}
		mu.Lock()
		received = append(received, a)
		mu.Unlock()
		if a.ValidateErr != "" {
			http.Error(w, a.ValidateErr, http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(marquez.Close)

	receiverHostPort := hostPortOf(t, marquez.URL)

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

	templateID := deployOLTemplate(t, ep, "openlineage-e2e")
	instanceID := createOLInstance(t, ep, templateID, "ck-openlineage-e2e")

	ep.RequireNodeTerminalSucceeded(t, instanceID, "acquire-and-execute")

	pool, err := pgxpool.New(ctx, ep.HostDSN)
	if err != nil {
		t.Fatalf("connect rimsky host DSN: %v", err)
	}
	defer pool.Close()

	waitOLLineageRows(t, ctx, pool, "leaf_run", 1, 60*time.Second)
	waitOLLineageRows(t, ctx, pool, "claim_terminal", 1, 60*time.Second)

	rimskyLeafRunIDs := selectOLLeafRunIDs(t, ctx, pool, instanceID)
	rimskyClaimScopeHashes := selectOLClaimScopeHashes(t, ctx, pool, instanceID)
	if len(rimskyLeafRunIDs) < 1 {
		t.Fatalf("rimsky-side leaf_run snapshot empty for instance %s; the "+
			"writer never produced a leaf_run row", instanceID)
	}
	if len(rimskyClaimScopeHashes) < 1 {
		t.Fatalf("rimsky-side claim_terminal snapshot empty for instance %s; "+
			"the producer's Commit never appended a claim_terminal row", instanceID)
	}

	leafRunIDSet := make(map[string]bool, len(rimskyLeafRunIDs))
	for _, id := range rimskyLeafRunIDs {
		leafRunIDSet[id] = true
	}
	t.Logf("rimsky-side IDs: instance_id=%s leaf_runs=%d claim_scope_hashes=%d (expected_ol_runids=%v)",
		instanceID, len(rimskyLeafRunIDs), len(rimskyClaimScopeHashes), rimskyLeafRunIDs)

	startOpenLineageSubscriber(ctx, t,
		netName,
		ep.InternalDSN,
		fmt.Sprintf("http://host.testcontainers.internal:%d", receiverHostPort),
		openLineageNamespace,
		receiverHostPort,
	)

	waitForOLArrivalMatching(t, &mu, &received, 60*time.Second,
		"leaf_run → COMPLETE event whose runId is a rimsky node-run UUID",
		func(a olArrival) bool {
			if a.ValidateErr != "" {
				return false
			}
			run, _ := a.Decoded["run"].(map[string]any)
			runID, _ := run["runId"].(string)
			job, _ := a.Decoded["job"].(map[string]any)
			ns, _ := job["namespace"].(string)
			return leafRunIDSet[runID] &&
				ns == openLineageNamespace &&
				a.Decoded["eventType"] == "COMPLETE"
		})

	waitForOLArrivalMatching(t, &mu, &received, 60*time.Second,
		"claim_terminal → event with dataset output keyed on rimsky scope_data_hash",
		func(a olArrival) bool {
			if a.ValidateErr != "" {
				return false
			}
			outputs, _ := a.Decoded["outputs"].([]any)
			if len(outputs) == 0 {
				return false
			}
			for _, o := range outputs {
				out, _ := o.(map[string]any)
				name, _ := out["name"].(string)
				if name == "" {
					continue
				}
				for _, want := range rimskyClaimScopeHashes {
					if name == want {
						return true
					}
				}
			}
			return false
		})

	mu.Lock()
	defer mu.Unlock()
	var validationErrs []string
	for i, a := range received {
		if a.ValidateErr != "" {
			validationErrs = append(validationErrs,
				fmt.Sprintf("arrival[%d] path=%s err=%s body=%s",
					i, a.Path, a.ValidateErr, string(a.Body)))
		}
		if a.Path != "/api/v1/lineage" {
			t.Errorf("arrival[%d] path = %q, want /api/v1/lineage (the documented OL transport)",
				i, a.Path)
		}
	}
	if len(validationErrs) > 0 {
		t.Fatalf("received %d malformed OpenLineage envelopes (falsifier 'malformed JSON' surfaced):\n%s",
			len(validationErrs), strings.Join(validationErrs, "\n"))
	}
}

func validateOpenLineageEnvelope(body map[string]any) string {
	allowedEvents := map[string]bool{
		"START":    true,
		"RUNNING":  true,
		"COMPLETE": true,
		"ABORT":    true,
		"FAIL":     true,
		"OTHER":    true,
	}
	eventType, _ := body["eventType"].(string)
	if !allowedEvents[eventType] {
		return fmt.Sprintf("eventType=%q not in OpenLineage 1.x allowed set", eventType)
	}
	eventTime, _ := body["eventTime"].(string)
	if eventTime == "" {
		return "eventTime missing or empty"
	}
	if _, err := time.Parse(time.RFC3339Nano, eventTime); err != nil {
		if _, err2 := time.Parse(time.RFC3339, eventTime); err2 != nil {
			return fmt.Sprintf("eventTime %q not RFC3339-parseable: %v", eventTime, err)
		}
	}
	producer, _ := body["producer"].(string)
	if producer == "" {
		return "producer URI missing or empty"
	}
	schemaURL, _ := body["schemaURL"].(string)
	if schemaURL == "" {
		return "schemaURL missing or empty"
	}
	if !strings.Contains(schemaURL, "openlineage.io") {
		return fmt.Sprintf("schemaURL %q does not reference openlineage.io", schemaURL)
	}
	run, ok := body["run"].(map[string]any)
	if !ok {
		return "run object missing"
	}
	runID, _ := run["runId"].(string)
	if runID == "" {
		return "run.runId missing or empty"
	}
	job, ok := body["job"].(map[string]any)
	if !ok {
		return "job object missing"
	}
	if ns, _ := job["namespace"].(string); ns == "" {
		return "job.namespace missing or empty"
	}
	if name, _ := job["name"].(string); name == "" {
		return "job.name missing or empty"
	}
	return ""
}

func startOpenLineageSubscriber(
	ctx context.Context,
	t *testing.T,
	networkName string,
	rimskyDSN string,
	backendURL string,
	namespace string,
	hostAccessPort int,
) testcontainers.Container {
	t.Helper()
	env := map[string]string{
		"RIMSKY_OPENLINEAGE_RIMSKY_DSN":    rimskyDSN,
		"RIMSKY_OPENLINEAGE_STATE_DSN":     rimskyDSN,
		"RIMSKY_OPENLINEAGE_BACKEND_URL":   backendURL,
		"RIMSKY_OPENLINEAGE_NAMESPACE":     namespace,
		"RIMSKY_OPENLINEAGE_POLL_INTERVAL": openLineagePollInterval.String(),
	}
	opts := []testcontainers.ContainerCustomizer{
		tcnet.WithNetworkName([]string{"subscriber-openlineage"}, networkName),
		testcontainers.WithEnv(env),
		testcontainers.WithHostPortAccess(hostAccessPort),
		testcontainers.WithWaitStrategy(
			wait.ForLog("openlineage.starting").WithStartupTimeout(60 * time.Second),
		),
	}
	c, err := harness.Run(ctx, harness.ImageRef(openLineageSubscriberImage), opts...)
	if err != nil {
		t.Fatalf("harness: start openlineage subscriber container: %v", err)
	}
	t.Cleanup(func() {
		termCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = c.Terminate(termCtx)
	})
	return c
}

func waitOLLineageRows(t *testing.T, ctx context.Context, pool *pgxpool.Pool, kind string, want int, deadline time.Duration) {
	t.Helper()
	end := time.Now().Add(deadline)
	var n int
	for time.Now().Before(end) {
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM rimsky_lineage WHERE record_kind = $1`, kind,
		).Scan(&n); err != nil {
			t.Fatalf("count rimsky_lineage where record_kind=%s: %v", kind, err)
		}
		if n >= want {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("rimsky_lineage record_kind=%s: want >= %d rows within %v, got %d — "+
		"the writer never produced this record_kind for the driven cascade",
		kind, want, deadline, n)
}

func selectOLLeafRunIDs(t *testing.T, ctx context.Context, pool *pgxpool.Pool, instanceID string) []string {
	t.Helper()
	rows, err := pool.Query(ctx,
		`SELECT DISTINCT record->>'run_id'
		   FROM rimsky_lineage
		  WHERE record_kind = 'leaf_run' AND instance_id::text = $1`,
		instanceID,
	)
	if err != nil {
		t.Fatalf("select leaf_run run_ids: %v", err)
	}
	defer rows.Close()
	out := make([]string, 0)
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			t.Fatalf("scan leaf_run run_id: %v", err)
		}
		if v != "" {
			out = append(out, v)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate leaf_run rows: %v", err)
	}
	return out
}

func selectOLClaimScopeHashes(t *testing.T, ctx context.Context, pool *pgxpool.Pool, instanceID string) []string {
	t.Helper()
	rows, err := pool.Query(ctx,
		`SELECT DISTINCT record->>'claim_scope_data_hash'
		   FROM rimsky_lineage
		  WHERE record_kind = 'claim_terminal'
		    AND instance_id::text = $1
		    AND record->>'claim_scope_data_hash' IS NOT NULL`,
		instanceID,
	)
	if err != nil {
		t.Fatalf("select claim_terminal claim_scope_data_hashes: %v", err)
	}
	defer rows.Close()
	out := make([]string, 0)
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			t.Fatalf("scan claim_terminal claim_scope_data_hash: %v", err)
		}
		if v != "" {
			out = append(out, v)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate claim_terminal rows: %v", err)
	}
	return out
}

type olArrival struct {
	Path        string
	Decoded     map[string]any
	ValidateErr string
	Body        []byte
}

func waitForOLArrivalMatching(
	t *testing.T,
	mu *sync.Mutex,
	received *[]olArrival,
	deadline time.Duration,
	label string,
	pred func(a olArrival) bool,
) {
	t.Helper()
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		mu.Lock()
		snapshot := append([]olArrival{}, (*received)...)
		mu.Unlock()
		for _, a := range snapshot {
			if pred(a) {
				return
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	t.Fatalf("no OpenLineage arrival matched %q within %v (got %d arrivals); "+
		"this either means the subscriber never reached the receiver, or it "+
		"emitted events whose runId/dataset name does not correspond to the "+
		"rimsky-side records (the falsifier). Arrivals: %s",
		label, deadline, len(*received), dumpOLArrivals(*received))
}

func dumpOLArrivals(arrivals []olArrival) string {
	var b strings.Builder
	for i, a := range arrivals {
		et, _ := a.Decoded["eventType"].(string)
		run, _ := a.Decoded["run"].(map[string]any)
		runID, _ := run["runId"].(string)
		fmt.Fprintf(&b, "\n  [%d] path=%s err=%q eventType=%s runId=%s outputs=%v",
			i, a.Path, a.ValidateErr, et, runID, a.Decoded["outputs"])
	}
	return b.String()
}

func deployOLTemplate(t *testing.T, ep harness.RimskyEndpoint, name string) string {
	t.Helper()
	body := map[string]any{
		"spec": map[string]any{
			"name":    name,
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
	deployStatus, deployRaw := ep.PostJSON(t,
		"/v1/templates/"+resp.TemplateID+"/deploy", map[string]any{})
	if deployStatus != http.StatusOK {
		t.Fatalf("POST /v1/templates/%s/deploy: %d %s",
			resp.TemplateID, deployStatus, string(deployRaw))
	}
	return resp.TemplateID
}

// @decision: test-harness-create-instance-wakes-roots-after-create
func createOLInstance(t *testing.T, ep harness.RimskyEndpoint, templateID, instanceKey string) string {
	t.Helper()
	status, raw := ep.PostJSON(t, "/v1/instances", map[string]any{
		"template":     templateID,
		"instance_key": instanceKey,
		"params":       map[string]any{},
		"target_agent": "scenario-default-agent",
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
	ep.EmptyWakeAfterCreate(t, resp.InstanceID, "ol-instance", instanceKey)
	return resp.InstanceID
}
