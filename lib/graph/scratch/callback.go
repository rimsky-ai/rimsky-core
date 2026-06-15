// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Incremental executor-scratch writeback callback handler — paralleling
// the §12.5 attributes incremental writeback. Mirrors the wire shape
// for symmetry:
//
//	POST {callback_url}/v1/runs/{run_id}/scratch
//	Authorization: <cancel_token>          (matches §12.4 / §12.5 auth)
//	Body: raw bytes (Content-Type: application/octet-stream) — the
//	      executor-attached opaque scratch payload, inert to rimsky.
//	→ 204 No Content
//
// The handler resolves the supervisor-issued cancel_token via the
// AuthLookup callback, then persists the bytes onto the dispatch row
// via the ScratchWriter dependency.

package scratch

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

// ScratchWriter is the narrow interface the HTTP handler depends on.
// The supervisor adapts the Queue.WriteScratchInTx surface to this
// shape (the persistence method takes an additional `tx`; the callback
// handler always runs outside any caller-owned tx). The supervisor
// adapter wraps the call in a short tx + decides inline-vs-spill the
// same way the stream-close terminal path does.
type ScratchWriter interface {
	Write(ctx context.Context, runID shared.UUID, bytes []byte) error
}

// AuthLookup resolves a supervisor-issued cancel_token to the run_id it
// authorises. Returns ErrUnauthorizedCallback when the token is unknown
// or doesn't match the URL-supplied run_id. The supervisor wires this
// callback to its in-memory dispatch registry; tests pass a closure.
type AuthLookup func(token string, runID shared.UUID) error

// ErrUnauthorizedCallback is the sentinel an AuthLookup returns when the
// cancel_token is missing, unknown, or scoped to a different run. The
// handler maps it to HTTP 401.
var ErrUnauthorizedCallback = errors.New("scratch: unauthorized callback")

// ErrRunRowMissing is the sentinel a ScratchWriter.Write implementation
// returns when the dispatch row addressed by the callback no longer
// exists (terminal-flipped to a phase outside the in-flight set, or
// garbage-collected by a retention sweep). The handler maps it to
// HTTP 410 Gone so the executor sees that its checkpoint was NOT
// persisted, rather than the silent-no-op 204 that the missing-row
// case used to return. STORY-opaque-executor-scratch's round-trip
// contract requires the persistence layer to surface a missing row,
// not absorb it silently.
var ErrRunRowMissing = errors.New("scratch: dispatch row not found")

// HandlerDeps bundles the callback handler's dependencies. The
// supervisor constructs one of these and registers Handler under
// `/v1/runs/{run_id}/scratch` on its callback router.
type HandlerDeps struct {
	Writer ScratchWriter
	Auth   AuthLookup
	Logger shared.Logger
}

// Handler returns the chi-compatible http.Handler. Intended to be
// mounted at `POST /v1/runs/{run_id}/scratch` so chi can supply the
// URL parameter via chi.URLParam.
//
// Auth is required at construction. Passing a HandlerDeps with a nil
// Auth panics: a nil-skip would silently disable callback auth and is
// trivially exploitable. Writer is likewise required at construction.
// Tests must supply stub closures.
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
		token := strings.TrimSpace(r.Header.Get("Authorization"))
		// Strip an optional `Bearer ` prefix; tolerated for executor
		// convenience, mirroring the attributes-callback handler.
		token = strings.TrimPrefix(token, "Bearer ")
		token = strings.TrimSpace(token)
		if token == "" {
			http.Error(w, `{"error":"missing authorization"}`, http.StatusUnauthorized)
			return
		}
		if err := deps.Auth(token, runID); err != nil {
			logger.Warn("scratch callback: unauthorized",
				"run_id", runID.String(), "error", err.Error())
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		// Bound the body read so a malicious / runaway executor cannot
		// exhaust supervisor memory by streaming gigabytes. The cap
		// mirrors the attribute-writeback body limit; spill threshold
		// policy lives in the ScratchWriter adapter.
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
