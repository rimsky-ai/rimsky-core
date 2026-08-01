// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package main

import (
	"testing"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

func TestHTTPForwarder_BoundsConcurrentForwards(t *testing.T) {
	state := newProxyState()
	f := newHTTPForwarder(state)
	if cap(f.sem) != maxConcurrentForwards {
		t.Fatalf("sem capacity = %d, want %d", cap(f.sem), maxConcurrentForwards)
	}
	for i := 0; i < maxConcurrentForwards; i++ {
		f.sem <- struct{}{}
	}

	conn := newAgentConnection("agent-1", "label", "")
	fwd := &genv1.LocalHttpForward{ForwardId: "blocked", SpawnId: "no-such-spawn", Url: "/cb"}

	done := make(chan struct{})
	go func() {
		f.handle(conn, fwd)
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("handle() completed while the forward semaphore was fully saturated; concurrency is unbounded")
	default:
	}

	<-f.sem
	<-done
}
