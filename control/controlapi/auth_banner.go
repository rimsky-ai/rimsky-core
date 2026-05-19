// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Anonymous-mode WARN banner. The control-api logs at WARN once at
// startup and every DefaultBannerInterval thereafter while no API
// keys exist; the banner stops once any key is provisioned. See spec
// section "Loud startup warning".
//
// @concept: anonymous-mode

package controlapi

import (
	"context"
	"time"
)

// DefaultBannerInterval is the production cadence between repeated
// WARN banners in anonymous mode.
const DefaultBannerInterval = 5 * time.Minute

// AnonymousModeBannerMessage is the message logged when the
// deployment is in anonymous mode. Exposed as a const so the L9
// scenario test can match on it.
const AnonymousModeBannerMessage = "ANONYMOUS MODE: no API keys provisioned; all requests treated as admin. Run 'rimsky auth init' to enable authentication."

// CheckAnonymousBanner queries the anonymous-mode predicate once. If
// true, logs the WARN banner and returns true; otherwise returns
// false and logs nothing. Exposed for tests to exercise banner
// emission without running the goroutine loop.
func CheckAnonymousBanner(ctx context.Context, s *AuthState) bool {
	anon, err := s.IsAnonymousMode(ctx)
	if err != nil {
		s.Logger.Error("auth.anonymous_mode.check_failed", "err", err.Error())
		return false
	}
	if anon {
		s.Logger.Warn("auth.anonymous_mode", "message", AnonymousModeBannerMessage)
	}
	return anon
}

// WatchAnonymousMode runs CheckAnonymousBanner once at startup and
// then on each tick of the supplied interval until ctx is cancelled.
// Intended to be started as a goroutine by control-api startup; tests
// pass a small interval to exercise the loop without timing flake.
func WatchAnonymousMode(ctx context.Context, s *AuthState, interval time.Duration) {
	if interval <= 0 {
		interval = DefaultBannerInterval
	}
	_ = CheckAnonymousBanner(ctx, s)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			_ = CheckAnonymousBanner(ctx, s)
		case <-ctx.Done():
			return
		}
	}
}
