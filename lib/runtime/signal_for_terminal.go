// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: breakpoint
// @concept: signal

package runtime

import (
	signalpkg "github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
)

func signalForTerminal(args RunArgs, acq *acquisition, t terminalEvent) signalpkg.Signal {
	switch t.Kind {
	case terminalKindComplete:
		return signalpkg.BuildTerminalSuccessSignal(t.Changed, t.AttributesDel, t.ChangeSummary, t.Tags)
	case terminalKindErrored, terminalKindInfra:
		if acq != nil && acq.RetryDecision != nil {
			return acq.RetryDecision.Signal
		}
		return signalpkg.BuildTerminalErrorSignal(t.ErrorClass, t.Payload, 0, 0, t.AttributesDel, t.Tags)
	case terminalKindPark:
		return parkTerminalSignal(t)
	}
	return signalpkg.Signal{}
}
