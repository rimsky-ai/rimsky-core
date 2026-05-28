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
		}
		return signalpkg.Signal{Type: signalpkg.TypePath("terminal/success"), Payload: payload}
	case terminalKindErrored:
		payload := t.Payload
		if payload == nil {
			payload = map[string]any{}
		}
		return signalpkg.Signal{
			Type:    signalpkg.TypePath("terminal/error/" + t.ErrorClass),
			Payload: payload,
		}
	case terminalKindPark:
		// Park reason maps to two leaves per concept:signal.
		if t.ParkReason == genv1.ParkReason_PARK_REASON_SNOOZE {
			return signalpkg.Signal{
				Type: signalpkg.TypePath("terminal/park/snooze"),
				Payload: map[string]any{
					"resume_at":           t.ParkResumeAt,
					"session_token":       t.ParkSessionToken,
					"park_payload":        t.ParkPayload,
					"parked_reason_label": t.ParkReasonLabel,
					"parked_reason_note":  t.ParkReasonNote,
				},
			}
		}
		// AWAIT_CALLBACK — resume_at may be zero; omit the key in that case
		// so the payload stays value-based (no pointer indirection mismatch
		// with the SNOOZE branch above, which always carries a `time.Time`
		// value under `resume_at`).
		payload := map[string]any{
			"session_token":       t.ParkSessionToken,
			"park_payload":        t.ParkPayload,
			"parked_reason_label": t.ParkReasonLabel,
			"parked_reason_note":  t.ParkReasonNote,
		}
		if !t.ParkResumeAt.IsZero() {
			payload["resume_at"] = t.ParkResumeAt
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
