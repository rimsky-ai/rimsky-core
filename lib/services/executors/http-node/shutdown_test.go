// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package httpnode

import (
	"testing"
	"time"
)

type fakeGracefulStopper struct {
	stopCalled    chan struct{}
	stopWasForced bool
}

func newFakeGracefulStopper() *fakeGracefulStopper {
	return &fakeGracefulStopper{stopCalled: make(chan struct{})}
}

func (f *fakeGracefulStopper) GracefulStop() {
	<-f.stopCalled
}

func (f *fakeGracefulStopper) Stop() {
	f.stopWasForced = true
	close(f.stopCalled)
}

func TestGracefulStopWithDeadline_ForcesStopAfterGraceOnHungRPC(t *testing.T) {
	srv := newFakeGracefulStopper()
	GracefulStopWithDeadline(srv, 20*time.Millisecond)
	if !srv.stopWasForced {
		t.Fatal("expected Stop() to be called after the grace period elapsed on a server whose GracefulStop never returns on its own")
	}
}

func TestGracefulStopWithDeadline_ReturnsWithoutForcingWhenGracefulStopFinishes(t *testing.T) {
	srv := newFakeGracefulStopper()
	close(srv.stopCalled)
	GracefulStopWithDeadline(srv, 24*time.Hour)
	if srv.stopWasForced {
		t.Fatal("expected Stop() not to be called when GracefulStop finishes on its own")
	}
}
