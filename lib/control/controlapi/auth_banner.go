// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: anonymous-mode

package controlapi

import (
	"context"
	"time"
)

const DefaultBannerInterval = 5 * time.Minute

const AnonymousModeBannerMessage = "ANONYMOUS MODE: no API keys provisioned; all requests treated as admin. Run 'rimsky auth init' to enable authentication."

func CheckAnonymousBanner(ctx context.Context, s *AuthState) bool {
	anon, err := s.IsAnonymousMode(ctx)
	if err != nil {
		s.Logger.Error("AUTH.ANONYMOUSMODE.CHECKFAILED", "err", err.Error())
		return false
	}
	if anon {
		s.Logger.Warn("AUTH.ANONYMOUSMODE.ACTIVE", "message", AnonymousModeBannerMessage)
	}
	return anon
}

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
