// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"net"
	"strconv"
	"strings"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/runtime/peer"
)

func TestEffectiveCallbackHostPort(t *testing.T) {
	cases := []struct {
		name          string
		listenerAddr  string
		advertiseHost string
		advertisePort int
		wantHost      string
		wantPort      int
	}{
		{
			name:          "advertise host without port reuses bind port",
			listenerAddr:  "0.0.0.0:9100",
			advertiseHost: "rimsky-supervisor",
			wantHost:      "rimsky-supervisor",
			wantPort:      9100,
		},
		{
			name:          "advertise host and port both override",
			listenerAddr:  "0.0.0.0:9100",
			advertiseHost: "rimsky-supervisor",
			advertisePort: 9200,
			wantHost:      "rimsky-supervisor",
			wantPort:      9200,
		},
		{
			name:         "explicit non-wildcard bind host is a legal advertise fallback",
			listenerAddr: "10.1.2.3:9100",
			wantHost:     "10.1.2.3",
			wantPort:     9100,
		},
		{
			name:         "loopback bind host is a legal advertise fallback",
			listenerAddr: "127.0.0.1:9100",
			wantHost:     "127.0.0.1",
			wantPort:     9100,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			host, port, err := effectiveCallbackHostPort(tc.listenerAddr, tc.advertiseHost, tc.advertisePort)
			if err != nil {
				t.Fatalf("effectiveCallbackHostPort(%q, %q, %d) unexpected error: %v",
					tc.listenerAddr, tc.advertiseHost, tc.advertisePort, err)
			}
			if host != tc.wantHost || port != tc.wantPort {
				t.Fatalf("effectiveCallbackHostPort(%q, %q, %d) = (%q, %d); want (%q, %d)",
					tc.listenerAddr, tc.advertiseHost, tc.advertisePort, host, port, tc.wantHost, tc.wantPort)
			}
		})
	}
}

func TestEffectiveCallbackHostPort_FailsFastOnWildcardBindWithoutAdvertise(t *testing.T) {
	for _, listenerAddr := range []string{"0.0.0.0:9100", "[::]:9100"} {
		t.Run(listenerAddr, func(t *testing.T) {
			_, _, err := effectiveCallbackHostPort(listenerAddr, "", 0)
			if err == nil {
				t.Fatalf("effectiveCallbackHostPort(%q, \"\", 0): want startup error, got nil (would stamp an unreachable wildcard callback URL)", listenerAddr)
			}
			if !strings.Contains(err.Error(), "callback.advertise_host") ||
				!strings.Contains(err.Error(), "RIMSKY_SUPERVISOR_CALLBACK_ADVERTISE_HOST") {
				t.Fatalf("error must name callback.advertise_host and RIMSKY_SUPERVISOR_CALLBACK_ADVERTISE_HOST; got %q", err.Error())
			}
		})
	}
}

func TestAdvertisedURLMatchesPersistedHostPort(t *testing.T) {
	const (
		listenerAddr  = "0.0.0.0:9100"
		advertiseHost = "rimsky-supervisor"
		advertisePort = 9200
	)
	host, port, err := effectiveCallbackHostPort(listenerAddr, advertiseHost, advertisePort)
	if err != nil {
		t.Fatalf("effectiveCallbackHostPort: %v", err)
	}
	url := advertisedCallbackURL(host, port, peer.PeerAuthNone)
	want := "http://" + net.JoinHostPort(host, strconv.Itoa(port))
	if url != want {
		t.Fatalf("advertisedCallbackURL = %q; want %q (must match persisted host:port)", url, want)
	}
}

func TestAdvertisedURLUsesHTTPSUnderMTLS(t *testing.T) {
	url := advertisedCallbackURL("rimsky-supervisor", 9200, peer.PeerAuthMTLS)
	if !strings.HasPrefix(url, "https://") {
		t.Fatalf("advertisedCallbackURL under mtls = %q; want https:// scheme", url)
	}
}
