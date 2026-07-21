// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

package publisherkit

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
	if len(log.msgs) != 1 || log.msgs[0] != "publisher.message.rejected" {
		t.Fatalf("expected publisher.message.rejected log, got %#v", log.msgs)
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
	slowSleep := func(time.Duration) {
		close(started)
		time.Sleep(10 * time.Second)
	}

	done := make(chan bool, 1)
	go func() {
		done <- shouldRetry(ctx, 1, 3, slowSleep, []time.Duration{5 * time.Second})
	}()

	<-started
	cancel()

	select {
	case got := <-done:
		if got {
			t.Fatal("expected shouldRetry to return false once ctx is cancelled")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("shouldRetry did not return promptly after ctx cancellation")
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
