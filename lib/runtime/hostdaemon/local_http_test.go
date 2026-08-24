// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package hostdaemon

import (
	"net"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

func TestEnrollListenerPinnedToLoopbackDespiteNonLoopbackListenAddr(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:0", "0.0.0.0:0", ""} {
		lis, base, err := bindPlaintextListener(addr)
		if err != nil {
			t.Fatalf("bindPlaintextListener(%q): %v", addr, err)
		}
		host, _, splitErr := net.SplitHostPort(lis.Addr().String())
		_ = lis.Close()
		if splitErr != nil {
			t.Fatalf("split bound addr %q: %v", lis.Addr().String(), splitErr)
		}
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			t.Fatalf("listen %q bound enroll listener to non-loopback host %q (base=%s)", addr, host, base)
		}
	}
}

func TestBootstrapTokenAcceptedRepeatedlyForRenewalWithinLifetime(t *testing.T) {
	a := newDaemon(Config{}, nil, "", "", nil)
	base := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	a.registerBootstrapToken("spawn-1", "secret", base)

	if p, ok := a.principalForBootstrapToken("secret", base); !ok || p != "spawn-1" {
		t.Fatalf("first enroll must be accepted: got (%q, %v)", p, ok)
	}
	if p, ok := a.principalForBootstrapToken("secret", base.Add(time.Hour)); !ok || p != "spawn-1" {
		t.Fatalf("renewal re-presenting the same token must be accepted: got (%q, %v)", p, ok)
	}
}

func TestBootstrapTokenRejectedPastItsLifetimeBound(t *testing.T) {
	a := newDaemon(Config{}, nil, "", "", nil)
	base := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	a.registerBootstrapToken("spawn-1", "secret", base)

	if _, ok := a.principalForBootstrapToken("secret", base.Add(bootstrapTokenTTL).Add(time.Second)); ok {
		t.Fatal("token presented past its lifetime bound must be rejected")
	}
}

func TestDeliverHTTPResponseNeverSendsOnClearedForward(t *testing.T) {
	a := newDaemon(Config{}, nil, "", "", nil)
	for i := 0; i < 10000; i++ {
		forwardID := strconv.Itoa(i)
		ch := a.registerForward(forwardID)

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			a.deliverHTTPResponse(&genv1.LocalHttpResponse{ForwardId: forwardID, Status: http.StatusOK})
		}()
		go func() {
			defer wg.Done()
			a.clearForward(forwardID)
		}()
		wg.Wait()

		a.forwardMu.Lock()
		_, stillPending := a.pendingForwards[forwardID]
		a.forwardMu.Unlock()
		if stillPending {
			t.Fatalf("iteration %d: forward %q still pending after deliver+clear", i, forwardID)
		}

		select {
		case resp, ok := <-ch:
			if ok && resp == nil {
				t.Fatalf("iteration %d: delivered response is nil", i)
			}
		default:
			t.Fatalf("iteration %d: channel neither closed nor delivered", i)
		}
	}
}
