// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: breakpoint
// @concept: signal

package runtime

import (
	signalpkg "github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
)

func signalForTerminal(t terminalEvent) signalpkg.Signal {
	switch t.Kind {
	case terminalKindComplete:
		return signalpkg.BuildTerminalSuccessSignal(t.Changed, t.AttributesDel, t.ChangeSummary, t.Tags)
	case terminalKindErrored:
		var errorPayload map[string]any
		if t.Payload != nil {
			if raw, ok := t.Payload["payload"].(map[string]any); ok {
				errorPayload = raw
			}
		}
		return signalpkg.BuildTerminalErrorSignal(t.ErrorClass, errorPayload, 0, 0, t.AttributesDel, t.Tags)
	case terminalKindPark:
		return parkTerminalSignal(t)
	case terminalKindInfra:
		return signalpkg.Signal{}
	}
	return signalpkg.Signal{}
}
