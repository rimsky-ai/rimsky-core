// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

package serverkit

import (
	"context"
	"encoding/json"
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
		var req openBody
		if err := decodeOptional(body, &req); err != nil {
			return nil, fmt.Errorf("%w: %s", errBadRequest, err.Error())
		}
		if req.Intent != "r" && req.Intent != "rw" {
			return nil, fmt.Errorf("%w: intent must be \"r\" or \"rw\", got %q", errBadRequest, req.Intent)
		}
		return srv.Open(ctx, &genv1.OpenRequest{
			ClaimId:      req.ClaimID,
			ProducerName: req.ProducerName,
			Selector:     req.Selector,
			Intent:       req.Intent,
			Alias:        req.Alias,
			TemplateId:   req.TemplateID,
			InstanceId:   req.InstanceID,
			RunScopeId:   req.RunScopeID,
			Lifetime:     req.Lifetime,
		})
	case "commit":
		var req actionBody
		if err := decodeOptional(body, &req); err != nil {
			return nil, fmt.Errorf("%w: %s", errBadRequest, err.Error())
		}
		return srv.Commit(ctx, &genv1.CommitRequest{
			ClaimId: req.ClaimID, ClaimScope: req.Scope, Address: req.Address, LeaseToken: req.LeaseToken,
		})
	case "abandon":
		var req actionBody
		if err := decodeOptional(body, &req); err != nil {
			return nil, fmt.Errorf("%w: %s", errBadRequest, err.Error())
		}
		return srv.Abandon(ctx, &genv1.AbandonRequest{
			ClaimId: req.ClaimID, ClaimScope: req.Scope, Address: req.Address, LeaseToken: req.LeaseToken,
		})
	case "release":
		var req actionBody
		if err := decodeOptional(body, &req); err != nil {
			return nil, fmt.Errorf("%w: %s", errBadRequest, err.Error())
		}
		return srv.Release(ctx, &genv1.ReleaseRequest{
			ClaimId: req.ClaimID, ClaimScope: req.Scope, Address: req.Address, LeaseToken: req.LeaseToken,
		})
	case "split_scope":
		var req splitScopeBody
		if err := decodeOptional(body, &req); err != nil {
			return nil, fmt.Errorf("%w: %s", errBadRequest, err.Error())
		}
		return srv.SplitScope(ctx, &genv1.SplitScopeRequest{
			ClaimHandleId:    req.ClaimHandleID,
			PartitionRequest: req.PartitionRequest,
		})
	case "scopes_conflict":
		var req scopesConflictBody
		if err := decodeOptional(body, &req); err != nil {
			return nil, fmt.Errorf("%w: %s", errBadRequest, err.Error())
		}
		return srv.ScopesConflict(ctx, &genv1.ClaimScopesConflictRequest{
			ClaimScopeA: req.ClaimScopeA,
			ClaimScopeB: req.ClaimScopeB,
		})
	}
	return nil, errUnknownVerb
}

func dispatchLifecycle(ctx context.Context, srv genv1.LifecycleSubscriberServer, verb string, body []byte) (proto.Message, error) {
	switch verb {
	case "on_template_registered":
		var req templateScopeBody
		if err := decodeOptional(body, &req); err != nil {
			return nil, fmt.Errorf("%w: %s", errBadRequest, err.Error())
		}
		return srv.OnTemplateRegistered(ctx, &genv1.OnTemplateRegisteredRequest{
			TemplateHash: req.TemplateHash,
			Spec:         req.Spec,
		})
	case "on_template_deployed":
		var req templateScopeBody
		if err := decodeOptional(body, &req); err != nil {
			return nil, fmt.Errorf("%w: %s", errBadRequest, err.Error())
		}
		return srv.OnTemplateDeployed(ctx, &genv1.OnTemplateDeployedRequest{
			TemplateHash: req.TemplateHash,
			Tags:         req.Tags,
		})
	case "on_template_undeployed":
		var req templateScopeBody
		if err := decodeOptional(body, &req); err != nil {
			return nil, fmt.Errorf("%w: %s", errBadRequest, err.Error())
		}
		return srv.OnTemplateUndeployed(ctx, &genv1.OnTemplateUndeployedRequest{TemplateHash: req.TemplateHash})
	case "on_template_deregistered":
		var req templateScopeBody
		if err := decodeOptional(body, &req); err != nil {
			return nil, fmt.Errorf("%w: %s", errBadRequest, err.Error())
		}
		return srv.OnTemplateDeregistered(ctx, &genv1.OnTemplateDeregisteredRequest{TemplateHash: req.TemplateHash})
	case "on_instance_created":
		var req instanceScopeBody
		if err := decodeOptional(body, &req); err != nil {
			return nil, fmt.Errorf("%w: %s", errBadRequest, err.Error())
		}
		return srv.OnInstanceCreated(ctx, &genv1.OnInstanceCreatedRequest{
			InstanceId:            req.InstanceID,
			TemplateHash:          req.TemplateHash,
			InstanceKey:           req.InstanceKey,
			Params:                req.Params,
			ServiceBindings:       req.ServiceBindings,
			OwnerApiKeyId:         req.OwnerAPIKeyID,
			TargetRoutingIdentity: req.TargetRoutingIdentity,
		})
	case "on_instance_terminated":
		var req instanceScopeBody
		if err := decodeOptional(body, &req); err != nil {
			return nil, fmt.Errorf("%w: %s", errBadRequest, err.Error())
		}
		return srv.OnInstanceTerminated(ctx, &genv1.OnInstanceTerminatedRequest{
			InstanceId:         req.InstanceID,
			TemplateHash:       req.TemplateHash,
			TerminatedAtUnixMs: req.TerminatedAtUnixMs,
		})
	case "on_run_scope_terminal":
		var req runScopeTerminalBody
		if err := decodeOptional(body, &req); err != nil {
			return nil, fmt.Errorf("%w: %s", errBadRequest, err.Error())
		}
		return srv.OnRunScopeTerminal(ctx, &genv1.OnRunScopeTerminalRequest{
			RunScopeId:     req.RunScopeID,
			TerminalReason: req.TerminalReason,
			InstanceId:     req.InstanceID,
		})
	}
	return nil, errUnknownVerb
}

type openBody struct {
	ClaimID      string `json:"claim_id"`
	ProducerName string `json:"producer_name"`
	Selector     string `json:"selector"`
	Intent       string `json:"intent"`
	Alias        string `json:"alias"`
	TemplateID   string `json:"template_id"`
	InstanceID   string `json:"instance_id"`
	RunScopeID   string `json:"run_scope_id,omitempty"`
	Lifetime     string `json:"lifetime,omitempty"`
}

type actionBody struct {
	ClaimID    string `json:"claim_id"`
	Scope      []byte `json:"scope"`
	Address    []byte `json:"address"`
	LeaseToken string `json:"lease_token,omitempty"`
}

type splitScopeBody struct {
	ClaimHandleID    string `json:"claim_handle_id"`
	PartitionRequest []byte `json:"partition_request"`
}

type scopesConflictBody struct {
	ClaimScopeA []byte `json:"claim_scope_a"`
	ClaimScopeB []byte `json:"claim_scope_b"`
}

type templateScopeBody struct {
	TemplateHash string   `json:"template_hash"`
	Spec         []byte   `json:"spec,omitempty"`
	Tags         []string `json:"tags,omitempty"`
}

type instanceScopeBody struct {
	TemplateHash          string `json:"template_hash"`
	InstanceID            string `json:"instance_id"`
	InstanceKey           string `json:"instance_key,omitempty"`
	Params                []byte `json:"params,omitempty"`
	TerminatedAtUnixMs    int64  `json:"terminated_at_unix_ms,omitempty"`
	ServiceBindings       []byte `json:"service_bindings,omitempty"`
	OwnerAPIKeyID         string `json:"owner_api_key_id,omitempty"`
	TargetRoutingIdentity string `json:"target_routing_identity,omitempty"`
}

type runScopeTerminalBody struct {
	RunScopeID     string `json:"run_scope_id"`
	TerminalReason string `json:"terminal_reason,omitempty"`
	InstanceID     string `json:"instance_id,omitempty"`
}

func decodeOptional(body []byte, v any) error {
	if len(strings.TrimSpace(string(body))) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, v); err != nil {
		return fmt.Errorf("decode JSON body: %w", err)
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
