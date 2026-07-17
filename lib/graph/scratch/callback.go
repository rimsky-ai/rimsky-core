// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scratch

import (
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

type ScratchWriter interface {
	Write(ctx context.Context, runID shared.UUID, bytes []byte) error
}

type AuthLookup func(r *http.Request, runID shared.UUID) error

var ErrUnauthorizedCallback = errors.New("scratch: unauthorized callback")

var ErrRunRowMissing = errors.New("scratch: dispatch row not found")

type HandlerDeps struct {
	Writer ScratchWriter
	Auth   AuthLookup
	Logger shared.Logger
}

func Handler(deps HandlerDeps) http.Handler {
	if deps.Auth == nil {
		panic("scratch.Handler: deps.Auth is required (nil would silently disable callback auth)")
	}
	if deps.Writer == nil {
		panic("scratch.Handler: deps.Writer is required")
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
		if err := deps.Auth(r, runID); err != nil {
			logger.Warn("scratch callback: unauthorized",
				"run_id", runID.String(), "error", err.Error())
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		const maxBody = 64 * 1024 * 1024
		body, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
		if err != nil {
			http.Error(w, `{"error":"read_body"}`, http.StatusBadRequest)
			return
		}
		if len(body) > maxBody {
			http.Error(w, `{"error":"body_too_large"}`, http.StatusRequestEntityTooLarge)
			return
		}
		if err := deps.Writer.Write(r.Context(), runID, body); err != nil {
			if errors.Is(err, ErrRunRowMissing) {
				logger.Warn("scratch callback: dispatch row missing",
					"run_id", runID.String(), "error", err.Error())
				http.Error(w, `{"error":"run_not_found"}`, http.StatusGone)
				return
			}
			logger.Error("scratch callback: write failed",
				"run_id", runID.String(), "error", err.Error())
			http.Error(w, `{"error":"write_failed"}`, http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}
