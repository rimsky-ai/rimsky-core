// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package shared

import (
	"os"
)

func WaitForSignalOrFailure(log Logger, sigCh <-chan os.Signal, failCh <-chan error) error {
	select {
	case s := <-sigCh:
		log.Info("PROCESS.SIGNAL.RECEIVED", "signal", s.String())
		return nil
	case err := <-failCh:
		log.Error("PROCESS.ROLE.FAILED", "error", err.Error())
		return err
	}
}
