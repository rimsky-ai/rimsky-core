// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Package controlapi implements the HTTP+JSON control API for rimsky
// orchestrators. Routes are registered in sibling files (templates.go,
// instances.go, nodes.go, events.go, claims.go,
// admin_diagnostics.go, health.go). Errors thrown inside handlers are
// mapped to HTTP responses via setErrorHandler.
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
	// Persist is the unified persistence.Tables handle (rimsky_* tables).
	// Required.
	Persist persistence.Tables
	// Queue is the dispatch-queue accessor. Required.
	Queue  persistence.Queue
	Clock  foundationshared.Clock
	Logger foundationshared.Logger
	// AuthState is the per-process auth-middleware state. Required;
	// constructed by control/config.StartControlAPI. Replaces the
	// pre-spec `Auth Authenticator` field.
	AuthState *AuthState
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
	// hook. Setter is control/config.StartControlAPI; per spec §1.1 the
	// observability surface is wired in at startup alongside the
	// existing admin/operator routes. Nil → /v1/observability/* is not
	// mounted (used by tests that don't exercise observability). The
	// router itself registers routes relative to the parent mount; the
	// hosting code below mounts it under the unified /v1/ prefix.
	Observability ObservabilityRouter

	// ExecutorCapabilities optionally exposes the observability cache's
	// per-executor capability fields (declared_events,
	// declared_error_classes, expected_attributes_schema) without
	// forcing controlapi to import the observability package. Wired by
	// config.StartControlAPI when an observability handshake has run.
	// Returns ok=false when no capability cache is loaded for the named
	// executor (e.g. peer is unreachable). Plan F6 + F7 + 2026-05-23
	// signal-taxonomy Pass 6.
	ExecutorCapabilities func(executorName string) (declaredEvents []string, declaredErrorClasses []string, expectedAttributesSchema []byte, ok bool)

	// StoreDeclaredErrorClasses optionally exposes the observability
	// cache's per-store producer-declared error-class vocabulary
	// (claim_producer.proto::CapabilitiesResponse.declared_error_classes,
	// captured at the startup handshake) without forcing controlapi to
	// import the observability package. Wired by config.StartControlAPI
	// alongside ExecutorCapabilities. Returns ok=false when no
	// capability cache is loaded for the named store (e.g. peer is
	// unreachable). Feeds the template validator's `error_types:`
	// range-check union (executor ∪ reachable producers ∪ acquire/*).
	StoreDeclaredErrorClasses func(storeName string) (declaredErrorClasses []string, ok bool)

	// RefValidationMode is the operator-set registration-time
	// reference-validation mode (all / available / none), sourced from
	// cfg:templates.ref_validation_mode and env:RIMSKY_REF_VALIDATION_MODE
	// by config.StartControlAPI. The zero value (node.RefValidateAll) is
	// the strict default — every referenced service is validated at
	// registration and registration hard-fails on any unvalidatable
	// reference. Stamped onto node.RegistryHooks.RefValidationMode by
	// validatorHooksFor so the registration + POST /templates/validate
	// paths share one operator-chosen strictness. Story
	// S-template-validation-ref-validation-mode.
	RefValidationMode node.RefValidationMode

	// InvalidateHandler is the operator-supplied unified invalidate
	// dispatch. Used by POST /admin/instances/{i}/nodes/{n}/invalidate
	// (plan G3) and forward-compat for handler-emitted invalidates
	// (H2). nil → endpoint returns 503.
	InvalidateHandler InvalidateHandler

	// Metrics is the dispatch/terminal/invalidate/claim instrumentation
	// hook. Threaded through to the operator-invalidate handler in
	// nodes.go so admin-fired invalidates increment
	// `rimsky_invalidates_total{source="admin"}`. Type is
	// `runtime.MetricsHook` from runtime; importing
	// from here is fine because controlapi already imports integration.
	// Nil → no-op.
	Metrics runtime.MetricsHook

	// Publishers is the per-process publisher-client registry. The
	// instance-create flow inserts publisher-subscription rows in
	// `state = mounting` per the template's `publishers:` block; the
	// reconciler and instance-terminate flow resolve publisher names
	// through this registry to issue Subscribe / Unsubscribe. Nil →
	// every name resolves as unknown: rows are still inserted but flip
	// straight to `failed` (unknown publisher) with a reason, and the
	// startup resync sweep no-ops. Such rows recover at the next
	// startup resync once the registry contains the name.
	Publishers runtime.PublisherRegistry

	// Validators is the per-process Validation-mix-in registry. Used by
	// `POST /templates` to fire the Validation pipeline against each
	// service the template references that advertises the
	// `validation` protocol. Nil → the pipeline is skipped and the
	// template registers on the static-check pass alone. Plan F9.
	Validators runtime.ValidationRegistry

	// DataProcessors is the per-process DataProcessing-mix-in registry.
	// Resolves a producer name to a `runtime.DataProcessingClient` for
	// the fan-out / candidate / version surface
	// (`BeginCandidate` / `CommitCandidate` / `AbandonCandidate` /
	// `ListVersions` / `ListPartitions` / `GetVersionSchema`). Wired by
	// `control/config.StartControlAPI` from the per-peer `protocols:`
	// declarations. Nil → asset endpoints (`/instances/{id}/assets/...`)
	// that need version metadata return 503 and the fan-out path skips
	// candidate-handle persistence. Spec
	DataProcessors runtime.DataProcessingRegistry

	// UnreachableValidatorPolicy controls the pipeline's reaction to a
	// per-service RPC failure: `strict` rejects; `permissive_warn`
	// (default) surfaces a warning. Plan F9 step 4.
	UnreachableValidatorPolicy runtime.UnreachableValidatorPolicy

	// LateBindServiceProxies maps protocol → proxy service name. Populated
	// from rimsky.yml's late_bind_service_proxies by StartControlAPI.
	// Consumed by LifecyclePeersForSpec to know which proxy peer to add
	// to the fan-out when a template declares late_bind_services.
	LateBindServiceProxies map[string]string

	// KindAliases is the static `kind:` → executor-alias map seeded at
	// startup alongside the supervisor's InProcessRegistry. Threaded
	// through validatorHooksFor so the per-node `kind:` validator can
	// range-check the optional `kind:` field, and consumed by
	// CanonicalizeKindSugar in the template-deploy / validate paths to
	// rewrite `kind: <name>` → `executor: <alias>` before the spec is
	// hashed (so post-canonicalization the persisted spec is in normal
	// form and downstream registration code never has to know about
	// kind sugar). Nil → templates declaring any `kind:` value are
	// rejected. Populated by StartControlAPI from the same package
	// constants the supervisor uses.
	//
	// @concept: node
	KindAliases *node.KindAliasMap
}

// ObservabilityRouter is the seam controlapi uses to mount the
// observability handlers without importing control/observability/ (which
// in turn would close a cycle). config.StartControlAPI passes a
// concrete value populated from observability.Routes.
type ObservabilityRouter func(r chi.Router)

// ExecutorEntry mirrors control/config.ExecutorEntry but lives in the
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

	// @constraint: request ID + structured access log via slog-backed Logger.
	r.Use(chimiddleware.RequestID)
	r.Use(chimiddleware.Recoverer)
	r.Use(accessLog(deps.Logger))

	// @constraint: every control-API route lives under the /v1/ version prefix. The
	// previous tree mixed bare paths (`/templates`, `/instances`, …)
	// with carve-outs at `/v1/observability/*` and `/v1/callback/...`
	// (the callback listener lives on a separate http.Server in
	// runtime.CallbackServer and is unaffected by this mount). The /v1/
	// route below absorbs both surfaces under a single prefix per
	// decision:pre-v1-break-freely + tension:control-api-version-prefix.
	r.Route("/v1", func(v1 chi.Router) {
		// @constraint: health endpoints are NOT auth-gated — they predate auth and
		// serve infrastructure clients (load balancer, k8s probes) that
		// don't carry Bearer tokens. Register on the /v1/ sub-router
		// BEFORE the auth middleware group so `GET /v1/health` reaches
		// the handler without identity resolution. STORY-rimsky-health-
		// check Falsifier: "/v1/health requires auth" — protect that by
		// keeping this registration outside the auth group.
		registerHealthRoutes(v1, deps)

		// @constraint: everything else under the auth middleware.
		v1.Group(func(rr chi.Router) {
			if deps.AuthState != nil {
				rr.Use(deps.AuthState.IdentityResolver())
			}

			// @constraint: mount the observability subtree under its own chi.Group
			// with a minimal middleware stack. Observability is
			// read-only (GET-only) and intentionally exempt from the
			// AllowContentType "application/json" gate.
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

			// @constraint: write/admin surface — sets common content-type and the
			// strict AllowContentType gate. Sibling files register each
			// group of routes; each handler is wrapped with
			// gateByAction at registration time.
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
				registerNodesRoutes(rrr, deps)
				registerEventsRoutes(rrr, deps)
				registerAuditRoutes(rrr, deps)
				registerClaimsRoutes(rrr, deps)
				registerMessagesRoutes(rrr, deps)
				registerBackfillsRoutes(rrr, deps)
				registerAssetsRoutes(rrr, deps)
				registerLineageRoutes(rrr, deps)
				registerAdminDiagnosticsRoutes(rrr, deps)
				registerAuthRoutes(rrr, deps)
				registerMCPRoute(rrr, deps)
			})
		})
	})

	// @constraint: late-bind the MCP catalog's router pointer to the finished
	// chi router so in-process tool calls re-enter the pipeline.
	if deps.AuthState != nil && deps.AuthState.mcpRouterRef != nil {
		deps.AuthState.mcpRouterRef.h = r
	}

	return r
}

// observabilityWrapper turns an ObservabilityRouter into an
// http.HandlerFunc. The router is a `chi.Router → ObservabilityRouter`
// closure; we mount it under a local sub-router whose pattern matches
// the /v1/ sub-router we are already inside (`/observability/*`), so
// the outer /v1/ prefix is not double-applied.
func (s *AuthState) observabilityWrapper(or ObservabilityRouter) http.HandlerFunc {
	r := chi.NewRouter()
	r.Route("/observability", or)
	return r.ServeHTTP
}

// accessLog logs one line per request at INFO level via the supplied Logger.
// Latency in ms, method, URL, status, request-id.
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
// ErrNodeNotFound/ErrBreakpointNotFound/ErrBreakpointHitNotFound → 404;
// ErrInstanceKeyConflict/ErrTemplateInUse/ErrInstanceNotPaused/
// ErrInstanceAlreadyPaused → 409;
// ErrTemplateValidation/ErrResumeOverlayInvalid/matcher.ErrInvalid → 400;
// everything else → 500.
//
// A remote-producer failure (*peer.ProducerCallError, the typed
// translation every producer-protocol client returns) is handled first
// by writeProducerError: the producer's transmitted error class and
// message cross the HTTP boundary intact under a status distinguishing
// "your producer rejected/failed this" (422/502) from "rimsky broke
// internally" (500). Rimsky-internal errors keep the mapping above.
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

// writeProducerError maps a remote-producer call failure to the HTTP
// response. Status (a deliberate two-way split):
//   - 422 Unprocessable Entity when the producer REJECTED the request
//     — it ran fine and judged the request itself bad. That is the
//     request-rejection code family: InvalidArgument /
//     FailedPrecondition / OutOfRange (malformed or ill-timed input),
//     NotFound / AlreadyExists (the request names an entity in the
//     wrong existence state — e.g. Release of a claim the producer
//     doesn't hold), and PermissionDenied (the producer judged the
//     request unauthorized). None of these are producer faults.
//   - 502 Bad Gateway for every other code (Internal, Unavailable,
//     DeadlineExceeded, Unknown, …) — the producer faulted or is
//     unreachable, the gateway role rimsky plays between the operator
//     and their producer.
//
// The body extends the standard {"error": ...} envelope with the
// fields the producer transmitted: producer_name, error_class (the
// ErrorInfo.Reason rimsky's peer client decoded; "" when the producer
// named no class), and message (the producer's own status message,
// without the gRPC wrapping). Discarding any of these would force the
// operator back to grepping rimsky's logs for diagnosis the producer
// already did.
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

// badRequest writes a 400 with msg.
func badRequest(w http.ResponseWriter, msg string) {
	writeJSON(w, http.StatusBadRequest, map[string]any{"error": msg})
}

// notFoundResp writes a 404 with msg.
func notFoundResp(w http.ResponseWriter, msg string) {
	writeJSON(w, http.StatusNotFound, map[string]any{"error": msg})
}
