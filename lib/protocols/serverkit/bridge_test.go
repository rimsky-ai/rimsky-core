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

	OpenFunc func(*genv1.OpenRequest) (*genv1.OpenResponse, error)
}

func (f *fakeServer) Open(_ context.Context, req *genv1.OpenRequest) (*genv1.OpenResponse, error) {
	if f.OpenFunc != nil {
		return f.OpenFunc(req)
	}
	return &genv1.OpenResponse{}, nil
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

type lifecycleFakeServer struct {
	genv1.UnimplementedLifecycleSubscriberServer

	OnTemplateDeployedFunc   func(*genv1.OnTemplateDeployedRequest) (*genv1.LifecycleAck, error)
	OnInstanceTerminatedFunc func(*genv1.OnInstanceTerminatedRequest) (*genv1.LifecycleAck, error)
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
