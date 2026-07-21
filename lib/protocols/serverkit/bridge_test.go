// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package serverkit

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

type fakeServer struct {
	genv1.UnimplementedClaimProducerServer

	OpenFunc           func(*genv1.OpenRequest) (*genv1.OpenResponse, error)
	SplitScopeFunc     func(*genv1.SplitScopeRequest) (*genv1.SplitScopeResponse, error)
	ScopesConflictFunc func(*genv1.ClaimScopesConflictRequest) (*genv1.ScopesConflictResponse, error)
}

func (f *fakeServer) Open(_ context.Context, req *genv1.OpenRequest) (*genv1.OpenResponse, error) {
	if f.OpenFunc != nil {
		return f.OpenFunc(req)
	}
	return &genv1.OpenResponse{}, nil
}

func (f *fakeServer) SplitScope(_ context.Context, req *genv1.SplitScopeRequest) (*genv1.SplitScopeResponse, error) {
	if f.SplitScopeFunc != nil {
		return f.SplitScopeFunc(req)
	}
	return &genv1.SplitScopeResponse{}, nil
}

func (f *fakeServer) ScopesConflict(_ context.Context, req *genv1.ClaimScopesConflictRequest) (*genv1.ScopesConflictResponse, error) {
	if f.ScopesConflictFunc != nil {
		return f.ScopesConflictFunc(req)
	}
	return &genv1.ScopesConflictResponse{}, nil
}

func mountFake(t *testing.T, srv *fakeServer) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	Mount(mux, srv)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func postOpen(t *testing.T, ts *httptest.Server) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"claim_id":      "00000000-0000-0000-0000-000000000001",
		"producer_name": "fake",
		"selector":      "items/x",
		"intent":        "rw",
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	resp, err := http.Post(ts.URL+"/v1/open", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /v1/open: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status: %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return raw
}

func TestOpenBridge_AcquiredOneof(t *testing.T) {
	addr := []byte(`{"path":"/items/x"}`)
	payload := []byte(`{"data":"hello"}`)
	scope := []byte(`"items/x"`)
	srv := &fakeServer{
		OpenFunc: func(_ *genv1.OpenRequest) (*genv1.OpenResponse, error) {
			return &genv1.OpenResponse{
				Result: &genv1.OpenResponse_Acquired{Acquired: &genv1.Acquired{
					Address:    addr,
					Payload:    payload,
					ClaimScope: scope,
				}},
			}, nil
		},
	}
	ts := mountFake(t, srv)
	raw := postOpen(t, ts)

	var got genv1.OpenResponse
	if err := protojson.Unmarshal(raw, &got); err != nil {
		t.Fatalf("protojson.Unmarshal: %v\nbody: %s", err, raw)
	}
	acq := got.GetAcquired()
	if acq == nil {
		t.Fatalf("expected Acquired arm, got: %s", raw)
	}
	if !bytes.Equal(acq.GetAddress(), addr) {
		t.Errorf("address mismatch: got %q want %q", acq.GetAddress(), addr)
	}
	if !bytes.Equal(acq.GetPayload(), payload) {
		t.Errorf("payload mismatch: got %q want %q", acq.GetPayload(), payload)
	}
	if !bytes.Equal(acq.GetClaimScope(), scope) {
		t.Errorf("scope mismatch: got %q want %q", acq.GetClaimScope(), scope)
	}
}

func TestOpenBridge_UnavailableOneof(t *testing.T) {
	srv := &fakeServer{
		OpenFunc: func(_ *genv1.OpenRequest) (*genv1.OpenResponse, error) {
			return &genv1.OpenResponse{
				Result: &genv1.OpenResponse_Unavailable{Unavailable: &genv1.Unavailable{}},
			}, nil
		},
	}
	ts := mountFake(t, srv)
	raw := postOpen(t, ts)

	var got genv1.OpenResponse
	if err := protojson.Unmarshal(raw, &got); err != nil {
		t.Fatalf("protojson.Unmarshal: %v\nbody: %s", err, raw)
	}
	if got.GetUnavailable() == nil {
		t.Fatalf("expected Unavailable arm, got: %s", raw)
	}
	if got.GetAcquired() != nil {
		t.Fatalf("did not expect Acquired arm: %s", raw)
	}
}

func TestOpenBridge_StdJSONCannotRecoverOneof(t *testing.T) {
	addr := []byte(`{"path":"/items/x"}`)
	srv := &fakeServer{
		OpenFunc: func(_ *genv1.OpenRequest) (*genv1.OpenResponse, error) {
			return &genv1.OpenResponse{
				Result: &genv1.OpenResponse_Acquired{Acquired: &genv1.Acquired{Address: addr}},
			}, nil
		},
	}
	ts := mountFake(t, srv)
	raw := postOpen(t, ts)

	var viaProto genv1.OpenResponse
	if err := protojson.Unmarshal(raw, &viaProto); err != nil {
		t.Fatalf("protojson.Unmarshal: %v\nbody: %s", err, raw)
	}
	if viaProto.GetAcquired() == nil {
		t.Fatalf("protojson did not recover Acquired arm: %s", raw)
	}

	var viaStd genv1.OpenResponse
	if err := json.Unmarshal(raw, &viaStd); err == nil {
		if viaStd.GetAcquired() != nil {
			t.Fatalf("encoding/json unexpectedly recovered the oneof — wire format regressed: %s", raw)
		}
	}
}

func TestLifecycleBridge_TemplateScopeRoundTrip(t *testing.T) {
	var seen string
	srv := &lifecycleFakeServer{
		OnTemplateDeployedFunc: func(req *genv1.OnTemplateDeployedRequest) (*genv1.LifecycleAck, error) {
			seen = req.GetTemplateHash()
			return &genv1.LifecycleAck{}, nil
		},
	}
	mux := http.NewServeMux()
	MountLifecycle(mux, srv)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	body := []byte(`{"template_hash":"sha256-abc"}`)
	resp, err := http.Post(ts.URL+"/v1/on_template_deployed", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("unexpected status %d: %s", resp.StatusCode, raw)
	}
	if seen != "sha256-abc" {
		t.Fatalf("template_hash mismatch: got %q want sha256-abc", seen)
	}
}

func TestLifecycleBridge_InstanceScopeRoundTrip(t *testing.T) {
	var gotTemplate, gotInstance string
	srv := &lifecycleFakeServer{
		OnInstanceTerminatedFunc: func(req *genv1.OnInstanceTerminatedRequest) (*genv1.LifecycleAck, error) {
			gotTemplate = req.GetTemplateHash()
			gotInstance = req.GetInstanceId()
			return &genv1.LifecycleAck{}, nil
		},
	}
	mux := http.NewServeMux()
	MountLifecycle(mux, srv)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	body := []byte(`{"template_hash":"sha256-xyz","instance_id":"00000000-0000-0000-0000-000000000abc"}`)
	resp, err := http.Post(ts.URL+"/v1/on_instance_terminated", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("unexpected status %d: %s", resp.StatusCode, raw)
	}
	if gotTemplate != "sha256-xyz" {
		t.Fatalf("template_hash mismatch: got %q", gotTemplate)
	}
	if gotInstance != "00000000-0000-0000-0000-000000000abc" {
		t.Fatalf("instance_id mismatch: got %q", gotInstance)
	}
}

func TestSplitScopeBridge_RoundTrips(t *testing.T) {
	wantPartitionRequest := []byte(`{"list":[{"key":"a"}]}`)
	wantAddress := []byte(`{"path":"/items/a"}`)
	wantPayload := []byte(`{"v":42}`)
	gotPartitionRequest := []byte(nil)
	gotClaimHandleID := ""
	srv := &fakeServer{}
	srv.SplitScopeFunc = func(req *genv1.SplitScopeRequest) (*genv1.SplitScopeResponse, error) {
		gotClaimHandleID = req.GetClaimHandleId()
		gotPartitionRequest = req.GetPartitionRequest()
		return &genv1.SplitScopeResponse{
			SubScopes: []*genv1.SubScopeDescriptor{
				{
					PartitionKey:   "a",
					ClaimScopeData: []byte(`"sub-a"`),
					Address:        wantAddress,
					Payload:        wantPayload,
				},
			},
		}, nil
	}
	ts := mountFake(t, srv)

	body, err := json.Marshal(map[string]any{
		"claim_handle_id":   "00000000-0000-0000-0000-000000000001",
		"partition_request": wantPartitionRequest,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	resp, err := http.Post(ts.URL+"/v1/split_scope", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /v1/split_scope: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, raw)
	}
	if gotClaimHandleID != "00000000-0000-0000-0000-000000000001" {
		t.Fatalf("claim_handle_id mismatch: %q", gotClaimHandleID)
	}
	if !bytes.Equal(gotPartitionRequest, wantPartitionRequest) {
		t.Fatalf("partition_request not transported intact: got %s want %s", gotPartitionRequest, wantPartitionRequest)
	}
	var got genv1.SplitScopeResponse
	if err := protojson.Unmarshal(raw, &got); err != nil {
		t.Fatalf("protojson.Unmarshal: %v\nbody: %s", err, raw)
	}
	if n := len(got.GetSubScopes()); n != 1 {
		t.Fatalf("SubScopes count = %d, want 1", n)
	}
	sub := got.GetSubScopes()[0]
	if sub.GetPartitionKey() != "a" {
		t.Fatalf("PartitionKey = %q, want \"a\"", sub.GetPartitionKey())
	}
	if !bytes.Equal(sub.GetAddress(), wantAddress) {
		t.Fatalf("Address not transported intact: got %s want %s", sub.GetAddress(), wantAddress)
	}
	if !bytes.Equal(sub.GetPayload(), wantPayload) {
		t.Fatalf("Payload not transported intact: got %s want %s", sub.GetPayload(), wantPayload)
	}
}

func TestScopesConflictBridge_RoundTrips(t *testing.T) {
	wantScopeA := []byte(`"items/a"`)
	wantScopeB := []byte(`"items/b"`)
	gotScopeA := []byte(nil)
	gotScopeB := []byte(nil)
	srv := &fakeServer{}
	srv.ScopesConflictFunc = func(req *genv1.ClaimScopesConflictRequest) (*genv1.ScopesConflictResponse, error) {
		gotScopeA = req.GetClaimScopeA()
		gotScopeB = req.GetClaimScopeB()
		return &genv1.ScopesConflictResponse{Conflicts: true}, nil
	}
	ts := mountFake(t, srv)

	body, err := json.Marshal(map[string]any{
		"claim_scope_a": wantScopeA,
		"claim_scope_b": wantScopeB,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	resp, err := http.Post(ts.URL+"/v1/scopes_conflict", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /v1/scopes_conflict: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, raw)
	}
	if !bytes.Equal(gotScopeA, wantScopeA) {
		t.Fatalf("claim_scope_a not transported intact: got %s want %s", gotScopeA, wantScopeA)
	}
	if !bytes.Equal(gotScopeB, wantScopeB) {
		t.Fatalf("claim_scope_b not transported intact: got %s want %s", gotScopeB, wantScopeB)
	}
	var got genv1.ScopesConflictResponse
	if err := protojson.Unmarshal(raw, &got); err != nil {
		t.Fatalf("protojson.Unmarshal: %v\nbody: %s", err, raw)
	}
	if !got.GetConflicts() {
		t.Fatalf("Conflicts = false, want true")
	}
}

type lifecycleFakeServer struct {
	genv1.UnimplementedLifecycleSubscriberServer

	OnTemplateDeployedFunc   func(*genv1.OnTemplateDeployedRequest) (*genv1.LifecycleAck, error)
	OnInstanceTerminatedFunc func(*genv1.OnInstanceTerminatedRequest) (*genv1.LifecycleAck, error)
	OnInstanceCreatedFunc    func(*genv1.OnInstanceCreatedRequest) (*genv1.LifecycleAck, error)
	OnRunScopeTerminalFunc   func(*genv1.OnRunScopeTerminalRequest) (*genv1.LifecycleAck, error)
}

func (f *lifecycleFakeServer) OnTemplateDeployed(_ context.Context, req *genv1.OnTemplateDeployedRequest) (*genv1.LifecycleAck, error) {
	if f.OnTemplateDeployedFunc != nil {
		return f.OnTemplateDeployedFunc(req)
	}
	return &genv1.LifecycleAck{}, nil
}

func (f *lifecycleFakeServer) OnInstanceTerminated(_ context.Context, req *genv1.OnInstanceTerminatedRequest) (*genv1.LifecycleAck, error) {
	if f.OnInstanceTerminatedFunc != nil {
		return f.OnInstanceTerminatedFunc(req)
	}
	return &genv1.LifecycleAck{}, nil
}

func (f *lifecycleFakeServer) OnInstanceCreated(_ context.Context, req *genv1.OnInstanceCreatedRequest) (*genv1.LifecycleAck, error) {
	if f.OnInstanceCreatedFunc != nil {
		return f.OnInstanceCreatedFunc(req)
	}
	return &genv1.LifecycleAck{}, nil
}

func (f *lifecycleFakeServer) OnRunScopeTerminal(_ context.Context, req *genv1.OnRunScopeTerminalRequest) (*genv1.LifecycleAck, error) {
	if f.OnRunScopeTerminalFunc != nil {
		return f.OnRunScopeTerminalFunc(req)
	}
	return &genv1.LifecycleAck{}, nil
}

func TestOpenBridge_RunScopeIDThreaded(t *testing.T) {
	var gotRunScopeID string
	srv := &fakeServer{
		OpenFunc: func(req *genv1.OpenRequest) (*genv1.OpenResponse, error) {
			gotRunScopeID = req.GetRunScopeId()
			return &genv1.OpenResponse{
				Result: &genv1.OpenResponse_Unavailable{Unavailable: &genv1.Unavailable{}},
			}, nil
		},
	}
	ts := mountFake(t, srv)

	body, err := json.Marshal(map[string]any{
		"claim_id":      "00000000-0000-0000-0000-000000000001",
		"producer_name": "fake",
		"selector":      "items/x",
		"intent":        "rw",
		"run_scope_id":  "rs-1",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	resp, err := http.Post(ts.URL+"/v1/open", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /v1/open: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, raw)
	}
	if gotRunScopeID != "rs-1" {
		t.Fatalf("run_scope_id mismatch: got %q, want \"rs-1\" (claim_producer.proto field 8 must survive the HTTP bridge)", gotRunScopeID)
	}
}

func TestOpenBridge_LifetimeThreaded(t *testing.T) {
	var gotLifetime string
	srv := &fakeServer{
		OpenFunc: func(req *genv1.OpenRequest) (*genv1.OpenResponse, error) {
			gotLifetime = req.GetLifetime()
			return &genv1.OpenResponse{
				Result: &genv1.OpenResponse_Unavailable{Unavailable: &genv1.Unavailable{}},
			}, nil
		},
	}
	ts := mountFake(t, srv)

	body, err := json.Marshal(map[string]any{
		"claim_id":      "00000000-0000-0000-0000-000000000002",
		"producer_name": "fake",
		"selector":      "items/x",
		"intent":        "rw",
		"lifetime":      "durable",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	resp, err := http.Post(ts.URL+"/v1/open", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /v1/open: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, raw)
	}
	if gotLifetime != "durable" {
		t.Fatalf("lifetime mismatch: got %q, want \"durable\" (claim_producer.proto field 9 must survive the HTTP bridge)", gotLifetime)
	}
}

func TestLifecycleBridge_InstanceCreatedCarriesServiceBindingsAndOwnerAPIKeyID(t *testing.T) {
	var gotBindings []byte
	var gotOwnerKeyID string
	srv := &lifecycleFakeServer{
		OnInstanceCreatedFunc: func(req *genv1.OnInstanceCreatedRequest) (*genv1.LifecycleAck, error) {
			gotBindings = req.GetServiceBindings()
			gotOwnerKeyID = req.GetOwnerApiKeyId()
			return &genv1.LifecycleAck{}, nil
		},
	}
	mux := http.NewServeMux()
	MountLifecycle(mux, srv)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	body, err := json.Marshal(map[string]any{
		"template_hash":    "sha256-abc",
		"instance_id":      "00000000-0000-0000-0000-000000000abc",
		"service_bindings": []byte(`{"svc":"127.0.0.1:9090"}`),
		"owner_api_key_id": "key-1",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	resp, err := http.Post(ts.URL+"/v1/on_instance_created", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, raw)
	}
	if string(gotBindings) != `{"svc":"127.0.0.1:9090"}` {
		t.Fatalf("service_bindings mismatch: got %s (lifecycle.proto field 5 must survive the HTTP bridge)", gotBindings)
	}
	if gotOwnerKeyID != "key-1" {
		t.Fatalf("owner_api_key_id mismatch: got %q (lifecycle.proto field 6 must survive the HTTP bridge)", gotOwnerKeyID)
	}
}

func TestLifecycleBridge_OnRunScopeTerminalMounted(t *testing.T) {
	var gotRunScopeID, gotReason, gotInstanceID string
	srv := &lifecycleFakeServer{
		OnRunScopeTerminalFunc: func(req *genv1.OnRunScopeTerminalRequest) (*genv1.LifecycleAck, error) {
			gotRunScopeID = req.GetRunScopeId()
			gotReason = req.GetTerminalReason()
			gotInstanceID = req.GetInstanceId()
			return &genv1.LifecycleAck{}, nil
		},
	}
	mux := http.NewServeMux()
	MountLifecycle(mux, srv)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	body := []byte(`{"run_scope_id":"rs-1","terminal_reason":"completed","instance_id":"00000000-0000-0000-0000-000000000abc"}`)
	resp, err := http.Post(ts.URL+"/v1/on_run_scope_terminal", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /v1/on_run_scope_terminal: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s (lifecycle.proto's 7th RPC must be mounted on the HTTP bridge)", resp.StatusCode, raw)
	}
	if gotRunScopeID != "rs-1" {
		t.Fatalf("run_scope_id mismatch: got %q", gotRunScopeID)
	}
	if gotReason != "completed" {
		t.Fatalf("terminal_reason mismatch: got %q", gotReason)
	}
	if gotInstanceID != "00000000-0000-0000-0000-000000000abc" {
		t.Fatalf("instance_id mismatch: got %q", gotInstanceID)
	}
}
