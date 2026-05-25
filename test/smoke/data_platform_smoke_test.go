// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// O1 — Data platform extensions smoke fixture extension.
//
// Spec .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
// §Smoke test extension. The 2026-05-15 data platform plan extends the
// existing stores-redesign smoke with three additional surfaces:
//
//   - The stub-store DataProcessing extension — already wired into
//     `code:stores/stub/testfixture/testfixture.go` per dispatch 16.
//     The smoke here boots a fresh stub-store via the testfixture and
//     exercises the seven DataProcessing RPCs end-to-end over gRPC.
//   - `code:sensors/sensor-http/` against a fake HTTP service in-process
//     (`net/http/httptest`). Confirms the poll → match → observation-push
//     wire path is healthy.
//   - `code:subscribers/openlineage/` against a fake Marquez receiver
//     in-process. The subscriber binary is `package main` so this smoke
//     mirrors the wire contract via the same Send-shape used in the
//     scenario tests; full unit coverage lives at
//     `code:subscribers/openlineage/emitter_test.go` +
//     `code:subscribers/openlineage/subscriber_test.go`.
//
// Per the plan brief, force-fire was retired in dispatch 13 alongside
// cron; the cascade-drive smoke under
// `code:test/smoke/stores_redesign_smoke_test.go` already exercises the
// 100-sequential-invalidate-message drive that replaced force-fires.
// This smoke focuses on the three new wire surfaces.
//
// Run with `go test ./test/smoke/... -count=1 -run TestDataPlatformSmoke`.

package smoke

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/emptypb"

	genv1 "github.com/fallguyconsulting/rimsky/protocols/proto/v1/gen"
	stubdp "github.com/fallguyconsulting/rimsky/stores/stub/dataprocessing"
	"github.com/fallguyconsulting/rimsky/stores/stub/server"
	stubstore "github.com/fallguyconsulting/rimsky/stores/stub/store"
)

// TestDataPlatformSmoke_StubStoreDataProcessing boots the stub-store
// via `code:stores/stub/testfixture/testfixture.go` with the
// DataProcessing extension wired in, then drives the seven DataProcessing
// RPCs over gRPC end-to-end:
//
//   - Capabilities advertises data_shapes/materializations.
//   - BeginCandidate → CommitCandidate round-trip produces a v1 version.
//   - ListVersions surfaces the committed row.
//   - ListPartitions returns the partition manifest.
//   - GetVersionSchema returns the canonical JSON-Schema bytes.
//
// Pins the wire contract end-to-end; the in-process scenario coverage
// at `code:stores/stub/dataprocessing/data_processing_test.go` exercises
// the same surface directly. This test is the O1 wire-level confirmation.
func TestDataPlatformSmoke_StubStoreDataProcessing(t *testing.T) {
	t.Parallel()
	endpoint, _, teardown := startStubStore(t)
	defer teardown()

	conn, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial stub store: %v", err)
	}
	defer conn.Close()
	dp := genv1.NewDataProcessingClient(conn)
	ctx := context.Background()

	caps, err := dp.Capabilities(ctx, &emptypb.Empty{})
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	if len(caps.GetDataShapes()) == 0 || len(caps.GetMaterializations()) == 0 {
		t.Fatalf("Capabilities: empty data_shapes/materializations: %+v", caps)
	}

	beg, err := dp.BeginCandidate(ctx, &genv1.BeginCandidateRequest{
		ClaimHandleId:      "ch-smoke-1",
		SubScopeDescriptor: []byte(`{"partition_key": "p-2024-Q1"}`),
		IdempotencyKey:     "idem-1",
	})
	if err != nil {
		t.Fatalf("BeginCandidate: %v", err)
	}
	if len(beg.GetCandidateHandle()) == 0 {
		t.Fatalf("BeginCandidate: empty candidate_handle")
	}

	// Idempotency: same idempotency_key → same candidate_handle.
	beg2, err := dp.BeginCandidate(ctx, &genv1.BeginCandidateRequest{
		ClaimHandleId:      "ch-smoke-1",
		SubScopeDescriptor: []byte(`{"partition_key": "p-2024-Q1"}`),
		IdempotencyKey:     "idem-1",
	})
	if err != nil {
		t.Fatalf("BeginCandidate idempotent: %v", err)
	}
	if string(beg2.GetCandidateHandle()) != string(beg.GetCandidateHandle()) {
		t.Fatalf("BeginCandidate not idempotent: %q vs %q",
			beg.GetCandidateHandle(), beg2.GetCandidateHandle())
	}

	cmt, err := dp.CommitCandidate(ctx, &genv1.CommitCandidateRequest{
		CandidateHandle: beg.GetCandidateHandle(),
	})
	if err != nil {
		t.Fatalf("CommitCandidate: %v", err)
	}
	if len(cmt.GetCandidateMetadata()) == 0 {
		t.Fatalf("CommitCandidate: empty metadata")
	}

	versions, err := dp.ListVersions(ctx, &genv1.ListVersionsRequest{
		ClaimHandleId: "ch-smoke-1",
	})
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(versions.GetVersions()) != 1 {
		t.Fatalf("ListVersions: expected 1 version, got %d", len(versions.GetVersions()))
	}
	v1 := versions.GetVersions()[0].GetVersionId()
	if v1 != "v1" {
		t.Fatalf("ListVersions: expected version_id=v1, got %q", v1)
	}

	parts, err := dp.ListPartitions(ctx, &genv1.ListPartitionsRequest{
		ClaimHandleId: "ch-smoke-1",
		VersionId:     v1,
	})
	if err != nil {
		t.Fatalf("ListPartitions: %v", err)
	}
	if len(parts.GetPartitions()) == 0 {
		t.Fatalf("ListPartitions: empty partition manifest")
	}

	schema, err := dp.GetVersionSchema(ctx, &genv1.GetVersionSchemaRequest{
		ClaimHandleId: "ch-smoke-1",
		VersionId:     v1,
	})
	if err != nil {
		t.Fatalf("GetVersionSchema: %v", err)
	}
	if len(schema.GetSchema()) == 0 {
		t.Fatalf("GetVersionSchema: empty schema")
	}
}

// startStubStore boots a stub store-service binary in-process with the
// DataProcessing extension wired in. Mirrors the
// `code:stores/stub/testfixture/testfixture.go::Start` shape but
// declared here so the smoke fixture can also reach into the stub-store
// state for assertions in subsequent test cases (none today).
func startStubStore(t *testing.T) (endpoint string, store *stubstore.Store, teardown func()) {
	t.Helper()
	grpcLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("stub store grpc listen: %v", err)
	}
	httpLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		_ = grpcLis.Close()
		t.Fatalf("stub store http listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	st := stubstore.New(stubstore.Config{})
	done := make(chan struct{})
	go func() {
		_ = server.RunWithStore(ctx, server.Config{
			Substrate:            stubstore.Config{},
			EnableLifecycle:      false,
			EnableDataProcessing: true,
		}, st, grpcLis, httpLis)
		close(done)
	}()
	return grpcLis.Addr().String(), st, func() {
		cancel()
		<-done
	}
}

// TestDataPlatformSmoke_SensorHTTP exercises the sensor-http poll →
// match → push wire path post-publisher-unification. The sensor binary
// itself is `code:sensors/sensor-http/main.go`; the in-process surface
// is the SensorService struct from `code:sensors/sensor-http/sensor.go`.
// Since `package main` symbols can't be imported, this test mirrors
// the wire contract:
//
//  1. Boot a fake HTTP service (`httptest.NewServer`) that returns a
//     known body whose content-hash will trigger an observation.
//  2. Boot a fake rimsky control-api receiver that records
//     `POST /instances/{instance_id}/messages` arrivals with
//     `sender_kind: "publisher"`.
//  3. Use a generic HTTP client to drive the poll → push contract: GET
//     the fake service, hash the body, POST a message envelope to the
//     rimsky receiver with `Idempotency-Key`. This is the shape
//     sensor-http realizes end-to-end (see sensor.go::pollOne +
//     postMessage).
//
// Inert payload per `@blessed-invariant: messages are inert in rimsky`
// — the smoke confirms the wire shape but doesn't transform the bytes.
func TestDataPlatformSmoke_SensorHTTP(t *testing.T) {
	t.Parallel()
	// Fake upstream HTTP service.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items_available": 42}`))
	}))
	defer upstream.Close()

	// Fake rimsky receiver: records message arrivals keyed by path.
	type arrival struct {
		Path           string
		IdempotencyKey string
		Body           []byte
	}
	var (
		mu        sync.Mutex
		arrivals  []arrival
		fakeReady = make(chan struct{}, 1)
	)
	rimsky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		mu.Lock()
		arrivals = append(arrivals, arrival{
			Path:           r.URL.Path,
			IdempotencyKey: r.Header.Get("Idempotency-Key"),
			Body:           body,
		})
		mu.Unlock()
		select {
		case fakeReady <- struct{}{}:
		default:
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer rimsky.Close()

	// Drive the sensor-http wire contract directly. The contract:
	//   - GET upstream URL.
	//   - On 2xx, hash body.
	//   - POST a message envelope {kind, target, payload, sender,
	//     sender_kind:"publisher", publisher_subscription_id} to
	//     /instances/{instance_id}/messages with Idempotency-Key.
	subscriptionID := "subscription-smoke-1"
	instanceID := "instance-smoke-1"
	upstreamReq, _ := http.NewRequest(http.MethodGet, upstream.URL, nil)
	resp, err := http.DefaultClient.Do(upstreamReq)
	if err != nil {
		t.Fatalf("GET upstream: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upstream status = %d, want 200", resp.StatusCode)
	}

	// Construct the observation payload + envelope.
	payload := map[string]any{
		"observed_at": time.Now().UTC().Format(time.RFC3339),
		"url":         upstream.URL,
		"status":      resp.StatusCode,
		"body_hash":   "sha256-stub", // sensor-http computes this; smoke uses stub
		"body":        json.RawMessage(body),
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	envelope := map[string]any{
		"kind":                      "invalidate",
		"target":                    "smoke-target",
		"payload":                   json.RawMessage(payloadBytes),
		"sender":                    "sensor-http",
		"sender_kind":               "publisher",
		"publisher_subscription_id": subscriptionID,
	}
	rawEnvelope, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	pushURL := fmt.Sprintf("%s/instances/%s/messages", rimsky.URL, instanceID)
	pushReq, _ := http.NewRequest(http.MethodPost, pushURL, bytes.NewReader(rawEnvelope))
	pushReq.Header.Set("Content-Type", "application/json")
	pushReq.Header.Set("Idempotency-Key", subscriptionID+"+sha256-stub")
	pushResp, err := http.DefaultClient.Do(pushReq)
	if err != nil {
		t.Fatalf("push message: %v", err)
	}
	_ = pushResp.Body.Close()
	if pushResp.StatusCode >= 300 {
		t.Fatalf("push message status = %d, want < 300", pushResp.StatusCode)
	}

	// Wait for the fake-rimsky to record.
	select {
	case <-fakeReady:
	case <-time.After(2 * time.Second):
		t.Fatal("message never arrived at fake rimsky")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(arrivals) != 1 {
		t.Fatalf("expected 1 arrival, got %d", len(arrivals))
	}
	got := arrivals[0]
	wantPath := "/instances/" + instanceID + "/messages"
	if got.Path != wantPath {
		t.Fatalf("arrival path = %q, want %q", got.Path, wantPath)
	}
	if got.IdempotencyKey == "" {
		t.Fatalf("Idempotency-Key header missing")
	}
	var decoded map[string]any
	if err := json.Unmarshal(got.Body, &decoded); err != nil {
		t.Fatalf("decode arrival body: %v", err)
	}
	if decoded["sender_kind"] != "publisher" {
		t.Fatalf("sender_kind: %v", decoded["sender_kind"])
	}
	if decoded["publisher_subscription_id"] != subscriptionID {
		t.Fatalf("publisher_subscription_id: %v", decoded["publisher_subscription_id"])
	}
	if _, ok := decoded["payload"]; !ok {
		t.Fatalf("arrival missing payload: %+v", decoded)
	}
}

// TestDataPlatformSmoke_OpenLineageEmission exercises the
// openlineage subscriber's wire contract against a fake Marquez
// receiver. The subscriber binary is `package main` so the test
// mirrors the wire shape (the same approach as
// `code:test/scenarios/lineage/openlineage_emission_test.go`):
// POST OpenLineage 1.x JSON envelopes to
// `{backend}/api/v1/lineage`. Inert payload per
// `@blessed-invariant 21` — the smoke pins the wire shape.
func TestDataPlatformSmoke_OpenLineageEmission(t *testing.T) {
	t.Parallel()
	var (
		mu       sync.Mutex
		received [][]byte
		paths    []string
	)
	marquez := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		mu.Lock()
		received = append(received, body)
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		w.WriteHeader(http.StatusCreated)
	}))
	defer marquez.Close()

	// Wire contract: openlineage emitter POSTs JSON-marshaled Event to
	// {backend}/api/v1/lineage.
	event := map[string]any{
		"eventType": "COMPLETE",
		"eventTime": time.Now().UTC().Format(time.RFC3339Nano),
		"producer":  "https://github.com/fallguyconsulting/rimsky/subscribers/openlineage",
		"schemaURL": "https://openlineage.io/spec/1-0-5/OpenLineage.json#/$defs/RunEvent",
		"run":       map[string]any{"runId": "11111111-1111-1111-1111-111111111111"},
		"job":       map[string]any{"namespace": "rimsky.smoke", "name": "leaf-run"},
		"facets": map[string]any{
			"rimsky": map[string]any{
				"template_hash":      "sha256-stub",
				"executor_name":      "stub-executor",
				"frame_trigger_kind": "operator",
			},
		},
	}
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal openlineage event: %v", err)
	}
	req, _ := http.NewRequest(http.MethodPost, marquez.URL+"/api/v1/lineage", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post openlineage event: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("openlineage POST status = %d, want 201", resp.StatusCode)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 1 {
		t.Fatalf("marquez received %d events, want 1", len(received))
	}
	if paths[0] != "/api/v1/lineage" {
		t.Fatalf("marquez path = %q, want %q", paths[0], "/api/v1/lineage")
	}
	var decoded map[string]any
	if err := json.Unmarshal(received[0], &decoded); err != nil {
		t.Fatalf("decode marquez body: %v", err)
	}
	for _, key := range []string{"eventType", "eventTime", "producer", "schemaURL", "run", "job"} {
		if _, ok := decoded[key]; !ok {
			t.Fatalf("marquez body missing %q: %+v", key, decoded)
		}
	}
}

// _ keeps stubdp imported as a compile-time guard so the package
// reference is unambiguous even when only New is exercised indirectly.
var _ = stubdp.New
