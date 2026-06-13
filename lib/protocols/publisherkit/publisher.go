// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

// Package publisherkit implements the shared publisher-side message-emit
// retry-with-backoff helper used by every bundled sensor (sensor-cron,
// sensor-http, sensor-object-store, sensor-webhook) and any third-party
// publisher service that POSTs to rimsky's
// `POST /v1/instances/{id}/messages` endpoint.
//
// Retry policy: publishers POSTing to rimsky's
// `POST /v1/instances/{id}/messages` endpoint retry on 5xx + connection
// errors (3 attempts total, with sleeps of 200ms then ~566ms between
// attempts). 4xx is terminal and logged with the
// `publisher.message.rejected` key so operators can spot
// capability-revocation / route-misconfiguration without digging
// through per-sensor log noise.
//
// Lives in the protocols module so third-party publisher authors get the same
// retry-with-idempotency-header behavior as the bundled sensors.
package publisherkit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Logger is the narrow logging surface the helper needs. Sensors
// already define a `logger` interface; this type stays parallel so
// passing in `slogAdapter{}` works directly.
type Logger interface {
	Warn(msg string, args ...any)
}

// Result captures the outcome of a single Send call so callers can
// distinguish "rejected" from "transient failure exhausted retries".
type Result struct {
	// Status is the final HTTP status code from rimsky (0 when no
	// response was ever received).
	Status int
	// Err is the final error, if any. nil → success.
	Err error
	// Rejected is true when rimsky returned a 4xx that terminated the
	// retry loop (capability revoked, route gone, body invalid).
	Rejected bool
	// Attempts is how many POSTs were issued (1 → no retry).
	Attempts int
}

// Sleeper is the time-sleeping abstraction; production callers pass
// nil (defaults to time.Sleep). Tests can inject a no-op sleeper.
type Sleeper func(d time.Duration)

// Request is the input shape the helper needs to POST one envelope.
type Request struct {
	// URL is the fully-qualified rimsky messages endpoint, including
	// the `/v1/instances/{id}/messages` path.
	URL string
	// Envelope is the JSON-marshaled message body.
	Envelope []byte
	// IdempotencyKey is the value of the universal `Idempotency-Key`
	// header (empty → header omitted).
	IdempotencyKey string
	// SensorName is folded into the operator-visible log keys so
	// `publisher.message.rejected` lines disambiguate across sensors.
	SensorName string
	// SubscriptionID is logged alongside SensorName for the same
	// reason.
	SubscriptionID string
}

// Send POSTs `req.Envelope` to `req.URL` with the universal retry-
// with-backoff policy. Backoff: 200ms after attempt 1, ~566ms after
// attempt 2 (geometric, ratio ≈ 2.83), so attempt 3 is issued ≈766ms
// of cumulative sleep after attempt 1.
//
// 5xx + transport errors: retry up to 3 attempts total.
// 4xx: log `publisher.message.rejected` at WARN and abandon
// immediately (rimsky has rejected the capability or the body; a
// retry would not help).
// 2xx: success.
//
// On final failure (retries exhausted), the caller's existing
// `<sensor>.message_post_failed` log key still fires — the helper
// only adds the `publisher.message.rejected` 4xx-path key.
func Send(ctx context.Context, client *http.Client, log Logger, sleep Sleeper, req Request) Result {
	if sleep == nil {
		sleep = time.Sleep
	}
	const maxAttempts = 3
	// Backoff schedule: 200ms after attempt 1, ~566ms after attempt 2.
	// Cumulative sleep before attempt 3 ≈ 766ms (the wall-clock issue
	// time also includes the first two attempts' own round-trip time).
	delays := []time.Duration{
		200 * time.Millisecond,
		566 * time.Millisecond,
	}
	var lastErr error
	var lastStatus int
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, req.URL, bytes.NewReader(req.Envelope))
		if err != nil {
			return Result{Err: err, Attempts: attempt}
		}
		httpReq.Header.Set("Content-Type", "application/json")
		if req.IdempotencyKey != "" {
			httpReq.Header.Set("Idempotency-Key", req.IdempotencyKey)
		}
		resp, err := client.Do(httpReq)
		if err != nil {
			lastErr = err
			lastStatus = 0
			if !shouldRetry(attempt, maxAttempts, ctx, sleep, delays) {
				return Result{Err: err, Status: 0, Attempts: attempt}
			}
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		lastStatus = resp.StatusCode
		if resp.StatusCode < 300 {
			return Result{Status: resp.StatusCode, Attempts: attempt}
		}
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			// 4xx is terminal — rimsky rejected the capability or the
			// envelope. Operators need to see this distinctly from
			// transient transport failures.
			log.Warn("publisher.message.rejected",
				"sensor", req.SensorName,
				"publisher_subscription_id", req.SubscriptionID,
				"status", resp.StatusCode,
				"body", truncate(string(body), 256),
				"url", req.URL)
			return Result{
				Status:   resp.StatusCode,
				Err:      fmt.Errorf("rimsky %s → %d (rejected)", req.URL, resp.StatusCode),
				Rejected: true,
				Attempts: attempt,
			}
		}
		// 5xx — retryable.
		lastErr = fmt.Errorf("rimsky %s → %d", req.URL, resp.StatusCode)
		if !shouldRetry(attempt, maxAttempts, ctx, sleep, delays) {
			return Result{Err: lastErr, Status: resp.StatusCode, Attempts: attempt}
		}
	}
	if lastErr == nil {
		lastErr = errors.New("publisher.post: retries exhausted")
	}
	return Result{Err: lastErr, Status: lastStatus, Attempts: maxAttempts}
}

// shouldRetry sleeps the per-attempt backoff when more attempts remain
// and the context is still live. Returns false when the loop should
// terminate.
func shouldRetry(attempt, max int, ctx context.Context, sleep Sleeper, delays []time.Duration) bool {
	if attempt >= max {
		return false
	}
	if ctx.Err() != nil {
		return false
	}
	idx := attempt - 1
	if idx >= len(delays) {
		idx = len(delays) - 1
	}
	sleep(delays[idx])
	return ctx.Err() == nil
}

// truncate caps a string for log inclusion.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...(truncated)"
}

// MarshalEnvelope is a small convenience around `json.Marshal` so
// callers can compose `Send` calls in a single expression.
func MarshalEnvelope(envelope any) ([]byte, error) {
	b, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("marshal envelope: %w", err)
	}
	return b, nil
}
