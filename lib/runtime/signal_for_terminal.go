// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// signal_for_terminal.go — translates a runner-internal `terminalEvent`
// into the `signal.Signal` envelope the after_terminal breakpoint
// checkpoint matches against (per concept:breakpoint + concept:signal).
//
// The signal-type taxonomy comes from concept:signal — the leaves used
// here mirror what `runtime/runner_terminal.go::applyTerminalComplete`,
// `runtime/runner_error_policy.go::applyTerminalInfraError`, and
// `runtime/runner_terminal_park.go::parkTerminalSignal` emit at audit
// time. NO event row is written here; the audit-emit pathway runs
// independently inside `applyTerminal`. This helper only constructs
// the envelope for matcher evaluation.
//
// @concept: breakpoint
// @concept: signal

package runtime

import (
	signalpkg "github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// signalForTerminal returns the signal.Signal envelope that describes
// the just-applied terminal. Used by after_terminal breakpoint
// evaluation to populate CheckpointContext.TerminalSignal so the
// matcher's signal_type prefix filter (spec §4.5) can fire.
//
// Mapping mirrors the canonical audit-emit sites:
//   - Complete                       → terminal/success
//   - Errored{ErrorClass}            → terminal/error/<class>
//   - Park{ParkReason=SNOOZE}        → terminal/park/snooze
//   - Park{ParkReason=AWAIT_CALLBACK}→ terminal/park/await_callback
//   - Infra{ErrorClass}              → terminal/infra/<class>
//
// Unknown/zero kinds return an empty Signal — the matcher's HasPrefix
// against an empty type returns false for any non-empty prefix, so an
// unmatched terminal won't accidentally fire a breakpoint.
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
		// @deliberate: Per TD-remove-resume-context, session_token and
		// park_payload no longer ride on the Park signal payload —
		// resume state lives in attribute carry-forward. The payload
		// surfaces park reason metadata + the verdict's tags.
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
