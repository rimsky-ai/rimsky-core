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
			name:         "no advertise host falls back to bind host:port",
			listenerAddr: "0.0.0.0:9100",
			wantHost:     "0.0.0.0",
			wantPort:     9100,
		},
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
			name:         "ipv6 bind address splits cleanly",
			listenerAddr: "[::]:9100",
			wantHost:     "::",
			wantPort:     9100,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			host, port := effectiveCallbackHostPort(tc.listenerAddr, tc.advertiseHost, tc.advertisePort)
			if host != tc.wantHost || port != tc.wantPort {
				t.Fatalf("effectiveCallbackHostPort(%q, %q, %d) = (%q, %d); want (%q, %d)",
					tc.listenerAddr, tc.advertiseHost, tc.advertisePort, host, port, tc.wantHost, tc.wantPort)
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
	host, port := effectiveCallbackHostPort(listenerAddr, advertiseHost, advertisePort)
	url := advertisedCallbackURL(listenerAddr, advertiseHost, advertisePort, peer.PeerAuthNone)
	want := "http://" + net.JoinHostPort(host, strconv.Itoa(port))
	if url != want {
		t.Fatalf("advertisedCallbackURL = %q; want %q (must match persisted host:port)", url, want)
	}
}

func TestAdvertisedURLUsesHTTPSUnderMTLS(t *testing.T) {
	url := advertisedCallbackURL("0.0.0.0:9100", "rimsky-supervisor", 9200, peer.PeerAuthMTLS)
	if !strings.HasPrefix(url, "https://") {
		t.Fatalf("advertisedCallbackURL under mtls = %q; want https:// scheme", url)
	}
}
