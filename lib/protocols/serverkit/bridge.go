// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

package serverkit

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

var errBadRequest = errors.New("bridge: bad request")

var errUnknownVerb = errors.New("bridge: unknown verb")

func Mount(mux *http.ServeMux, srv genv1.ClaimProducerServer) {
	mux.HandleFunc("/v1/capabilities", producerHandler(srv, "capabilities"))
	mux.HandleFunc("/v1/open", producerHandler(srv, "open"))
	mux.HandleFunc("/v1/commit", producerHandler(srv, "commit"))
	mux.HandleFunc("/v1/abandon", producerHandler(srv, "abandon"))
	mux.HandleFunc("/v1/release", producerHandler(srv, "release"))
	mux.HandleFunc("/v1/split_scope", producerHandler(srv, "split_scope"))
	mux.HandleFunc("/v1/scopes_conflict", producerHandler(srv, "scopes_conflict"))
}

func MountLifecycle(mux *http.ServeMux, srv genv1.LifecycleSubscriberServer) {
	mux.HandleFunc("/v1/on_template_registered", lifecycleHandler(srv, "on_template_registered"))
	mux.HandleFunc("/v1/on_template_deployed", lifecycleHandler(srv, "on_template_deployed"))
	mux.HandleFunc("/v1/on_template_undeployed", lifecycleHandler(srv, "on_template_undeployed"))
	mux.HandleFunc("/v1/on_template_deregistered", lifecycleHandler(srv, "on_template_deregistered"))
	mux.HandleFunc("/v1/on_instance_created", lifecycleHandler(srv, "on_instance_created"))
	mux.HandleFunc("/v1/on_instance_terminated", lifecycleHandler(srv, "on_instance_terminated"))
	mux.HandleFunc("/v1/on_run_scope_terminal", lifecycleHandler(srv, "on_run_scope_terminal"))
}

func producerHandler(srv genv1.ClaimProducerServer, verb string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
			return
		}
		respObj, callErr := dispatchProducer(r.Context(), srv, verb, body)
		writeOrError(w, respObj, callErr)
	}
}

func lifecycleHandler(srv genv1.LifecycleSubscriberServer, verb string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
			return
		}
		respObj, callErr := dispatchLifecycle(r.Context(), srv, verb, body)
		writeOrError(w, respObj, callErr)
	}
}

func writeOrError(w http.ResponseWriter, respObj proto.Message, callErr error) {
	switch {
	case errors.Is(callErr, errBadRequest):
		http.Error(w, callErr.Error(), http.StatusBadRequest)
		return
	case errors.Is(callErr, errUnknownVerb):
		http.Error(w, callErr.Error(), http.StatusNotFound)
		return
	case callErr != nil:
		http.Error(w, callErr.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, respObj)
}

func dispatchProducer(ctx context.Context, srv genv1.ClaimProducerServer, verb string, body []byte) (proto.Message, error) {
	switch verb {
	case "capabilities":
		return srv.Capabilities(ctx, &genv1.CapabilitiesRequest{})
	case "open":
		req := &genv1.OpenRequest{}
		if err := decodeRequest(body, req); err != nil {
			return nil, err
		}
		if req.GetIntent() != "r" && req.GetIntent() != "rw" {
			return nil, fmt.Errorf("%w: intent must be \"r\" or \"rw\", got %q", errBadRequest, req.GetIntent())
		}
		return srv.Open(ctx, req)
	case "commit":
		req := &genv1.CommitRequest{}
		if err := decodeRequest(body, req); err != nil {
			return nil, err
		}
		return srv.Commit(ctx, req)
	case "abandon":
		req := &genv1.AbandonRequest{}
		if err := decodeRequest(body, req); err != nil {
			return nil, err
		}
		return srv.Abandon(ctx, req)
	case "release":
		req := &genv1.ReleaseRequest{}
		if err := decodeRequest(body, req); err != nil {
			return nil, err
		}
		return srv.Release(ctx, req)
	case "split_scope":
		req := &genv1.SplitScopeRequest{}
		if err := decodeRequest(body, req); err != nil {
			return nil, err
		}
		return srv.SplitScope(ctx, req)
	case "scopes_conflict":
		req := &genv1.ClaimScopesConflictRequest{}
		if err := decodeRequest(body, req); err != nil {
			return nil, err
		}
		return srv.ScopesConflict(ctx, req)
	}
	return nil, errUnknownVerb
}

func dispatchLifecycle(ctx context.Context, srv genv1.LifecycleSubscriberServer, verb string, body []byte) (proto.Message, error) {
	switch verb {
	case "on_template_registered":
		req := &genv1.OnTemplateRegisteredRequest{}
		if err := decodeRequest(body, req); err != nil {
			return nil, err
		}
		return srv.OnTemplateRegistered(ctx, req)
	case "on_template_deployed":
		req := &genv1.OnTemplateDeployedRequest{}
		if err := decodeRequest(body, req); err != nil {
			return nil, err
		}
		return srv.OnTemplateDeployed(ctx, req)
	case "on_template_undeployed":
		req := &genv1.OnTemplateUndeployedRequest{}
		if err := decodeRequest(body, req); err != nil {
			return nil, err
		}
		return srv.OnTemplateUndeployed(ctx, req)
	case "on_template_deregistered":
		req := &genv1.OnTemplateDeregisteredRequest{}
		if err := decodeRequest(body, req); err != nil {
			return nil, err
		}
		return srv.OnTemplateDeregistered(ctx, req)
	case "on_instance_created":
		req := &genv1.OnInstanceCreatedRequest{}
		if err := decodeRequest(body, req); err != nil {
			return nil, err
		}
		return srv.OnInstanceCreated(ctx, req)
	case "on_instance_terminated":
		req := &genv1.OnInstanceTerminatedRequest{}
		if err := decodeRequest(body, req); err != nil {
			return nil, err
		}
		return srv.OnInstanceTerminated(ctx, req)
	case "on_run_scope_terminal":
		req := &genv1.OnRunScopeTerminalRequest{}
		if err := decodeRequest(body, req); err != nil {
			return nil, err
		}
		return srv.OnRunScopeTerminal(ctx, req)
	}
	return nil, errUnknownVerb
}

// @decision: protojson-gateway
var requestUnmarshal = protojson.UnmarshalOptions{DiscardUnknown: true}

// @decision: protojson-gateway
func decodeRequest(body []byte, msg proto.Message) error {
	if len(strings.TrimSpace(string(body))) == 0 {
		return nil
	}
	if err := requestUnmarshal.Unmarshal(body, msg); err != nil {
		return fmt.Errorf("%w: decode JSON body: %s", errBadRequest, err.Error())
	}
	return nil
}

// @decision: protojson-gateway
func writeJSON(w http.ResponseWriter, v proto.Message) {
	w.Header().Set("Content-Type", "application/json")
	data, err := protojson.Marshal(v)
	if err != nil {
		http.Error(w, "marshal: "+err.Error(), http.StatusInternalServerError)
		return
	}
	_, _ = w.Write(data)
}

func WriteSemanticsToProto(s string) genv1.WriteSemantics {
	switch s {
	case "sync":
		return genv1.WriteSemantics_WRITE_SEMANTICS_SYNC
	case "staged_async":
		return genv1.WriteSemantics_WRITE_SEMANTICS_STAGED_ASYNC
	case "blocking_async":
		return genv1.WriteSemantics_WRITE_SEMANTICS_BLOCKING_ASYNC
	case "read_only":
		return genv1.WriteSemantics_WRITE_SEMANTICS_READ_ONLY
	default:
		return genv1.WriteSemantics_WRITE_SEMANTICS_UNKNOWN
	}
}

func WriteSemanticsFromProto(ws genv1.WriteSemantics) string {
	switch ws {
	case genv1.WriteSemantics_WRITE_SEMANTICS_SYNC:
		return "sync"
	case genv1.WriteSemantics_WRITE_SEMANTICS_STAGED_ASYNC:
		return "staged_async"
	case genv1.WriteSemantics_WRITE_SEMANTICS_BLOCKING_ASYNC:
		return "blocking_async"
	case genv1.WriteSemantics_WRITE_SEMANTICS_READ_ONLY:
		return "read_only"
	default:
		return ""
	}
}
