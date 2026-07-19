// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcnet "github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
	"google.golang.org/grpc"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/peer"
	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

func TestE2E_ExampleClaimProducerAgainstRunningRimsky(t *testing.T) {
	ctx := context.Background()

	netName := harness.NewNetwork(ctx, t)
	prodInternal := startExampleClaimProducerOnNetwork(ctx, t, netName, "example-producer")
	okEndpoint := harness.StartExecutorStubOnNetwork(ctx, t, netName)
	errEndpoint := harness.StartErroringExecutorStubOnNetwork(ctx, t, netName)
	ep := harness.BringUpRimsky(ctx, t,
		harness.WithExistingNetwork(netName),
		harness.WithClaimProducer("example", prodInternal, "read_only"),
		harness.WithExecutor("ok", okEndpoint),
		harness.WithExecutor("err", errEndpoint),
	)

	inProcPort := freeHostPort(t)
	inProcProd := startExampleProducerInProcess(t, inProcPort)

	t.Run("Open_and_Commit_on_success_terminal", func(t *testing.T) {
		exerciseOpenCommitLeg(t, ep)
	})
	t.Run("Open_and_Abandon_on_failure_terminal", func(t *testing.T) {
		exerciseOpenAbandonLeg(t, ep)
	})
	t.Run("Release_RPC_lands_on_real_producer", func(t *testing.T) {
		exerciseReleaseLeg(t, inProcProd, inProcPort)
	})
	t.Run("Unadvertised_write_semantics_refused_at_registration", func(t *testing.T) {
		exerciseUnadvertisedWriteSemanticsLeg(t, inProcPort)
	})
}

func exerciseOpenCommitLeg(t *testing.T, ep harness.RimskyEndpoint) {
	tplID := deployClaimTemplate(t, ep, "example-claim-commit", "ok", "r-commit")
	instanceID := createClaimInstance(t, ep, tplID, "ck-example-claim-commit")

	waitForNodeState(t, ep, instanceID, "worker", "fresh", 120*time.Second)
	requireEventKindWithProducer(t, ep, instanceID,
		"claim_resolution.commit", "example", 60*time.Second,
		"the supervisor's terminal pipeline must have called the example producer's Commit RPC and the RPC must have returned successfully (falsifier: Commit called but the producer's effect is canned)")
}

func exerciseOpenAbandonLeg(t *testing.T, ep harness.RimskyEndpoint) {
	tplID := deployClaimTemplateWithErrorPolicy(t, ep, "example-claim-abandon", "err", "r-abandon")
	instanceID := createClaimInstance(t, ep, tplID, "ck-example-claim-abandon")

	waitForNodeState(t, ep, instanceID, "worker", "failed", 120*time.Second)
	requireEventKindWithProducer(t, ep, instanceID,
		"claim_resolution.abandon", "example", 60*time.Second,
		"the supervisor's terminal pipeline must have called the example producer's Abandon RPC and the RPC must have returned successfully (falsifier: Abandon called but the producer's effect is canned)")
}

func exerciseReleaseLeg(t *testing.T, prod *Producer, prodPort int) {
	before := prod.Calls()

	dialCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := peer.Dial(dialCtx, "example", fmt.Sprintf("127.0.0.1:%d", prodPort), peer.TLSModeOff)
	if err != nil {
		t.Fatalf("peer.Dial against the in-process example producer: %v", err)
	}
	defer client.Close()

	const releaseClaimID = "release-test-claim-id-cabc1234"
	callCtx, callCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer callCancel()
	if rErr := client.Release(callCtx,
		claimproducer.ClaimID(releaseClaimID),
		[]byte("release-test-scope"),
		[]byte("release-test-address"),
	); rErr != nil {
		t.Fatalf("Client.Release against the example producer: %v (the producer's Release handler must accept the verb and return without error — falsifier: rimsky drives Release but the producer is unreachable / canned)", rErr)
	}

	after := prod.Calls()
	if after.Release <= before.Release {
		t.Fatalf("Release count did NOT grow on the in-process producer: before=%d after=%d — the peer client did not call the producer's Release handler (falsifier: the verb's effect was canned)",
			before.Release, after.Release)
	}

	releaseIDs := prod.ReleaseClaimIDs()
	if len(releaseIDs) == 0 {
		t.Fatalf("Release landed but no claim_ids were recorded — internal counter inconsistency")
	}
	gotID := releaseIDs[len(releaseIDs)-1]
	if gotID != releaseClaimID {
		t.Fatalf("Release landed with claim_id %q, want %q — the producer's effect must NOT be canned; it must receive the claim_id rimsky passed",
			gotID, releaseClaimID)
	}
}

func exerciseUnadvertisedWriteSemanticsLeg(t *testing.T, prodPort int) {
	dialCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := peer.Dial(dialCtx, "example", fmt.Sprintf("127.0.0.1:%d", prodPort), peer.TLSModeOff)
	if err != nil {
		t.Fatalf("peer.Dial against the in-process example producer: %v (the dial must succeed — the registration refusal we are proving happens at ValidateCapabilities, AFTER Capabilities returns successfully)", err)
	}
	defer client.Close()

	declared := claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
	}
	vErr := client.ValidateCapabilities(declared)
	if vErr == nil {
		t.Fatalf("ValidateCapabilities accepted an operator envelope ([sync]) that the producer NEVER advertised (producer's Capabilities returns [read_only] only) — the falsifier fires (\"a write-semantics the producer didn't advertise is silently accepted at registration\"). A startup config with this envelope would cause the rimsky-all-in-one container to exit non-zero before /health flips to 200")
	}

	msg := strings.ToLower(vErr.Error())
	if !strings.Contains(msg, "capabilities mismatch") {
		t.Fatalf("ValidateCapabilities rejected the envelope but the error does not name the canonical failure mode (\"capabilities mismatch\"): %v", vErr)
	}
}

func requireEventKindWithProducer(t *testing.T, ep harness.RimskyEndpoint, instanceID, kind, wantProducer string, deadline time.Duration, why string) {
	t.Helper()
	end := time.Now().Add(deadline)
	path := fmt.Sprintf("/v1/events?instance_id=%s&kind=%s", instanceID, kind)
	var lastStatus int
	var lastBody string
	for time.Now().Before(end) {
		statusCode, raw := ep.GetJSON(t, path, "")
		lastStatus, lastBody = statusCode, string(raw)
		if statusCode == http.StatusOK {
			var resp struct {
				Events []struct {
					Kind    string          `json:"kind"`
					Payload json.RawMessage `json:"payload"`
				} `json:"events"`
			}
			if err := json.Unmarshal(raw, &resp); err == nil {
				for _, e := range resp.Events {
					if e.Kind != kind {
						continue
					}
					var p map[string]any
					if jErr := json.Unmarshal(e.Payload, &p); jErr != nil {
						continue
					}
					if pn, ok := p["producer_name"].(string); ok && pn == wantProducer {
						return
					}
				}
			}
		}
		time.Sleep(1 * time.Second)
	}
	dump := dumpEventKindsForInstance(t, ep, instanceID)
	t.Fatalf("event kind %q with producer_name=%q never landed on the event log for instance %s within %v (last GET status=%d body=%s) — %s\nobserved event kinds on this instance: %v",
		kind, wantProducer, instanceID, deadline, lastStatus, lastBody, why, dump)
}

func dumpEventKindsForInstance(t *testing.T, ep harness.RimskyEndpoint, instanceID string) []string {
	t.Helper()
	var (
		statusCode int
		raw        []byte
	)
	for attempt := 0; attempt < 10; attempt++ {
		statusCode, raw = ep.GetJSON(t, "/v1/events?instance_id="+instanceID+"&limit=500", "")
		if statusCode == http.StatusOK {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if statusCode != http.StatusOK {
		return []string{fmt.Sprintf("<GET /v1/events failed after retries: %d %s>", statusCode, string(raw))}
	}
	var resp struct {
		Events []struct {
			Kind string `json:"kind"`
		} `json:"events"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return []string{fmt.Sprintf("<decode failed: %v>", err)}
	}
	seen := map[string]int{}
	for _, e := range resp.Events {
		seen[e.Kind]++
	}
	out := make([]string, 0, len(seen))
	for k, n := range seen {
		out = append(out, fmt.Sprintf("%s(%d)", k, n))
	}
	return out
}

func deployClaimTemplate(t *testing.T, ep harness.RimskyEndpoint, name, executor, selector string) string {
	t.Helper()
	return deployClaimTemplateInternal(t, ep, name, executor, selector, false)
}

func deployClaimTemplateWithErrorPolicy(t *testing.T, ep harness.RimskyEndpoint, name, executor, selector string) string {
	t.Helper()
	return deployClaimTemplateInternal(t, ep, name, executor, selector, true)
}

func deployClaimTemplateInternal(t *testing.T, ep harness.RimskyEndpoint, name, executor, selector string, withErrorPolicy bool) string {
	t.Helper()
	node := map[string]any{
		"type":     "worker",
		"executor": executor,
		"claim_producers": []map[string]any{
			{
				"name":     "example",
				"selector": selector,
				"intent":   "r",
				"alias":    "claim",
			},
		},
	}
	if withErrorPolicy {
		node["error_types"] = map[string]any{
			"stub/forced_error": map[string]any{
				"action": "give_up",
			},
		}
	}
	body := map[string]any{
		"spec": map[string]any{
			"name":    name,
			"version": "1",
			"nodes":   []map[string]any{node},
		},
	}
	statusCode, raw := ep.PostJSON(t, "/v1/templates", body)
	if statusCode != http.StatusCreated {
		t.Fatalf("POST /v1/templates (%s): %d %s", name, statusCode, string(raw))
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
func createClaimInstance(t *testing.T, ep harness.RimskyEndpoint, templateID, instanceKey string) string {
	t.Helper()
	statusCode, raw := ep.PostJSON(t, "/v1/instances", map[string]any{
		"template":     templateID,
		"instance_key": instanceKey,
		"params":       map[string]any{},
	})
	if statusCode != http.StatusCreated {
		t.Fatalf("POST /v1/instances: %d %s", statusCode, string(raw))
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
	ep.EmptyWakeAfterCreate(t, resp.InstanceID, "claimproducer-example", instanceKey)
	return resp.InstanceID
}

// @concept: node
func waitForNodeState(t *testing.T, ep harness.RimskyEndpoint, instanceID, nodeType, want string, deadline time.Duration) {
	t.Helper()
	end := time.Now().Add(deadline)
	var last string
	for time.Now().Before(end) {
		statusCode, raw := ep.GetJSON(t, "/v1/observability/nodes/"+instanceID+"/"+nodeType, "")
		if statusCode == http.StatusOK {
			var resp struct {
				RunSummary struct {
					ActiveCount  int `json:"active_count"`
					PendingCount int `json:"pending_count"`
					FreshCount   int `json:"fresh_count"`
					FailedCount  int `json:"failed_count"`
				} `json:"run_summary"`
			}
			if err := json.Unmarshal(raw, &resp); err == nil {
				got := categorize(resp.RunSummary.ActiveCount, resp.RunSummary.PendingCount, resp.RunSummary.FreshCount, resp.RunSummary.FailedCount)
				last = got
				if got == want {
					return
				}
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("node %q on instance %s did not reach %q within %v; last categorical state=%q",
		nodeType, instanceID, want, deadline, last)
}

// @concept: node
func categorize(active, pending, fresh, failed int) string {
	if failed > 0 {
		return "failed"
	}
	if active > 0 || pending > 0 {
		return "in-flight"
	}
	if fresh > 0 {
		return "fresh"
	}
	return "idle"
}

const exampleClaimProducerImage = "rimsky-example/claim-producer:latest"

func startExampleClaimProducerOnNetwork(ctx context.Context, t *testing.T, networkName, alias string) (endpoint string) {
	t.Helper()
	c, err := testcontainers.Run(ctx, exampleClaimProducerImage,
		tcnet.WithNetworkName([]string{alias}, networkName),
		testcontainers.WithExposedPorts("9400/tcp"),
		testcontainers.WithWaitStrategy(
			wait.ForListeningPort("9400/tcp").WithStartupTimeout(120*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("harness: start example claim-producer: %v", err)
	}
	t.Cleanup(func() {
		termCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = c.Terminate(termCtx)
	})
	return alias + ":9400"
}

func freeHostPort(t *testing.T) int {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	port := lis.Addr().(*net.TCPAddr).Port
	if cerr := lis.Close(); cerr != nil {
		t.Fatalf("close listener: %v", cerr)
	}
	return port
}

func startExampleProducerInProcess(t *testing.T, port int) *Producer {
	t.Helper()
	lis, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("listen %d: %v", port, err)
	}
	srv := grpc.NewServer()
	prod := newProducer()
	genv1.RegisterClaimProducerServer(srv, prod)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		conn, dialErr := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if dialErr == nil {
			_ = conn.Close()
			return prod
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("in-process example producer did not become dialable at %s within 10s", addr)
	return nil
}
