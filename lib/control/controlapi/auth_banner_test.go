// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package controlapi

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func TestCheckAnonymousBanner_WarnsOnlyWhenAnonymous(t *testing.T) {
	t.Run("anonymous_mode_warns", func(t *testing.T) {
		h := newUnseededAuthTestHarness(t)
		mu := &sync.Mutex{}
		events := &[]captureEvent{}
		logger := captureLogger{mu: mu, events: events}
		h.state.Logger = logger

		anon := CheckAnonymousBanner(context.Background(), h.state)
		if !anon {
			t.Fatalf("CheckAnonymousBanner: got false want true (zero active keys)")
		}
		msg, ok := logger.fieldFor("AUTH.ANONYMOUSMODE.ACTIVE", "message")
		if !ok {
			t.Fatalf("no auth.anonymous_mode WARN emitted while in anonymous mode; events=%v", *events)
		}
		if msg != AnonymousModeBannerMessage {
			t.Fatalf("auth.anonymous_mode message field: got %q want %q", msg, AnonymousModeBannerMessage)
		}
	})

	t.Run("authenticated_mode_silent", func(t *testing.T) {
		h := newAuthTestHarness(t)
		mu := &sync.Mutex{}
		events := &[]captureEvent{}
		h.state.Logger = captureLogger{mu: mu, events: events}

		anon := CheckAnonymousBanner(context.Background(), h.state)
		if anon {
			t.Fatalf("CheckAnonymousBanner: got true want false (an active key is seeded)")
		}
		mu.Lock()
		defer mu.Unlock()
		for _, e := range *events {
			if e.msg == "AUTH.ANONYMOUSMODE.ACTIVE" {
				t.Fatalf("auth.anonymous_mode WARN emitted while an active key is present: %+v", e)
			}
		}
	})
}

type warnSignalLogger struct {
	ch chan struct{}
}

func (w warnSignalLogger) Debug(string, ...any) {}
func (w warnSignalLogger) Info(string, ...any)  {}
func (w warnSignalLogger) Warn(msg string, _ ...any) {
	if msg != "AUTH.ANONYMOUSMODE.ACTIVE" {
		return
	}
	select {
	case w.ch <- struct{}{}:
	default:
	}
}
func (w warnSignalLogger) Error(string, ...any)      {}
func (w warnSignalLogger) With(...any) shared.Logger { return w }

func TestWatchAnonymousMode_FiresOnCadenceNotJustOnce(t *testing.T) {
	h := newUnseededAuthTestHarness(t)
	ch := make(chan struct{}, 1)
	h.state.Logger = warnSignalLogger{ch: ch}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go WatchAnonymousMode(ctx, h.state, time.Millisecond)

	<-ch
	<-ch
	<-ch
}
