// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package claimproducer

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	protocol "github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/peer"
)

type fakeHandler struct {
	calls []string

	openOut protocol.OpenOutcome
	openErr error

	commitRes protocol.CommitResult
	commitErr error

	abandonErr error
	releaseErr error

	splitResp protocol.SplitClaimScopeResponse
	splitErr  error

	conflicts   bool
	conflictErr error
}

func (h *fakeHandler) Name() string { return "fake-handler" }

func (h *fakeHandler) Capabilities(context.Context) (protocol.Capabilities, error) {
	return protocol.Capabilities{}, fmt.Errorf("handler Capabilities must not be consulted by the in-proc client")
}

func (h *fakeHandler) Open(context.Context, protocol.ClaimID, protocol.ClaimSpec) (protocol.OpenOutcome, error) {
	h.calls = append(h.calls, "Open")
	return h.openOut, h.openErr
}

func (h *fakeHandler) Commit(context.Context, protocol.ClaimID, []byte, []byte, string) (protocol.CommitResult, error) {
	h.calls = append(h.calls, "Commit")
	return h.commitRes, h.commitErr
}

func (h *fakeHandler) Abandon(context.Context, protocol.ClaimID, []byte, []byte, string) error {
	h.calls = append(h.calls, "Abandon")
	return h.abandonErr
}

func (h *fakeHandler) Release(context.Context, protocol.ClaimID, []byte, []byte, string) error {
	h.calls = append(h.calls, "Release")
	return h.releaseErr
}

func (h *fakeHandler) SplitScope(context.Context, protocol.SplitClaimScopeRequest) (protocol.SplitClaimScopeResponse, error) {
	h.calls = append(h.calls, "SplitScope")
	return h.splitResp, h.splitErr
}

func (h *fakeHandler) ScopesConflict(context.Context, []byte, []byte) (bool, error) {
	h.calls = append(h.calls, "ScopesConflict")
	return h.conflicts, h.conflictErr
}

type classedErr struct {
	class string
	msg   string
}

func (e *classedErr) Error() string      { return e.msg }
func (e *classedErr) ErrorClass() string { return e.class }

func clientOver(h *fakeHandler, caps protocol.Capabilities) *Client {
	return &Client{name: "items", caps: caps, handler: h}
}

func availableOutcome(rws protocol.WriteSemantics) protocol.OpenOutcome {
	return protocol.OpenOutcome{
		Available: true,
		Result:    protocol.ClaimResult{RealizedWriteSemantics: rws},
	}
}

func TestOpenPassesThroughWithinEnvelope(t *testing.T) {
	h := &fakeHandler{openOut: availableOutcome(protocol.WriteSemanticsSync)}
	c := clientOver(h, coreCapabilities())
	out, err := c.Open(context.Background(), "claim-1", protocol.ClaimSpec{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !out.Available || out.Result.RealizedWriteSemantics != protocol.WriteSemanticsSync {
		t.Fatalf("Open outcome mangled: %+v", out)
	}
}

func TestOpenUnavailablePassesThrough(t *testing.T) {
	h := &fakeHandler{openOut: protocol.OpenOutcome{Available: false, UnavailableClass: "fs/root_unavailable"}}
	c := clientOver(h, coreCapabilities())
	out, err := c.Open(context.Background(), "claim-1", protocol.ClaimSpec{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if out.Available || out.UnavailableClass != "fs/root_unavailable" {
		t.Fatalf("Open unavailable outcome mangled: %+v", out)
	}
}

func TestOpenRejectsUnknownRealizedWriteSemantics(t *testing.T) {
	h := &fakeHandler{openOut: availableOutcome(protocol.WriteSemanticsUnknown)}
	c := clientOver(h, coreCapabilities())
	_, err := c.Open(context.Background(), "claim-1", protocol.ClaimSpec{})
	if err == nil || !strings.Contains(err.Error(), "realized_write_semantics is UNKNOWN") {
		t.Fatalf("want UNKNOWN realized-write-semantics error, got %v", err)
	}
}

func TestOpenRejectsRealizedOutsideEnvelope(t *testing.T) {
	h := &fakeHandler{openOut: availableOutcome(protocol.WriteSemanticsReadOnly)}
	c := clientOver(h, coreCapabilities())
	_, err := c.Open(context.Background(), "claim-1", protocol.ClaimSpec{})
	if err == nil || !strings.Contains(err.Error(), "not in advertised envelope") {
		t.Fatalf("want envelope error, got %v", err)
	}
}

func TestHandlerErrorsWrapAsProducerCallError(t *testing.T) {
	underlying := fmt.Errorf("wrapped: %w", &classedErr{class: "fs/root_unavailable", msg: "root gone"})
	h := &fakeHandler{
		openErr:     underlying,
		commitErr:   underlying,
		abandonErr:  underlying,
		releaseErr:  underlying,
		splitErr:    underlying,
		conflictErr: underlying,
	}
	caps := coreCapabilities()
	caps.SupportsSplitScope = true
	caps.SupportsScopesConflict = true
	c := clientOver(h, caps)

	ctx := context.Background()
	verbs := []struct {
		method string
		call   func() error
	}{
		{"Open", func() error { _, err := c.Open(ctx, "claim-1", protocol.ClaimSpec{}); return err }},
		{"Commit", func() error { _, err := c.Commit(ctx, "claim-1", nil, nil, ""); return err }},
		{"Abandon", func() error { return c.Abandon(ctx, "claim-1", nil, nil, "") }},
		{"Release", func() error { return c.Release(ctx, "claim-1", nil, nil, "") }},
		{"SplitScope", func() error { _, err := c.SplitScope(ctx, protocol.SplitClaimScopeRequest{}); return err }},
		{"ScopesConflict", func() error { _, err := c.ScopesConflict(ctx, []byte("a"), []byte("b")); return err }},
	}
	for _, v := range verbs {
		t.Run(v.method, func(t *testing.T) {
			err := v.call()
			var pcErr *peer.ProducerCallError
			if !errors.As(err, &pcErr) {
				t.Fatalf("want *peer.ProducerCallError, got %T: %v", err, err)
			}
			if pcErr.ProducerName != "items" || pcErr.Method != v.method {
				t.Fatalf("ProducerCallError fields: got (%q, %q), want (items, %s)", pcErr.ProducerName, pcErr.Method, v.method)
			}
			if pcErr.ErrorClass != "fs/root_unavailable" {
				t.Fatalf("ErrorClass: got %q, want fs/root_unavailable", pcErr.ErrorClass)
			}
			if !errors.Is(err, underlying) {
				t.Fatal("wrapped error lost the underlying chain")
			}
			if !strings.Contains(err.Error(), `producer "items"`) {
				t.Fatalf("error text: got %q", err.Error())
			}
		})
	}
}

func TestHandlerErrorWithoutClassYieldsEmptyErrorClass(t *testing.T) {
	h := &fakeHandler{openErr: errors.New("plain failure")}
	c := clientOver(h, coreCapabilities())
	_, err := c.Open(context.Background(), "claim-1", protocol.ClaimSpec{})
	var pcErr *peer.ProducerCallError
	if !errors.As(err, &pcErr) {
		t.Fatalf("want *peer.ProducerCallError, got %T: %v", err, err)
	}
	if pcErr.ErrorClass != "" {
		t.Fatalf("ErrorClass: got %q, want empty", pcErr.ErrorClass)
	}
}

func TestHandlerStatusErrorYieldsErrorClassFromDetails(t *testing.T) {
	st := status.New(codes.Internal, "root gone")
	withInfo, err := st.WithDetails(&errdetails.ErrorInfo{
		Reason: "fs/root_unavailable",
		Domain: "rimsky.store-filesystem",
	})
	if err != nil {
		t.Fatalf("WithDetails: %v", err)
	}
	h := &fakeHandler{openErr: withInfo.Err()}
	c := clientOver(h, coreCapabilities())
	_, callErr := c.Open(context.Background(), "claim-1", protocol.ClaimSpec{})
	var pcErr *peer.ProducerCallError
	if !errors.As(callErr, &pcErr) {
		t.Fatalf("want *peer.ProducerCallError, got %T: %v", callErr, callErr)
	}
	if pcErr.ErrorClass != "fs/root_unavailable" {
		t.Fatalf("ErrorClass: got %q, want fs/root_unavailable (status-details channel)", pcErr.ErrorClass)
	}
}

func TestSplitScopeGatedOnCapability(t *testing.T) {
	h := &fakeHandler{}
	c := clientOver(h, coreCapabilities())
	_, err := c.SplitScope(context.Background(), protocol.SplitClaimScopeRequest{})
	if !errors.Is(err, protocol.ErrSplitScopeUnsupported) {
		t.Fatalf("want ErrSplitScopeUnsupported, got %v", err)
	}
	if len(h.calls) != 0 {
		t.Fatalf("handler called despite unsupported capability: %v", h.calls)
	}

	caps := coreCapabilities()
	caps.SupportsSplitScope = true
	h2 := &fakeHandler{splitResp: protocol.SplitClaimScopeResponse{
		SubClaimScopes: []protocol.SubClaimScopeDescriptor{{PartitionKey: "p1"}},
	}}
	resp, err := clientOver(h2, caps).SplitScope(context.Background(), protocol.SplitClaimScopeRequest{})
	if err != nil {
		t.Fatalf("SplitScope: %v", err)
	}
	if len(resp.SubClaimScopes) != 1 || resp.SubClaimScopes[0].PartitionKey != "p1" {
		t.Fatalf("SplitScope response mangled: %+v", resp)
	}
}

func TestScopesConflictGatedOnCapability(t *testing.T) {
	h := &fakeHandler{}
	c := clientOver(h, coreCapabilities())

	equal, err := c.ScopesConflict(context.Background(), []byte("same"), []byte("same"))
	if err != nil || !equal {
		t.Fatalf("fallback on equal scopes: got (%v, %v), want (true, nil)", equal, err)
	}
	differ, err := c.ScopesConflict(context.Background(), []byte("a"), []byte("b"))
	if err != nil || differ {
		t.Fatalf("fallback on differing scopes: got (%v, %v), want (false, nil)", differ, err)
	}
	if len(h.calls) != 0 {
		t.Fatalf("handler called despite unsupported capability: %v", h.calls)
	}

	caps := coreCapabilities()
	caps.SupportsScopesConflict = true
	h2 := &fakeHandler{conflicts: true}
	conflicts, err := clientOver(h2, caps).ScopesConflict(context.Background(), []byte("a"), []byte("b"))
	if err != nil || !conflicts {
		t.Fatalf("delegated ScopesConflict: got (%v, %v), want (true, nil)", conflicts, err)
	}
}

func TestCommitDelegates(t *testing.T) {
	h := &fakeHandler{commitRes: protocol.CommitResult{VersionID: "v1", ProducerMetadata: []byte("meta")}}
	c := clientOver(h, coreCapabilities())
	res, err := c.Commit(context.Background(), "claim-1", []byte("scope"), []byte("addr"), "")
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if res.VersionID != "v1" || string(res.ProducerMetadata) != "meta" {
		t.Fatalf("Commit result mangled: %+v", res)
	}
	if err := c.Abandon(context.Background(), "claim-1", nil, nil, ""); err != nil {
		t.Fatalf("Abandon: %v", err)
	}
	if err := c.Release(context.Background(), "claim-1", nil, nil, ""); err != nil {
		t.Fatalf("Release: %v", err)
	}
}
