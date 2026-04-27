// Package controlapi implements the HTTP+JSON control API for rimsky
// orchestrators. Routes are registered in sibling files
// (templates.go, instances.go, nodes.go, events.go, claims.go,
// admin_claim_stores.go, admin_force_fire.go, health.go). Errors thrown
// inside handlers are mapped to HTTP responses via setErrorHandler.
package controlapi

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"

	"github.com/fallguy/rimsky/core/queue"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
	"github.com/fallguy/rimsky/core/store"
)

type AppDeps struct {
	Storage storage.StorageBackend
	Queue   queue.DispatchQueue
	Clock   shared.Clock
	Logger  shared.Logger
	Auth    Authenticator // may be nil → anonymous access
	// Stores is the per-process *store.Registry built from stores.yml. Used by
	// admin endpoints that target a specific named store (e.g.
	// POST /admin/claim-stores/:name/items). May be nil at construction time;
	// admin handlers that need it return 503 when nil.
	Stores *store.Registry
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

	// Set common content-type + strict headers.
	r.Use(chimiddleware.AllowContentType("application/json"))
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			next.ServeHTTP(w, req)
		})
	})

	// Route groups — sibling files register each group.
	registerTemplatesRoutes(r, deps)
	registerInstancesRoutes(r, deps)
	registerNodesRoutes(r, deps)
	registerEventsRoutes(r, deps)
	registerClaimsRoutes(r, deps)
	registerAdminClaimStoresRoutes(r, deps)
	registerAdminScheduleRoutes(r, deps)
	registerHealthRoutes(r, deps)

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
// ErrNodeNotFound → 404; ErrConsumerKeyConflict/ErrTemplateInUse → 409;
// ErrTemplateValidation → 400; everything else → 500.
func writeError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errorsIs(err, shared.ErrTemplateNotFound),
		errorsIs(err, shared.ErrInstanceNotFound),
		errorsIs(err, shared.ErrNodeNotFound):
		status = http.StatusNotFound
	case errorsIs(err, shared.ErrConsumerKeyConflict),
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
