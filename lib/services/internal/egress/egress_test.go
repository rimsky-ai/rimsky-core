// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package egress

import (
	"net/http"
	"testing"
	"time"
)

func TestGuardBlocksNonPublicByDefault(t *testing.T) {
	g := Guard{}
	blocked := []string{
		"127.0.0.1:80",
		"169.254.169.254:80",
		"10.0.0.5:443",
		"192.168.1.1:80",
		"172.16.0.1:80",
		"[::1]:80",
		"0.0.0.0:80",
		"[fd00::1]:80",
		"[fe80::1]:80",
		"[::ffff:127.0.0.1]:80",
	}
	for _, addr := range blocked {
		if err := g.CheckAddr(addr); err == nil {
			t.Errorf("expected %s to be blocked by default", addr)
		}
	}
}

func TestGuardAllowsPublic(t *testing.T) {
	g := Guard{}
	for _, addr := range []string{"1.1.1.1:443", "8.8.8.8:53", "[2606:4700:4700::1111]:443"} {
		if err := g.CheckAddr(addr); err != nil {
			t.Errorf("expected %s allowed, got %v", addr, err)
		}
	}
}

func TestGuardAllowlistException(t *testing.T) {
	g, err := NewGuard([]string{"10.0.0.0/8", "169.254.169.254"})
	if err != nil {
		t.Fatal(err)
	}
	for _, addr := range []string{"10.1.2.3:80", "169.254.169.254:80"} {
		if err := g.CheckAddr(addr); err != nil {
			t.Errorf("allowlisted %s should pass, got %v", addr, err)
		}
	}
	if err := g.CheckAddr("192.168.0.1:80"); err == nil {
		t.Error("192.168.0.1 is not allowlisted; expected block")
	}
}

func TestNewGuardRejectsMalformed(t *testing.T) {
	if _, err := NewGuard([]string{"not-a-cidr"}); err == nil {
		t.Fatal("expected error for malformed allowlist entry")
	}
}

func TestHTTPClientRejectsNonHTTPScheme(t *testing.T) {
	c := Guard{}.HTTPClient(time.Second)
	req, err := http.NewRequest(http.MethodGet, "file:///etc/passwd", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Do(req); err == nil {
		t.Fatal("expected non-http scheme to be rejected")
	}
}
