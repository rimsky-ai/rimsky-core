// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

package publisherkit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type Logger interface {
	Warn(msg string, args ...any)
}

type Result struct {
	Status   int
	Err      error
	Rejected bool
	Attempts int
}

type RejectedError struct {
	Status int
	URL    string
}

func (e *RejectedError) Error() string {
	return fmt.Sprintf("rimsky %s → %d (rejected)", e.URL, e.Status)
}

func permanentRejection(code int) bool {
	if code < 400 || code >= 500 {
		return false
	}
	return code != http.StatusRequestTimeout && code != http.StatusTooManyRequests
}

type Sleeper func(d time.Duration)

type Request struct {
	URL            string
	Envelope       []byte
	IdempotencyKey string
	PublisherName  string
	SubscriptionID string
}

type noopLogger struct{}

func (noopLogger) Warn(string, ...any) {}

func Send(ctx context.Context, client *http.Client, log Logger, sleep Sleeper, req Request) Result {
	if sleep == nil {
		sleep = time.Sleep
	}
	if log == nil {
		log = noopLogger{}
	}
	const maxAttempts = 3
	delays := []time.Duration{
		200 * time.Millisecond,
		566 * time.Millisecond,
	}
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
			if !shouldRetry(ctx, attempt, maxAttempts, sleep, delays) {
				return Result{Err: err, Status: 0, Attempts: attempt}
			}
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode < 300 {
			return Result{Status: resp.StatusCode, Attempts: attempt}
		}
		if permanentRejection(resp.StatusCode) {
			log.Warn("PUBLISHERKIT.MESSAGE.REJECTED",
				"publisher", req.PublisherName,
				"publisher_subscription_id", req.SubscriptionID,
				"status", resp.StatusCode,
				"body", truncate(string(body), 256),
				"url", req.URL)
			return Result{
				Status:   resp.StatusCode,
				Err:      &RejectedError{Status: resp.StatusCode, URL: req.URL},
				Rejected: true,
				Attempts: attempt,
			}
		}
		lastErr := fmt.Errorf("rimsky %s → %d", req.URL, resp.StatusCode)
		if !shouldRetry(ctx, attempt, maxAttempts, sleep, delays) {
			return Result{Err: lastErr, Status: resp.StatusCode, Attempts: attempt}
		}
	}
	panic("publisherkit.Send: loop exited without returning — shouldRetry must return false by the final attempt")
}

func shouldRetry(ctx context.Context, attempt, max int, sleep Sleeper, delays []time.Duration) bool {
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
	ctxAwareSleep(ctx, sleep, delays[idx])
	return ctx.Err() == nil
}

func ctxAwareSleep(ctx context.Context, sleep Sleeper, d time.Duration) {
	done := make(chan struct{})
	go func() {
		sleep(d)
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...(truncated)"
}

func MarshalEnvelope(envelope any) ([]byte, error) {
	b, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("marshal envelope: %w", err)
	}
	return b, nil
}
