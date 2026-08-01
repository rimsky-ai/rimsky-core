// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package httpnode

import "time"

type gracefulStopper interface {
	GracefulStop()
	Stop()
}

func GracefulStopWithDeadline(srv gracefulStopper, grace time.Duration) {
	done := make(chan struct{})
	go func() {
		srv.GracefulStop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(grace):
		srv.Stop()
		<-done
	}
}
