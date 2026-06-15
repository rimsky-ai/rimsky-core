// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"net"
	"strconv"
	"testing"
)

// TestEffectiveCallbackHostPort is the single source of truth for both the
// callback_url handed to executors and the host:port persisted to
// rimsky_supervisors. These cases pin the preference order and guard the
// invariant that the persisted row carries a dialable address (never the
// 0.0.0.0 bind host) when an advertise host is configured.
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

// TestAdvertisedURLMatchesPersistedHostPort URL handed to executors and the host:port written to the supervisor
// row must agree — they are derived from the same helper, so a peer reading
// the row reconstructs the exact callback base URL.
func TestAdvertisedURLMatchesPersistedHostPort(t *testing.T) {
	const (
		listenerAddr  = "0.0.0.0:9100"
		advertiseHost = "rimsky-supervisor"
		advertisePort = 9200
	)
	host, port := effectiveCallbackHostPort(listenerAddr, advertiseHost, advertisePort)
	url := advertisedCallbackURL(listenerAddr, advertiseHost, advertisePort)
	want := "http://" + net.JoinHostPort(host, strconv.Itoa(port))
	if url != want {
		t.Fatalf("advertisedCallbackURL = %q; want %q (must match persisted host:port)", url, want)
	}
}
