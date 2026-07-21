// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package egress

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"syscall"
	"time"
)

type Guard struct {
	allow []*net.IPNet
}

func NewGuardFromEnv(envVar string) (Guard, error) {
	raw := strings.TrimSpace(os.Getenv(envVar))
	if raw == "" {
		return Guard{}, nil
	}
	return NewGuard(strings.Split(raw, ","))
}

func NewGuard(entries []string) (Guard, error) {
	var allow []*net.IPNet
	for _, part := range entries {
		entry := strings.TrimSpace(part)
		if entry == "" {
			continue
		}
		if !strings.Contains(entry, "/") {
			if ip := net.ParseIP(entry); ip != nil {
				bits := 32
				if ip.To4() == nil {
					bits = 128
				}
				entry = fmt.Sprintf("%s/%d", entry, bits)
			}
		}
		_, cidr, err := net.ParseCIDR(entry)
		if err != nil {
			return Guard{}, fmt.Errorf("egress allowlist: invalid entry %q: %w", part, err)
		}
		allow = append(allow, cidr)
	}
	return Guard{allow: allow}, nil
}

func (g Guard) allowlisted(ip net.IP) bool {
	for _, n := range g.allow {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

func nonPublic(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified()
}

func (g Guard) CheckAddr(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		host = address
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("egress: cannot parse dial address %q", address)
	}
	if nonPublic(ip) && !g.allowlisted(ip) {
		return fmt.Errorf("egress: destination %s is in a blocked range (loopback/private/link-local/metadata) and not in the operator egress allowlist", ip)
	}
	return nil
}

func (g Guard) HTTPClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			return g.CheckAddr(address)
		},
	}
	base := &http.Transport{
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	return &http.Client{
		Timeout:       timeout,
		Transport:     schemeGuard{base: base},
		CheckRedirect: stripHeadersOnCrossOriginRedirect,
	}
}

const maxRedirects = 10

func stripHeadersOnCrossOriginRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return fmt.Errorf("egress: stopped after %d redirects", maxRedirects)
	}
	if req.URL.Host != via[0].URL.Host || req.URL.Scheme != via[0].URL.Scheme {
		req.Header = make(http.Header)
	}
	return nil
}

type schemeGuard struct{ base http.RoundTripper }

func (t schemeGuard) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Scheme != "http" && r.URL.Scheme != "https" {
		return nil, fmt.Errorf("egress: scheme %q not permitted (only http/https)", r.URL.Scheme)
	}
	return t.base.RoundTrip(r)
}
