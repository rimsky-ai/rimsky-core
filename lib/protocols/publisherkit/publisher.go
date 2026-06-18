// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

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

type Logger interface {
	Warn(msg string, args ...any)
}

type Result struct {
	Status int
	Err error
	Rejected bool
	Attempts int
}

type Sleeper func(d time.Duration)

type Request struct {
	URL string
	Envelope []byte
	IdempotencyKey string
	SensorName string
	SubscriptionID string
}

func Send(ctx context.Context, client *http.Client, log Logger, sleep Sleeper, req Request) Result {
	if sleep == nil {
		sleep = time.Sleep
	}
	const maxAttempts = 3
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
