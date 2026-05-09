// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Package controlapi implements the HTTP+JSON control API for rimsky
// orchestrators. Routes are registered in sibling files
// (templates.go, instances.go, nodes.go, events.go, claims.go,
// admin_force_fire.go, health.go). Errors thrown inside handlers are
// mapped to HTTP responses via setErrorHandler.
package controlapi

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"

	"github.com/fallguy/rimsky/foundation/integration"
	"github.com/fallguy/rimsky/foundation/locks"
	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/modeling/shared"
)

type AppDeps struct {
	// Persist is the unified persistence.Store handle (rimsky_* tables).
	// Required.
	Persist persistence.Store
	// Queue is the dispatch-queue accessor. Required.
	Queue  persistence.Queue
	Clock  shared.Clock
	Logger shared.Logger
	Auth   Authenticator // may be nil → anonymous access
	// Stores is the per-process *locks.Registry built from rimsky.yml.
	// Used by admin endpoints that target a specific named store and by
	// the template-deploy validator. May be nil at construction time;
	// admin handlers that need it return 503 when nil.
	Stores *locks.Registry
	// LifecycleSubs is the per-process *locks.LifecycleRegistry built
	// from rimsky.yml. Holds one entry per peer (claim_producer or
	// executor) whose protocols list includes "lifecycle_subscriber".
	// Lifecycle events fan out to every entry. May be nil — control-api
	// then logs but does not fan out.
	LifecycleSubs *locks.LifecycleRegistry
	// NamedLocks is the operator-side named-lock config (spec §6.1).
	// Consulted at template-deploy time to validate that every
	// template-referenced lock name is declared. Empty / missing → no
	// named locks declared (templates referencing any will fail
	// validation).
	NamedLocks locks.NamedLocksConfig
	// Executors is the operator-side executors block from rimsky.yml
	// (per docs/specs/2026-05-01-control-plane-and-store-lifecycle-
	// design.md §3.1). Consulted at template registration to validate
	// that every node-referenced executor name is declared. Empty / nil
	// → templates referencing any executor will fail validation.
	Executors map[string]ExecutorEntry
	// Observability is the optional read-only /v1/observability/* mount
	// hook. Setter is core/config.StartControlAPI; per spec §1.1 the
	// observability surface is wired in at startup alongside the
	// existing admin/operator routes. Nil → /v1/observability/* is not
	// mounted (used by tests that don't exercise observability).
	Observability ObservabilityRouter

	// ExecutorCapabilities optionally exposes the observability cache's
	// per-executor capability fields (declared_events, userdata_schema)
	// without forcing controlapi to import the observability package.
	// Wired by config.StartControlAPI when an observability handshake
	// has run. Returns ok=false when no capability cache is loaded for
	// the named executor (e.g. peer is unreachable). Plan F6 + F7.
	ExecutorCapabilities func(executorName string) (declaredEvents []string, userdataSchema []byte, ok bool)

	// DiagnosticReader is the operator-supplied accessor for parked-node
	// diagnostics. nil → /admin/diagnostics/parked-nodes returns an
	// empty list. Wired by config.StartControlAPI when the persistence
	// driver's reader is constructed. Plan G1 / G2.
	DiagnosticReader DiagnosticReader

	// InvalidateHandler is the operator-supplied unified invalidate
	// dispatch. Used by POST /admin/instances/{i}/nodes/{n}/invalidate
	// (plan G3) and forward-compat for handler-emitted invalidates
	// (H2). nil → endpoint returns 503.
	InvalidateHandler InvalidateHandler

	// Metrics is the dispatch/terminal/invalidate/claim instrumentation
	// hook. Threaded through to the operator-invalidate handler in
	// nodes.go so admin-fired invalidates increment
	// `rimsky_invalidates_total{source="admin"}`. Type is
	// `integration.MetricsHook` from foundation/integration; importing
	// from here is fine because controlapi already imports integration.
	// Nil → no-op.
	Metrics integration.MetricsHook
}

// ObservabilityRouter is the seam controlapi uses to mount the
// observability handlers without importing core/observability/ (which
// in turn would close a cycle). config.StartControlAPI passes a
// concrete value populated from observability.Routes.
type ObservabilityRouter func(r chi.Router)

// ExecutorEntry mirrors core/config.ExecutorEntry but lives in the
// controlapi package so the package compiles without the cyclic
// import (controlapi → config). The wiring helper at AppDeps
// construction time (config.StartControlAPI) populates this from the
// parsed config struct.
type ExecutorEntry struct {
	Transport string
	Endpoint  string
	TLS       string
}

// NewApp builds the full chi router with all registered routes + middleware.
// Individual route groups are registered in sibling files; the dependency
// graph is always:
//
//	NewApp -> registerFooRoutes(r, deps) -> handler(deps)
//
// so handlers never need global state.
func NewApp(deps AppDeps) http.Handler {
	r := chi.NewRouter()

	// Request ID + structured access log via slog-backed Logger.
	r.Use(chimiddleware.RequestID)
	r.Use(chimiddleware.Recoverer)
	r.Use(accessLog(deps.Logger))

	if deps.Auth != nil {
		r.Use(authMiddleware(deps.Auth))
	}

	// Mount the observability subtree under its own chi.Group with a
	// minimal middleware stack. Observability is read-only (GET-only)
	// and intentionally exempt from the AllowContentType
	// "application/json" gate that protects the write/admin surfaces;
	// keeping the gate scoped to the write paths means future read-
	// only additions on /v1/observability/* don't accidentally
	// inherit the gate.
	if deps.Observability != nil {
		r.Group(func(rr chi.Router) {
			rr.Use(func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					next.ServeHTTP(w, req)
				})
			})
			rr.Route("/v1/observability", deps.Observability)
		})
	}

	// Set common content-type + strict headers on the write/admin
	// surface only. AllowContentType applies to non-GET requests.
	r.Group(func(rr chi.Router) {
		rr.Use(chimiddleware.AllowContentType("application/json"))
		rr.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				next.ServeHTTP(w, req)
			})
		})

		// Route groups — sibling files register each group.
		registerTemplatesRoutes(rr, deps)
		registerTagsRoutes(rr, deps)
		registerInstancesRoutes(rr, deps)
		registerNodesRoutes(rr, deps)
		registerEventsRoutes(rr, deps)
		registerClaimsRoutes(rr, deps)
		registerAdminScheduleRoutes(rr, deps)
		registerAdminDiagnosticsRoutes(rr, deps)
		registerHealthRoutes(rr, deps)
	})

	return r
}

// accessLog logs one line per request at INFO level via the supplied Logger.
// Latency in ms, method, URL, status, request-id.
func accessLog(log shared.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := chimiddleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			lat := time.Since(start)
			log.Info("controlapi.request",
				slog.String("method", r.Method),
				slog.String("url", r.URL.String()),
				slog.Int("status", ww.Status()),
				slog.Duration("elapsed", lat),
				slog.String("request_id", chimiddleware.GetReqID(r.Context())))
		})
	}
}

// writeJSON marshals v as JSON and writes with the given status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = jsonEncode(w, v)
}

func jsonEncode(w http.ResponseWriter, v any) error {
	enc := jsonMarshal(v)
	_, err := w.Write(enc)
	return err
}

func jsonMarshal(v any) []byte {
	b, err := jsonMarshalStrict(v)
	if err != nil {
		return []byte(`{"error":"internal marshal error"}`)
	}
	return b
}

func jsonMarshalStrict(v any) ([]byte, error) {
	return jsonMarshalImpl(v)
}

// writeError translates rimsky sentinels to status codes, returning a JSON
// {"error": "<msg>"} body. Sentinels: ErrTemplateNotFound/ErrInstanceNotFound/
// ErrNodeNotFound → 404; ErrInstanceKeyConflict/ErrTemplateInUse → 409;
// ErrTemplateValidation → 400; everything else → 500.
func writeError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errorsIs(err, shared.ErrTemplateNotFound),
		errorsIs(err, shared.ErrInstanceNotFound),
		errorsIs(err, shared.ErrNodeNotFound):
		status = http.StatusNotFound
	case errorsIs(err, shared.ErrInstanceKeyConflict),
		errorsIs(err, shared.ErrTemplateInUse):
		status = http.StatusConflict
	case errorsIs(err, shared.ErrTemplateValidation):
		status = http.StatusBadRequest
	}
	writeJSON(w, status, map[string]any{"error": err.Error()})
}

// badRequest writes a 400 with msg.
func badRequest(w http.ResponseWriter, msg string) {
	writeJSON(w, http.StatusBadRequest, map[string]any{"error": msg})
}

// notFoundResp writes a 404 with msg.
func notFoundResp(w http.ResponseWriter, msg string) {
	writeJSON(w, http.StatusNotFound, map[string]any{"error": msg})
}
