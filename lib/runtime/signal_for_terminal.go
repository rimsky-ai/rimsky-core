// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: breakpoint
// @concept: signal

package runtime

import (
	signalpkg "github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
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
		payload := map[string]any{
			"parked_reason_label": t.ParkReasonLabel,
			"parked_reason_note":  t.ParkReasonNote,
			"tags":                t.Tags,
		}
		if !t.ParkResumeAt.IsZero() {
			payload["resume_at"] = t.ParkResumeAt
		}
		if t.ParkReason == genv1.ParkReason_PARK_REASON_SNOOZE {
			return signalpkg.Signal{
				Type:    signalpkg.TypePath("transient/park/snooze"),
				Payload: payload,
			}
		}
		return signalpkg.Signal{
			Type:    signalpkg.TypePath("transient/park/await_callback"),
			Payload: payload,
		}
	case terminalKindInfra:
		return signalpkg.Signal{}
	}
	return signalpkg.Signal{}
}
