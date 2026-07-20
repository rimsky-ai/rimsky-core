// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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
		var errorPayload map[string]any
		if t.Payload != nil {
			if raw, ok := t.Payload["payload"].(map[string]any); ok {
				errorPayload = raw
			}
		}
		return signalpkg.BuildTerminalErrorSignal(t.ErrorClass, errorPayload, 0, 0, t.AttributesDel, t.Tags)
	case terminalKindPark:
		return parkTerminalSignal(args, t)
	}
	return signalpkg.Signal{}
}
