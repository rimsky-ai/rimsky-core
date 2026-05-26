// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// Package bridge mounts the HTTP+JSON bridge handlers per spec §5.2 onto
// an http.ServeMux. Every claim-producer-service exposes the same five
// runtime routes (`/v1/capabilities`, `/v1/open`, `/v1/commit`,
// `/v1/abandon`, `/v1/release`); rather than duplicating ~95% identical
// handlers across stores/{filesystem,postgres,stub}/server, each
// claim-producer-service calls Mount with its own
// genv1.ClaimProducerServer implementation.
//
// LifecycleSubscriber is exposed via a separate optional Mount call
// (MountLifecycle) when the binary opts into the lifecycle protocol.
//
// Per spec §15 (out of scope): the bridge currently surfaces every
// store error as HTTP 500 / gRPC Internal. Finer-grained
// status.Code mapping (NotFound, AlreadyExists, ResourceExhausted,
// FailedPrecondition, …) is deferred to a follow-up cycle.
package server

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

	genv1 "github.com/fallguyconsulting/rimsky/protocols/proto/v1/gen"
)

// errBadRequest tags decode errors so the handler can map them to 400
// while keeping all store errors at 500 (per the spec §15 deferral
// noted on the package comment).
var errBadRequest = errors.New("bridge: bad request")

// errUnknownVerb tags an unrecognised verb path; mapped to 404.
var errUnknownVerb = errors.New("bridge: unknown verb")

// Mount registers the ClaimProducer bridge routes on mux: the five
// runtime-verb routes (capabilities/open/commit/abandon/release).
func Mount(mux *http.ServeMux, srv genv1.ClaimProducerServer) {
	mux.HandleFunc("/v1/capabilities", producerHandler(srv, "capabilities"))
	mux.HandleFunc("/v1/open", producerHandler(srv, "open"))
	mux.HandleFunc("/v1/commit", producerHandler(srv, "commit"))
	mux.HandleFunc("/v1/abandon", producerHandler(srv, "abandon"))
	mux.HandleFunc("/v1/release", producerHandler(srv, "release"))
}

// MountLifecycle registers the LifecycleSubscriber bridge routes on mux.
// Optional: a binary that doesn't implement lifecycle simply doesn't
// call this function.
func MountLifecycle(mux *http.ServeMux, srv genv1.LifecycleSubscriberServer) {
	mux.HandleFunc("/v1/on_template_registered", lifecycleHandler(srv, "on_template_registered"))
	mux.HandleFunc("/v1/on_template_deployed", lifecycleHandler(srv, "on_template_deployed"))
	mux.HandleFunc("/v1/on_template_undeployed", lifecycleHandler(srv, "on_template_undeployed"))
	mux.HandleFunc("/v1/on_template_deregistered", lifecycleHandler(srv, "on_template_deregistered"))
	mux.HandleFunc("/v1/on_instance_created", lifecycleHandler(srv, "on_instance_created"))
	mux.HandleFunc("/v1/on_instance_terminated", lifecycleHandler(srv, "on_instance_terminated"))
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

// dispatchProducer reads the request body, decodes the verb-specific
// JSON payload, and forwards to the ClaimProducerServer.
func dispatchProducer(ctx context.Context, srv genv1.ClaimProducerServer, verb string, body []byte) (proto.Message, error) {
	switch verb {
	case "capabilities":
		return srv.Capabilities(ctx, &genv1.CapabilitiesRequest{})
	case "open":
		var req openBody
		if err := decodeOptional(body, &req); err != nil {
			return nil, fmt.Errorf("%w: %s", errBadRequest, err.Error())
		}
		// The proto types `intent` as a bare string; the wire schema
		// permits only "r" or "rw". Validate at the server-side bridge
		// so a malformed client cannot reach a producer-service's Open
		// implementation with an unrecognized value.
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
		})
	case "commit":
		var req actionBody
		if err := decodeOptional(body, &req); err != nil {
			return nil, fmt.Errorf("%w: %s", errBadRequest, err.Error())
		}
		return srv.Commit(ctx, &genv1.CommitRequest{
			ClaimId: req.ClaimID, ClaimScope: req.Scope, Address: req.Address,
		})
	case "abandon":
		var req actionBody
		if err := decodeOptional(body, &req); err != nil {
			return nil, fmt.Errorf("%w: %s", errBadRequest, err.Error())
		}
		return srv.Abandon(ctx, &genv1.AbandonRequest{
			ClaimId: req.ClaimID, ClaimScope: req.Scope, Address: req.Address,
		})
	case "release":
		var req actionBody
		if err := decodeOptional(body, &req); err != nil {
			return nil, fmt.Errorf("%w: %s", errBadRequest, err.Error())
		}
		return srv.Release(ctx, &genv1.ReleaseRequest{
			ClaimId: req.ClaimID, ClaimScope: req.Scope, Address: req.Address,
		})
	}
	return nil, errUnknownVerb
}

// dispatchLifecycle handles the six lifecycle event endpoints.
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
			InstanceId:   req.InstanceID,
			TemplateHash: req.TemplateHash,
			InstanceKey:  req.InstanceKey,
			Params:       req.Params,
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
	}
	return nil, errUnknownVerb
}

// openBody is the JSON shape decoded from POST /v1/open.
type openBody struct {
	ClaimID      string `json:"claim_id"`
	ProducerName string `json:"producer_name"`
	Selector     string `json:"selector"`
	Intent       string `json:"intent"`
	Alias        string `json:"alias"`
	TemplateID   string `json:"template_id"`
	InstanceID   string `json:"instance_id"`
}

// actionBody is the JSON shape decoded from POST /v1/{commit,abandon,
// release}. Same fields for all three.
type actionBody struct {
	ClaimID string `json:"claim_id"`
	Scope   []byte `json:"scope"`
	Address []byte `json:"address"`
}

// templateScopeBody is the JSON shape decoded from the four template-
// scope lifecycle event endpoints. Optional Spec / Tags fields carry
// the per-event payload that the proto-side requests use; absent
// fields decode to zero values, which is treated as "no payload".
type templateScopeBody struct {
	TemplateHash string   `json:"template_hash"`
	Spec         []byte   `json:"spec,omitempty"` // populated for on_template_registered
	Tags         []string `json:"tags,omitempty"` // populated for on_template_deployed
}

// instanceScopeBody is the JSON shape decoded from the two instance-
// scope lifecycle event endpoints. Optional InstanceKey / Params /
// TerminatedAtUnixMs carry the per-event payload.
type instanceScopeBody struct {
	TemplateHash       string `json:"template_hash"`
	InstanceID         string `json:"instance_id"`
	InstanceKey        string `json:"instance_key,omitempty"`          // populated for on_instance_created
	Params             []byte `json:"params,omitempty"`                // populated for on_instance_created
	TerminatedAtUnixMs int64  `json:"terminated_at_unix_ms,omitempty"` // populated for on_instance_terminated
}

// decodeOptional accepts either an empty body or a JSON object.
func decodeOptional(body []byte, v any) error {
	if len(strings.TrimSpace(string(body))) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, v); err != nil {
		return fmt.Errorf("decode JSON body: %w", err)
	}
	return nil
}

// writeJSON serializes the response with protojson, the canonical
// proto3-JSON encoder. Required because OpenResponse uses a oneof that
// encoding/json does not produce in the proto3-JSON discriminator
// shape (`{"acquired": {...}}` / `{"unavailable": {}}`).
func writeJSON(w http.ResponseWriter, v proto.Message) {
	w.Header().Set("Content-Type", "application/json")
	data, err := protojson.Marshal(v)
	if err != nil {
		http.Error(w, "marshal: "+err.Error(), http.StatusInternalServerError)
		return
	}
	_, _ = w.Write(data)
}

// WriteSemanticsToProto maps the foundation-locks string constant to
// the proto enum value. Used by store servers to construct
// CapabilitiesResponse and Open's Acquired.realized_write_semantics.
// Returns WRITE_SEMANTICS_UNKNOWN for empty / unrecognized values; the
// caller is responsible for refusing to populate that field with the
// zero value.
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

// WriteSemanticsFromProto reverses WriteSemanticsToProto. Returns the
// empty string for the proto-default zero value.
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
