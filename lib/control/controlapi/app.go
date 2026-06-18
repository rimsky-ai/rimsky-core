// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package controlapi

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/matcher"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	foundationshared "github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/peer"
)

type AppDeps struct {
	Persist       persistence.Tables
	Queue         persistence.Queue
	Clock         foundationshared.Clock
	Logger        foundationshared.Logger
	AuthState     *AuthState
	Stores        *locks.Registry
	LifecycleSubs *locks.LifecycleRegistry
	NamedLocks    locks.NamedLocksConfig
	Executors     map[string]ExecutorEntry
	Observability ObservabilityRouter

	ExecutorCapabilities func(executorName string) (declaredEvents []string, declaredErrorClasses []string, expectedAttributesSchema []byte, ok bool)

	StoreDeclaredErrorClasses func(storeName string) (declaredErrorClasses []string, ok bool)

	RefValidationMode node.RefValidationMode

	Metrics runtime.MetricsHook

	Publishers runtime.PublisherRegistry

	Validators runtime.ValidationRegistry

	DataProcessors runtime.DataProcessingRegistry

	UnreachableValidatorPolicy runtime.UnreachableValidatorPolicy

	LateBindServiceProxies map[string]string

	// @concept: node
	KindAliases *node.KindAliasMap
}

type ObservabilityRouter func(r chi.Router)

type ExecutorEntry struct {
	Transport string
	Endpoint  string
	TLS       string
}

func NewApp(deps AppDeps) http.Handler {
	r := chi.NewRouter()

	r.Use(chimiddleware.RequestID)
	r.Use(chimiddleware.Recoverer)
	r.Use(accessLog(deps.Logger))

	r.Route("/v1", func(v1 chi.Router) {
		registerHealthRoutes(v1, deps)

		v1.Group(func(rr chi.Router) {
			if deps.AuthState != nil {
				rr.Use(deps.AuthState.IdentityResolver())
			}

			if deps.Observability != nil {
				rr.Group(func(obs chi.Router) {
					obs.Use(func(next http.Handler) http.Handler {
						return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
							w.Header().Set("Content-Type", "application/json")
							next.ServeHTTP(w, req)
						})
					})
					if deps.AuthState != nil {
						obs.Method("GET", "/observability/*", deps.AuthState.gateByAction("observability:read", deps.AuthState.observabilityWrapper(deps.Observability)))
					} else {
						obs.Route("/observability", deps.Observability)
					}
				})
			}

			rr.Group(func(rrr chi.Router) {
				rrr.Use(chimiddleware.AllowContentType("application/json"))
				rrr.Use(func(next http.Handler) http.Handler {
					return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						next.ServeHTTP(w, req)
					})
				})

				registerTemplatesRoutes(rrr, deps)
				registerTagsRoutes(rrr, deps)
				registerInstancesRoutes(rrr, deps)
				registerBreakpointsRoutes(rrr, deps)
				registerDebugOverrideRoutes(rrr, deps)
				registerNodesRoutes(rrr, deps)
				registerEventsRoutes(rrr, deps)
				registerAuditRoutes(rrr, deps)
				registerClaimsRoutes(rrr, deps)
				registerMessagesRoutes(rrr, deps)
				registerFramesRoutes(rrr, deps)
				registerAssetsRoutes(rrr, deps)
				registerLineageRoutes(rrr, deps)
				registerAdminDiagnosticsRoutes(rrr, deps)
				registerAuthRoutes(rrr, deps)
				registerMCPRoute(rrr, deps)
			})
		})
	})

	if deps.AuthState != nil && deps.AuthState.mcpRouterRef != nil {
		deps.AuthState.mcpRouterRef.h = r
	}

	return r
}

func (s *AuthState) observabilityWrapper(or ObservabilityRouter) http.HandlerFunc {
	r := chi.NewRouter()
	r.Route("/observability", or)
	return r.ServeHTTP
}

func accessLog(log foundationshared.Logger) func(http.Handler) http.Handler {
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

func writeError(w http.ResponseWriter, err error) {
	var pcErr *peer.ProducerCallError
	if errors.As(err, &pcErr) {
		writeProducerError(w, pcErr)
		return
	}
	status := http.StatusInternalServerError
	switch {
	case errorsIs(err, foundationshared.ErrTemplateNotFound),
		errorsIs(err, foundationshared.ErrInstanceNotFound),
		errorsIs(err, foundationshared.ErrNodeNotFound),
		errorsIs(err, foundationshared.ErrBreakpointNotFound),
		errorsIs(err, foundationshared.ErrBreakpointHitNotFound):
		status = http.StatusNotFound
	case errorsIs(err, foundationshared.ErrInstanceKeyConflict),
		errorsIs(err, foundationshared.ErrTemplateInUse),
		errorsIs(err, foundationshared.ErrInstanceNotPaused),
		errorsIs(err, foundationshared.ErrInstanceAlreadyPaused):
		status = http.StatusConflict
	case errorsIs(err, foundationshared.ErrTemplateValidation),
		errorsIs(err, foundationshared.ErrResumeOverlayInvalid),
		errorsIs(err, matcher.ErrInvalid):
		status = http.StatusBadRequest
	}
	writeJSON(w, status, map[string]any{"error": err.Error()})
}

func writeProducerError(w http.ResponseWriter, pcErr *peer.ProducerCallError) {
	httpStatus := http.StatusBadGateway
	switch grpcstatus.Code(pcErr.Underlying) {
	case grpccodes.InvalidArgument, grpccodes.FailedPrecondition, grpccodes.OutOfRange,
		grpccodes.NotFound, grpccodes.AlreadyExists, grpccodes.PermissionDenied:
		httpStatus = http.StatusUnprocessableEntity
	}
	writeJSON(w, httpStatus, map[string]any{
		"error":         pcErr.Error(),
		"producer_name": pcErr.ProducerName,
		"error_class":   pcErr.ErrorClass,
		"message":       pcErr.Message,
	})
}

func badRequest(w http.ResponseWriter, msg string) {
	writeJSON(w, http.StatusBadRequest, map[string]any{"error": msg})
}

func notFoundResp(w http.ResponseWriter, msg string) {
	writeJSON(w, http.StatusNotFound, map[string]any{"error": msg})
}
