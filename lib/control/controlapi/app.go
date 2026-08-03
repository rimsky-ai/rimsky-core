// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package controlapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/lifecycle"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/matcher"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	foundationshared "github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/peer"
)

type AppDeps struct {
	Persist        persistence.Tables
	Queue          persistence.Queue
	AdvisoryLocker persistence.AdvisoryLocker
	Clock          foundationshared.Clock
	Logger         foundationshared.Logger
	AuthState      *AuthState
	ClaimProducers *locks.Registry
	LifecycleSubs  *lifecycle.Registry
	NamedLocks     locks.NamedLocksConfig
	Executors      map[string]ExecutorEntry
	Observability  ObservabilityRouter

	ExecutorCapabilities func(executorName string) (declaredTags []string, declaredErrorClasses []string, expectedAttributesSchema []byte, ok bool)

	ClaimProducerDeclaredErrorClasses func(producerName string) (declaredErrorClasses []string, ok bool)

	Metrics runtime.MetricsHook

	Publishers runtime.PublisherRegistry

	Validators runtime.ValidationRegistry

	DataProcessors runtime.DataProcessingRegistry

	UnreachableValidatorPolicy runtime.UnreachableValidatorPolicy

	LateBindServiceProxies map[string]string

	// @concept: node
	KindAliases *node.KindAliasMap

	PeerAuth string

	Enroll *EnrollDeps
}

type ObservabilityRouter func(r chi.Router)

type ExecutorEntry struct {
	Transport string
	Endpoint  string
	TLS       string
}

func NewApp(deps AppDeps) http.Handler {
	if deps.AuthState == nil {
		panic("controlapi: NewApp requires AppDeps.AuthState; construct one (anonymous-mode is the zero-config case) rather than serving ungated")
	}

	r := chi.NewRouter()

	r.Use(chimiddleware.RequestID)
	r.Use(chimiddleware.Recoverer)
	r.Use(accessLog(deps.Logger))

	// @decision: protocol-version-v1-namespaced
	r.Route("/v1", func(v1 chi.Router) {
		registerHealthRoutes(v1, deps)

		v1.Group(func(rr chi.Router) {
			rr.Use(deps.AuthState.IdentityResolver())

			if deps.Observability != nil {
				rr.Group(func(obs chi.Router) {
					obs.Use(func(next http.Handler) http.Handler {
						return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
							w.Header().Set("Content-Type", "application/json")
							next.ServeHTTP(w, req)
						})
					})
					obs.Method("GET", "/observability/*", deps.AuthState.gateByAction("observability:read", deps.AuthState.observabilityWrapper(deps.Observability)))
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
				registerRunsRoutes(rrr, deps)
				registerEventsRoutes(rrr, deps)
				registerAuditRoutes(rrr, deps)
				registerClaimsRoutes(rrr, deps)
				registerMessagesRoutes(rrr, deps)
				registerFramesRoutes(rrr, deps)
				registerAssetsRoutes(rrr, deps)
				registerLineageRoutes(rrr, deps)
				registerAdminDiagnosticsRoutes(rrr, deps)
				registerAuthRoutes(rrr, deps)
				registerEnrollRoutes(rrr, deps)
				registerMCPRoute(rrr, deps)
			})
		})
	})

	if deps.AuthState.mcpRouterRef != nil {
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
	b, err := json.Marshal(v)
	if err != nil {
		return []byte(`{"error":"internal marshal error"}`)
	}
	return b
}

func writeError(w http.ResponseWriter, err error) {
	var pcErr *peer.ProducerCallError
	if errors.As(err, &pcErr) {
		writeProducerError(w, pcErr)
		return
	}
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, foundationshared.ErrTemplateNotFound),
		errors.Is(err, foundationshared.ErrInstanceNotFound),
		errors.Is(err, foundationshared.ErrNodeNotFound),
		errors.Is(err, foundationshared.ErrBreakpointNotFound),
		errors.Is(err, foundationshared.ErrBreakpointHitNotFound):
		status = http.StatusNotFound
	case errors.Is(err, foundationshared.ErrInstanceKeyConflict),
		errors.Is(err, foundationshared.ErrTemplateInUse),
		errors.Is(err, foundationshared.ErrInstanceNotPaused),
		errors.Is(err, foundationshared.ErrInstanceAlreadyPaused):
		status = http.StatusConflict
	case errors.Is(err, foundationshared.ErrTemplateValidation),
		errors.Is(err, foundationshared.ErrResumeOverlayInvalid),
		errors.Is(err, matcher.ErrInvalid):
		status = http.StatusBadRequest
	}
	writeJSON(w, status, map[string]any{"error": err.Error()})
}

// @decision: producer-error-passthrough
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
