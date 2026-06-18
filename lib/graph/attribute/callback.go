// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package attributes

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

type Row struct {
	RunID     shared.UUID
	NodeID    shared.UUID
	Data      map[string]any
	UpdatedAt time.Time
}

type NodeAttributeTable interface {
	GetByRun(ctx context.Context, runID shared.UUID) (*Row, error)
	Upsert(ctx context.Context, runID, nodeID shared.UUID, data map[string]any) error
	MergeDelta(ctx context.Context, runID shared.UUID, delta map[string]any) error
}

type AuthLookup func(token string, runID shared.UUID) error

var ErrUnauthorizedCallback = errors.New("attributes: unauthorized callback")

type HandlerDeps struct {
	Store  NodeAttributeTable
	Auth   AuthLookup
	Logger shared.Logger
}

func Handler(deps HandlerDeps) http.Handler {
	if deps.Auth == nil {
		panic("attributes.Handler: deps.Auth is required (nil would silently disable callback auth)")
	}
	if deps.Store == nil {
		panic("attributes.Handler: deps.Store is required")
	}
	logger := deps.Logger
	if logger == nil {
		logger = shared.SilentLogger{}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		runIDStr := chi.URLParam(r, "run_id")
		runID, err := uuid.Parse(runIDStr)
		if err != nil {
			http.Error(w, `{"error":"invalid run_id"}`, http.StatusBadRequest)
			return
		}
		token := strings.TrimSpace(r.Header.Get("Authorization"))
		token = strings.TrimPrefix(token, "Bearer ")
		token = strings.TrimSpace(token)
		if token == "" {
			http.Error(w, `{"error":"missing authorization"}`, http.StatusUnauthorized)
			return
		}
		if err := deps.Auth(token, runID); err != nil {
			logger.Warn("attributes callback: unauthorized",
				"run_id", runID.String(), "error", err.Error())
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		var body struct {
			Delta map[string]any `json:"delta"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
			return
		}
		if body.Delta == nil {
			body.Delta = map[string]any{}
		}
		if err := deps.Store.MergeDelta(r.Context(), runID, body.Delta); err != nil {
			logger.Error("attributes callback: merge failed",
				"run_id", runID.String(), "error", err.Error())
			http.Error(w, `{"error":"merge_failed"}`, http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}
