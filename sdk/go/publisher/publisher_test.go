// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

package publisher

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type captureLog struct {
	mu   sync.Mutex
	msgs []string
}

func (c *captureLog) Warn(msg string, args ...any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.msgs = append(c.msgs, msg)
}

// TestSend_Success_NoRetry confirms a 2xx returns immediately with a
// single attempt.
func TestSend_Success_NoRetry(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	res := Send(context.Background(), srv.Client(), &captureLog{}, func(time.Duration) {}, Request{
		URL:            srv.URL,
		Envelope:       []byte(`{}`),
		IdempotencyKey: "k1",
		SensorName:     "test",
	})
	if res.Err != nil {
		t.Fatalf("Send: %v", res.Err)
	}
	if res.Attempts != 1 {
		t.Fatalf("expected 1 attempt, got %d", res.Attempts)
	}
	if atomic.LoadInt32(&hits) != 1 {
		t.Fatalf("expected 1 hit, got %d", hits)
	}
}

// TestSend_5xx_RetriesUpToMax confirms 5xx triggers retries.
func TestSend_5xx_RetriesUpToMax(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()
	res := Send(context.Background(), srv.Client(), &captureLog{}, func(time.Duration) {}, Request{
		URL:            srv.URL,
		Envelope:       []byte(`{}`),
		IdempotencyKey: "k1",
		SensorName:     "test",
	})
	if res.Err == nil {
		t.Fatal("Send: expected err on exhausted retries")
	}
	if res.Attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", res.Attempts)
	}
	if atomic.LoadInt32(&hits) != 3 {
		t.Fatalf("expected 3 hits, got %d", hits)
	}
	if res.Rejected {
		t.Fatal("5xx is transient, not Rejected")
	}
}

// TestSend_4xx_NoRetry_LogsRejected confirms 4xx is terminal AND logs
// the `publisher.message.rejected` key so operators see capability
// revocations distinctly.
func TestSend_4xx_NoRetry_LogsRejected(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	log := &captureLog{}
	res := Send(context.Background(), srv.Client(), log, func(time.Duration) {}, Request{
		URL:            srv.URL,
		Envelope:       []byte(`{}`),
		IdempotencyKey: "k1",
		SensorName:     "test",
		SubscriptionID: "sub-x",
	})
	if !res.Rejected {
		t.Fatal("4xx must set Rejected=true")
	}
	if res.Attempts != 1 {
		t.Fatalf("expected 1 attempt on 4xx, got %d", res.Attempts)
	}
	if atomic.LoadInt32(&hits) != 1 {
		t.Fatalf("expected 1 hit on 4xx, got %d", hits)
	}
	if len(log.msgs) != 1 || log.msgs[0] != "publisher.message.rejected" {
		t.Fatalf("expected publisher.message.rejected log, got %#v", log.msgs)
	}
}

// TestSend_5xxThenSuccess confirms the loop succeeds when a later
// attempt returns 2xx.
func TestSend_5xxThenSuccess(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := atomic.AddInt32(&hits, 1)
		if n == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	res := Send(context.Background(), srv.Client(), &captureLog{}, func(time.Duration) {}, Request{
		URL:        srv.URL,
		Envelope:   []byte(`{}`),
		SensorName: "test",
	})
	if res.Err != nil {
		t.Fatalf("Send: %v", res.Err)
	}
	if res.Attempts != 2 {
		t.Fatalf("expected 2 attempts, got %d", res.Attempts)
	}
}
