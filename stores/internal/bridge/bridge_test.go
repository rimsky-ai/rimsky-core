// bridge_test.go covers the HTTP+JSON bridge response encoding.
//
// The OpenResponse proto uses a `oneof result` carrying either an
// `Acquired` or `Unavailable` arm. proto3-JSON encodes that as a
// discriminator key (`{"acquired": {...}}` / `{"unavailable": {}}`),
// which `encoding/json` does NOT produce when marshalling the same Go
// struct directly. The bridge therefore uses `protojson.Marshal` to
// serialize responses; these tests guard against a regression that
// swaps it back to `encoding/json`.

package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"

	genv1 "github.com/fallguy/rimsky/proto/v1/gen"
)

// fakeServer is a minimal genv1.StoreServiceServer the bridge can
// dispatch to. Each verb returns a canned response or an error from the
// corresponding `*Func` field, defaulting to an empty proto when unset.
type fakeServer struct {
	genv1.UnimplementedStoreServiceServer

	OpenFunc func(*genv1.OpenRequest) (*genv1.OpenResponse, error)
}

func (f *fakeServer) Open(_ context.Context, req *genv1.OpenRequest) (*genv1.OpenResponse, error) {
	if f.OpenFunc != nil {
		return f.OpenFunc(req)
	}
	return &genv1.OpenResponse{}, nil
}

// mountFake wires the bridge against fakeServer and returns an
// httptest.Server the test can POST to.
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
		"claim_id":   "00000000-0000-0000-0000-000000000001",
		"store_name": "fake",
		"selector":   "items/x",
		"intent":     "rw",
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

// TestOpenBridge_AcquiredOneof asserts the bridge returns the
// `acquired` arm of the OpenResponse oneof in the proto3-JSON
// discriminator shape and that protojson can decode it back into the
// same shape.
func TestOpenBridge_AcquiredOneof(t *testing.T) {
	addr := []byte(`{"path":"/items/x"}`)
	payload := []byte(`{"data":"hello"}`)
	region := []byte(`"items/x"`)
	srv := &fakeServer{
		OpenFunc: func(_ *genv1.OpenRequest) (*genv1.OpenResponse, error) {
			return &genv1.OpenResponse{
				Result: &genv1.OpenResponse_Acquired{Acquired: &genv1.Acquired{
					Address: addr,
					Payload: payload,
					Region:  region,
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
	if !bytes.Equal(acq.GetRegion(), region) {
		t.Errorf("region mismatch: got %q want %q", acq.GetRegion(), region)
	}
}

// TestOpenBridge_UnavailableOneof asserts the Unavailable arm encodes
// as a recognisable discriminator key that protojson can decode.
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

// TestOpenBridge_StdJSONCannotRecoverOneof guards against a regression
// that swaps `protojson.Marshal` back to `encoding/json.Marshal`.
// `encoding/json` produces the Go-struct shape (`{"Result": {...}}`)
// rather than the proto3-JSON discriminator shape, which protojson
// cannot decode. If this test starts failing on the protojson decode,
// the bridge has regressed.
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

	// protojson MUST recover the oneof.
	var viaProto genv1.OpenResponse
	if err := protojson.Unmarshal(raw, &viaProto); err != nil {
		t.Fatalf("protojson.Unmarshal: %v\nbody: %s", err, raw)
	}
	if viaProto.GetAcquired() == nil {
		t.Fatalf("protojson did not recover Acquired arm: %s", raw)
	}

	// stdlib encoding/json does NOT recover the oneof discriminator —
	// the field decodes as nil because Go's json reflection cannot map
	// the proto3-JSON shape onto the generated Result interface. This
	// asserts the bridge is using protojson for marshal; a swap-back
	// to encoding/json would produce a different on-the-wire shape and
	// the protojson decode above would fail before this point.
	var viaStd genv1.OpenResponse
	if err := json.Unmarshal(raw, &viaStd); err == nil {
		if viaStd.GetAcquired() != nil {
			t.Fatalf("encoding/json unexpectedly recovered the oneof — wire format regressed: %s", raw)
		}
	}
}

// TestLifecycleBridge_TemplateScopeRoundTrip verifies that a POST to
// /v1/on_template_deployed decodes the JSON body into the
// corresponding proto request and forwards to the server.
func TestLifecycleBridge_TemplateScopeRoundTrip(t *testing.T) {
	var seen string
	srv := &lifecycleFakeServer{
		OnTemplateDeployedFunc: func(req *genv1.OnTemplateDeployedRequest) (*genv1.OnTemplateDeployedResponse, error) {
			seen = req.GetTemplateId()
			return &genv1.OnTemplateDeployedResponse{}, nil
		},
	}
	mux := http.NewServeMux()
	Mount(mux, srv)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	body := []byte(`{"template_id":"sha256-abc"}`)
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
		t.Fatalf("template_id mismatch: got %q want sha256-abc", seen)
	}
}

// TestLifecycleBridge_InstanceScopeRoundTrip verifies that a POST to
// /v1/on_instance_terminated decodes both template_id and instance_id.
func TestLifecycleBridge_InstanceScopeRoundTrip(t *testing.T) {
	var gotTemplate, gotInstance string
	srv := &lifecycleFakeServer{
		OnInstanceTerminatedFunc: func(req *genv1.OnInstanceTerminatedRequest) (*genv1.OnInstanceTerminatedResponse, error) {
			gotTemplate = req.GetTemplateId()
			gotInstance = req.GetInstanceId()
			return &genv1.OnInstanceTerminatedResponse{}, nil
		},
	}
	mux := http.NewServeMux()
	Mount(mux, srv)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	body := []byte(`{"template_id":"sha256-xyz","instance_id":"00000000-0000-0000-0000-000000000abc"}`)
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
		t.Fatalf("template_id mismatch: got %q", gotTemplate)
	}
	if gotInstance != "00000000-0000-0000-0000-000000000abc" {
		t.Fatalf("instance_id mismatch: got %q", gotInstance)
	}
}

// lifecycleFakeServer extends the bridge's test fakeServer pattern with
// optional callbacks for the six lifecycle methods.
type lifecycleFakeServer struct {
	genv1.UnimplementedStoreServiceServer

	OnTemplateDeployedFunc   func(*genv1.OnTemplateDeployedRequest) (*genv1.OnTemplateDeployedResponse, error)
	OnInstanceTerminatedFunc func(*genv1.OnInstanceTerminatedRequest) (*genv1.OnInstanceTerminatedResponse, error)
}

func (f *lifecycleFakeServer) OnTemplateDeployed(_ context.Context, req *genv1.OnTemplateDeployedRequest) (*genv1.OnTemplateDeployedResponse, error) {
	if f.OnTemplateDeployedFunc != nil {
		return f.OnTemplateDeployedFunc(req)
	}
	return &genv1.OnTemplateDeployedResponse{}, nil
}

func (f *lifecycleFakeServer) OnInstanceTerminated(_ context.Context, req *genv1.OnInstanceTerminatedRequest) (*genv1.OnInstanceTerminatedResponse, error) {
	if f.OnInstanceTerminatedFunc != nil {
		return f.OnInstanceTerminatedFunc(req)
	}
	return &genv1.OnInstanceTerminatedResponse{}, nil
}
