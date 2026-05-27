// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Audit-event emit helpers consumed by the auth middleware and the
// auth-endpoint handlers. Writes rows into rimsky_events with the
// `auth.*` kind and the structured payload from foundation/auth.
//
// Audit writes happen off the request hot path: the auth middleware
// hands a fully-built payload to `insertEvent` which dispatches the
// actual DB transaction via a bounded worker pool
// (`auditDispatcher`). Failures are logged and swallowed; the spec
// (section "Audit and dry-run") requires that audit-system trouble
// never affect user-facing latency, and a slow / hung Postgres must
// not back up the response loop.
//
// @concept: event-log

package controlapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/rimsky-ai/rimsky-core/foundation/auth"
	"github.com/rimsky-ai/rimsky-core/foundation/persistence"
	foundationshared "github.com/rimsky-ai/rimsky-core/foundation/shared"
)

// emitAttempted writes one auth.access_attempted event.
// paramsInvalid signals the captured request body was non-empty but
// not valid JSON; when set, requestParams must be nil and the audit
// row carries `request_params_invalid: true`.
func (s *AuthState) emitAttempted(
	ctx context.Context,
	r *http.Request,
	start time.Time,
	ident auth.Identity,
	action string,
	skin string,
	requestParams json.RawMessage,
	paramsInvalid bool,
	status int,
	mode auth.Mode,
) {
	elapsed := s.Clock.Now().Sub(start).Milliseconds()
	p := auth.AccessAttemptedPayload{
		KeyID:                ident.KeyID,
		KeyName:              ident.KeyName,
		IdentityKind:         ident.Kind,
		ProtocolSkin:         skin,
		Action:               action,
		RequestPath:          r.URL.Path,
		RequestMethod:        r.Method,
		RequestParams:        requestParams,
		RequestParamsInvalid: paramsInvalid,
		ResponseStatus:       status,
		Mode:                 mode,
		Executed:             mode == auth.ModeExecute && status < 400,
		DurationMS:           elapsed,
		ClientIP:             clientIP(r),
		UserAgent:            r.Header.Get("User-Agent"),
	}
	s.insertEvent(ctx, auth.EventAccessAttempted, p)
}

// emitDenied writes one auth.access_denied event with the population
// rules from spec section "Population rules for denial rows":
//
//   - For pre-action-resolution denials (no_token, invalid_token,
//     expired_token, revoked_token): Action/RequestParams/Mode null.
//   - For permission_denied: Action and RequestParams populated; Mode
//     null (no matching grant entry, so no mode determined).
//
// paramsInvalid signals the captured request body was non-empty but
// not valid JSON; when set, requestParams must be nil and the audit
// row carries `request_params_invalid: true`.
func (s *AuthState) emitDenied(
	ctx context.Context,
	r *http.Request,
	start time.Time,
	ident auth.Identity,
	action string,
	skin string,
	requestParams json.RawMessage,
	paramsInvalid bool,
	status int,
	reason auth.DenialReason,
) {
	elapsed := s.Clock.Now().Sub(start).Milliseconds()
	p := auth.AccessDeniedPayload{
		ProtocolSkin:         skin,
		RequestPath:          r.URL.Path,
		RequestMethod:        r.Method,
		RequestParams:        requestParams,
		RequestParamsInvalid: paramsInvalid,
		ResponseStatus:       status,
		Executed:             false,
		DurationMS:           elapsed,
		ClientIP:             clientIP(r),
		UserAgent:            r.Header.Get("User-Agent"),
		DenialReason:         reason,
	}
	if ident.KeyID != nil || ident.KeyName != "" {
		kn := ident.KeyName
		kk := ident.Kind
		p.KeyID = ident.KeyID
		p.KeyName = &kn
		p.IdentityKind = &kk
	}
	if action != "" {
		a := action
		p.Action = &a
	}
	s.insertEvent(ctx, auth.EventAccessDenied, p)
}

// EmitKeyCreated writes one auth.key_created event.
func (s *AuthState) EmitKeyCreated(ctx context.Context, p auth.KeyCreatedPayload) {
	s.insertEvent(ctx, auth.EventKeyCreated, p)
}

// EmitKeyRevoked writes one auth.key_revoked event.
func (s *AuthState) EmitKeyRevoked(ctx context.Context, p auth.KeyRevokedPayload) {
	s.insertEvent(ctx, auth.EventKeyRevoked, p)
}

// EmitKeyRotated writes one auth.key_rotated event.
func (s *AuthState) EmitKeyRotated(ctx context.Context, p auth.KeyRotatedPayload) {
	s.insertEvent(ctx, auth.EventKeyRotated, p)
}

// auditQueueSize bounds the audit dispatcher's pending-job buffer.
// Sized for steady-state bursts; if the worker pool can't keep up the
// channel fills and `insertEvent` drops the row with a logged warning
// rather than blocking the request goroutine.
const auditQueueSize = 1024

// auditWorkers is the goroutine count consuming the queue. Bounded so
// a slow Postgres can't pin the process under unbounded goroutines.
const auditWorkers = 4

// auditWriteTimeout is the per-row DB-write deadline. A request that
// took 1ms shouldn't pay >2s for its audit-row insertion to surface
// as a logged error; the bound mirrors the existing UpdateLastUsed
// background timeout.
const auditWriteTimeout = 2 * time.Second

// auditJob carries one pending insert. The payload is already
// serialized into a map[string]any so the dispatcher goroutine never
// touches request-scoped state.
type auditJob struct {
	kind    string
	payload map[string]any
}

// auditDispatcher buffers audit inserts and runs them off the request
// hot path. Construct via newAuditDispatcher; Stop drains the queue.
type auditDispatcher struct {
	tables persistence.Tables
	logger foundationshared.Logger
	queue  chan auditJob
	wg     sync.WaitGroup
}

// newAuditDispatcher starts `auditWorkers` goroutines consuming the
// bounded queue. The dispatcher lives for the life of the AuthState.
func newAuditDispatcher(tables persistence.Tables, logger foundationshared.Logger) *auditDispatcher {
	d := &auditDispatcher{
		tables: tables,
		logger: logger,
		queue:  make(chan auditJob, auditQueueSize),
	}
	for i := 0; i < auditWorkers; i++ {
		d.wg.Add(1)
		go d.run()
	}
	return d
}

// run consumes jobs from the queue until it closes.
func (d *auditDispatcher) run() {
	defer d.wg.Done()
	for job := range d.queue {
		d.write(job)
	}
}

// write performs the actual DB insert with a bounded timeout.
func (d *auditDispatcher) write(job auditJob) {
	ctx, cancel := context.WithTimeout(context.Background(), auditWriteTimeout)
	defer cancel()
	err := d.tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return d.tables.Events().Append(ctx, persistence.EventAppendInput{
			Kind:    job.kind,
			Payload: job.payload,
		}, tx)
	})
	if err != nil && d.logger != nil {
		d.logger.Error("audit.insert", "kind", job.kind, "err", err.Error())
	}
}

// submit enqueues a job non-blockingly. On a full queue, the job is
// dropped with a logged warning — preferring a missing audit row over
// a blocked request goroutine.
func (d *auditDispatcher) submit(job auditJob) {
	select {
	case d.queue <- job:
	default:
		if d.logger != nil {
			d.logger.Warn("audit.queue_full_dropped", "kind", job.kind)
		}
	}
}

// Stop closes the queue and waits for the workers to drain. Tests use
// this to deterministically observe audit rows before assertions.
func (d *auditDispatcher) Stop() {
	close(d.queue)
	d.wg.Wait()
}

// insertEvent marshals the typed payload, decodes it back into the
// generic map[string]any shape the EventTable.Append signature wants,
// and submits the job to the audit dispatcher. The actual DB insert
// runs on a background worker so request-handler latency is decoupled
// from audit-system health.
func (s *AuthState) insertEvent(_ context.Context, kind string, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		s.Logger.Error("audit.marshal", "kind", kind, "err", err.Error())
		return
	}
	payloadMap := map[string]any{}
	if err := json.Unmarshal(data, &payloadMap); err != nil {
		s.Logger.Error("audit.unmarshal-to-map", "kind", kind, "err", err.Error())
		return
	}
	d := s.dispatcher()
	if d == nil {
		// No dispatcher wired (test path that bypassed
		// EnsureAuditDispatcher). Fall back to the synchronous insert
		// so audit rows still land. Production wires the dispatcher
		// via EnsureAuditDispatcher at startup.
		if err := s.Tables.Transaction(context.Background(), func(ctx context.Context, tx persistence.Tx) error {
			return s.Tables.Events().Append(ctx, persistence.EventAppendInput{
				Kind:    kind,
				Payload: payloadMap,
			}, tx)
		}); err != nil {
			s.Logger.Error("audit.insert", "kind", kind, "err", err.Error())
		}
		return
	}
	d.submit(auditJob{kind: kind, payload: payloadMap})
}

// clientIP extracts a usable client IP from the request. Prefer the
// first entry in X-Forwarded-For; fall back to RemoteAddr.
func clientIP(r *http.Request) string {
	if h := r.Header.Get("X-Forwarded-For"); h != "" {
		if i := strings.Index(h, ","); i > 0 {
			return strings.TrimSpace(h[:i])
		}
		return strings.TrimSpace(h)
	}
	return r.RemoteAddr
}
