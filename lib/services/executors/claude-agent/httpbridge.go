// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package claudeagent

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/services/executors/internal/observability"
	"github.com/rimsky-ai/rimsky-core/lib/services/internal/peerauth"
)

const maxExecuteBodyBytes = 10 * 1024 * 1024

type httpExecuteBody struct {
	NodeID                   string         `json:"node_id"`
	InstanceID               string         `json:"instance_id"`
	NodeType                 string         `json:"node_type"`
	NodeRunID                string         `json:"dispatch_id"`
	Attributes               map[string]any `json:"attributes"`
	AttributesSchema         map[string]any `json:"attributes_schema"`
	ClaimProducers           map[string]any `json:"claim_producers"`
	CallbackURL              string         `json:"callback_url"`
	CancelToken              string         `json:"cancel_token"`
	PriorNodeRunID           string         `json:"prior_dispatch_id"`
	PriorDispatchDisposition string         `json:"prior_dispatch_disposition"`
	RunScopeID               string         `json:"run_scope_id"`
	Scratch                  string         `json:"scratch"`
}

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
		var body httpExecuteBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
			return
		}
		ackID := uuid.NewString()
		runID := body.NodeRunID
		if runID == "" {
			runID = uuid.NewString()
		}
		traceID := body.NodeRunID
		if traceID == "" {
			traceID = ackID
		}
		logger := executor.cfg.Logger.With("run_id", runID, "node_id", body.NodeID)

		attributes := body.Attributes
		if attributes == nil {
			attributes = map[string]any{}
		}
		inputs := dispatchInputs{
			NodeID:                       nodeIDOr(body.NodeID, runID),
			InstanceID:                   body.InstanceID,
			NodeType:                     nodeTypeOr(body.NodeType),
			NodeRunID:                    body.NodeRunID,
			RunScopeID:                   body.RunScopeID,
			PriorNodeRunID:               body.PriorNodeRunID,
			PriorDispatchDispositionWire: body.PriorDispatchDisposition,
			CallbackURL:                  body.CallbackURL,
			CancelToken:                  body.CancelToken,
			Attributes:                   attributes,
			AttributesSchema:             body.AttributesSchema,
			ClaimProducers:               unwrapClaimProducersJSON(body.ClaimProducers),
			SessionToken:                 sessionTokenOr(SessionTokenFromScratchBase64(body.Scratch), attributes),
		}

		executor.recordDispatchStart(traceID, inputs)

		go executor.runAndCallback(inputs, ackID, traceID, runID, logger,
			func(base string) string { return base },
			func(callbackBody map[string]any) { callbackBody["async_ack_id"] = ackID },
		)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{"async_ack_id": ackID})
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
