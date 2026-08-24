// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

package publisherkit

import (
	"context"
	"errors"
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
		PublisherName:  "test",
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
		PublisherName:  "test",
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
		PublisherName:  "test",
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
	if len(log.msgs) != 1 || log.msgs[0] != "PUBLISHERKIT.MESSAGE.REJECTED" {
		t.Fatalf("expected a PUBLISHERKIT.MESSAGE.REJECTED log, got %#v", log.msgs)
	}
}

func TestSend_4xx_ErrIsTypedRejectedError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	res := Send(context.Background(), srv.Client(), &captureLog{}, func(time.Duration) {}, Request{
		URL:           srv.URL,
		Envelope:      []byte(`{}`),
		PublisherName: "test",
	})
	var rejected *RejectedError
	if !errors.As(res.Err, &rejected) {
		t.Fatalf("permanent 4xx must return *RejectedError, got %T: %v", res.Err, res.Err)
	}
	if rejected.Status != http.StatusForbidden {
		t.Fatalf("RejectedError.Status = %d, want %d", rejected.Status, http.StatusForbidden)
	}
	if rejected.URL != srv.URL {
		t.Fatalf("RejectedError.URL = %q, want %q", rejected.URL, srv.URL)
	}
}

func TestSend_408And429_RetriedThenSucceed(t *testing.T) {
	for _, code := range []int{http.StatusRequestTimeout, http.StatusTooManyRequests} {
		var hits int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if atomic.AddInt32(&hits, 1) < 3 {
				w.WriteHeader(code)
				return
			}
			w.WriteHeader(http.StatusCreated)
		}))
		res := Send(context.Background(), srv.Client(), &captureLog{}, func(time.Duration) {}, Request{
			URL:           srv.URL,
			Envelope:      []byte(`{}`),
			PublisherName: "test",
		})
		srv.Close()
		if res.Err != nil {
			t.Fatalf("status %d: Send after retries: %v", code, res.Err)
		}
		if res.Attempts != 3 {
			t.Fatalf("status %d: attempts = %d, want 3", code, res.Attempts)
		}
		if got := atomic.LoadInt32(&hits); got != 3 {
			t.Fatalf("status %d: hits = %d, want 3", code, got)
		}
	}
}

func TestSend_408And429_ExhaustionIsTransientNotRejected(t *testing.T) {
	for _, code := range []int{http.StatusRequestTimeout, http.StatusTooManyRequests} {
		var hits int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			atomic.AddInt32(&hits, 1)
			w.WriteHeader(code)
		}))
		log := &captureLog{}
		res := Send(context.Background(), srv.Client(), log, func(time.Duration) {}, Request{
			URL:           srv.URL,
			Envelope:      []byte(`{}`),
			PublisherName: "test",
		})
		srv.Close()
		if res.Err == nil {
			t.Fatalf("status %d: expected err on exhausted retries", code)
		}
		if res.Rejected {
			t.Fatalf("status %d is retryable and must not set Rejected", code)
		}
		var rejected *RejectedError
		if errors.As(res.Err, &rejected) {
			t.Fatalf("status %d exhaustion must be a transient error, got *RejectedError", code)
		}
		if res.Attempts != 3 {
			t.Fatalf("status %d: attempts = %d, want 3", code, res.Attempts)
		}
		if got := atomic.LoadInt32(&hits); got != 3 {
			t.Fatalf("status %d: hits = %d, want 3", code, got)
		}
		log.mu.Lock()
		msgs := append([]string(nil), log.msgs...)
		log.mu.Unlock()
		for _, m := range msgs {
			if m == "PUBLISHERKIT.MESSAGE.REJECTED" {
				t.Fatalf("status %d must not log PUBLISHERKIT.MESSAGE.REJECTED", code)
			}
		}
	}
}

func TestSend_5xx_ErrIsNotRejectedError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()
	res := Send(context.Background(), srv.Client(), &captureLog{}, func(time.Duration) {}, Request{
		URL:           srv.URL,
		Envelope:      []byte(`{}`),
		PublisherName: "test",
	})
	if res.Err == nil {
		t.Fatal("Send: expected err on exhausted retries")
	}
	var rejected *RejectedError
	if errors.As(res.Err, &rejected) {
		t.Fatal("5xx exhaustion must be a transient error, got *RejectedError")
	}
}

func TestSend_4xx_NilLoggerDoesNotPanic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	res := Send(context.Background(), srv.Client(), nil, func(time.Duration) {}, Request{
		URL:           srv.URL,
		Envelope:      []byte(`{}`),
		PublisherName: "test",
	})
	if !res.Rejected {
		t.Fatal("4xx must set Rejected=true even with a nil Logger")
	}
}

func TestShouldRetry_ContextCancelInterruptsSleep(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	slowSleep := func(time.Duration) {
		close(started)
		<-release
	}

	done := make(chan bool, 1)
	go func() {
		done <- shouldRetry(ctx, 1, 3, slowSleep, []time.Duration{5 * time.Second})
	}()

	<-started
	cancel()

	if got := <-done; got {
		t.Fatal("expected shouldRetry to return false once ctx is cancelled")
	}
}

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
		URL:           srv.URL,
		Envelope:      []byte(`{}`),
		PublisherName: "test",
	})
	if res.Err != nil {
		t.Fatalf("Send: %v", res.Err)
	}
	if res.Attempts != 2 {
		t.Fatalf("expected 2 attempts, got %d", res.Attempts)
	}
}
