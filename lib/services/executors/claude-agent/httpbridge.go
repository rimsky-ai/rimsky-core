// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package claudeagent

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/peerauth"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/services/executors/internal/observability"
)

const maxExecuteBodyBytes = 10 * 1024 * 1024

// @decision: protojson-gateway
var executeRequestUnmarshal = protojson.UnmarshalOptions{DiscardUnknown: true}

type RunningHTTPBridge struct {
	Address string
	server  *http.Server
}

func (r *RunningHTTPBridge) Shutdown(ctx context.Context) error {
	return r.server.Shutdown(ctx)
}

// @decision: http-bridge-preserved
func StartHTTPBridge(host string, port int, executor *ExecutorServer, identity *peerauth.Identity) (*RunningHTTPBridge, error) {
	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})

	if executor.cfg.Observability != nil {
		obs := executor.cfg.Observability
		mux.HandleFunc("/observability/v1/capabilities", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			observability.WriteProtoJSON(w, obs.CapabilitiesPayload())
		})
		observability.MountTraceBridge(mux, obs.Store())
	}

	mux.HandleFunc("/execute", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxExecuteBodyBytes)
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
			return
		}
		req := &genv1.ExecuteRequest{}
		if err := executeRequestUnmarshal.Unmarshal(raw, req); err != nil {
			http.Error(w, "protojson: "+err.Error(), http.StatusBadRequest)
			return
		}
		ackID := uuid.NewString()
		runID := req.GetDispatchId()
		if runID == "" {
			runID = uuid.NewString()
		}
		traceID := req.GetDispatchId()
		if traceID == "" {
			traceID = ackID
		}
		logger := executor.cfg.Logger.With("run_id", runID, "node_id", req.GetNodeId())

		attributes := req.GetAttributes().AsMap()
		if attributes == nil {
			attributes = map[string]any{}
		}
		inputs := dispatchInputs{
			NodeID:                       nodeIDOr(req.GetNodeId(), runID),
			InstanceID:                   req.GetInstanceId(),
			NodeType:                     nodeTypeOr(req.GetNodeType()),
			NodeRunID:                    req.GetDispatchId(),
			RunScopeID:                   req.GetRunScopeId(),
			PriorNodeRunID:               req.GetPriorDispatchId(),
			PriorDispatchDispositionWire: priorDispositionWire(req),
			CallbackURL:                  req.GetCallbackUrl(),
			CancelToken:                  req.GetCancelToken(),
			Attributes:                   attributes,
			AttributesSchema:             req.GetAttributesSchema().AsMap(),
			ClaimProducers:               unwrapClaimProducersProto(req.GetClaimProducers()),
			SessionToken:                 sessionTokenOr(SessionTokenFromScratch(req.GetScratch()), attributes),
		}

		executor.recordDispatchStart(traceID, inputs)

		go executor.runAndCallback(inputs, traceID, runID, logger,
			func(base string) string { return base },
			func(callbackBody map[string]any) { callbackBody["async_ack_id"] = ackID },
		)

		outcome := &genv1.Outcome{Outcome: &genv1.Outcome_AwaitAsync{AwaitAsync: &genv1.AwaitAsyncCallback{
			AsyncAckId: ackID,
		}}}
		encoded, mErr := protojson.Marshal(outcome)
		if mErr != nil {
			http.Error(w, "protojson: "+mErr.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write(encoded)
	})

	listener, err := net.Listen("tcp", fmt.Sprintf("%s:%d", host, port))
	if err != nil {
		return nil, err
	}
	server := identity.HTTPServer(mux)
	server.ReadHeaderTimeout = 30 * time.Second
	go func() {
		if serveErr := identity.RunHTTP(server, listener); serveErr != nil && serveErr != http.ErrServerClosed {
			executor.cfg.Logger.Error("http bridge serve", "error", serveErr.Error())
		}
	}()
	return &RunningHTTPBridge{
		Address: listener.Addr().String(),
		server:  server,
	}, nil
}
