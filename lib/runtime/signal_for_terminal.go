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
		payload := map[string]any{
			"changed":          t.Changed,
			"attributes_delta": orEmptyMap(t.AttributesDel),
			"change_summary":   t.ChangeSummary,
			"tags":             t.Tags,
		}
		return signalpkg.Signal{Type: signalpkg.TypePath("terminal/success"), Payload: payload}
	case terminalKindErrored:
		payload := t.Payload
		if payload == nil {
			payload = map[string]any{}
		}
		payload["tags"] = t.Tags
		return signalpkg.Signal{
			Type:    signalpkg.TypePath("terminal/error/" + t.ErrorClass),
			Payload: payload,
		}
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
				Type:    signalpkg.TypePath("terminal/park/snooze"),
				Payload: payload,
			}
		}
		return signalpkg.Signal{
			Type:    signalpkg.TypePath("terminal/park/await_callback"),
			Payload: payload,
		}
	case terminalKindInfra:
		payload := map[string]any{"reason": t.ErrorClass}
		if t.Payload != nil {
			payload["details"] = t.Payload
		}
		return signalpkg.Signal{
			Type:    signalpkg.TypePath("terminal/infra/" + t.ErrorClass),
			Payload: payload,
		}
	}
	return signalpkg.Signal{}
}
