// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package events_test

import (
	"errors"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// TestOperationalKindRoundTrip exercises the typed → wire → typed
// round-trip for every operational kind in the catalog. The
// load-bearing property: a wire form built by Kind.String() parses
// back to a Kind that compares equal (same family + same enum value)
// to the original. Catching a regression here means an emit-site
// kind would land in the column under one spelling and read back as
// a different operational name (or fall to ErrUnknownKind).
func TestOperationalKindRoundTrip(t *testing.T) {
	// @deliberate: every operational kind currently emitted is held
	// literal here (not pulled from AllOperationalKinds) so a
	// regression that drops an entry from the canonical map shows up
	// as a missing case in this list, not a silently-empty round-trip
	// pass.
	cases := []struct {
		name string
		op   genv1.OperationalKind
		wire string
	}{
		{"auth.access_attempted", genv1.OperationalKind_OPERATIONAL_KIND_AUTH_ACCESS_ATTEMPTED, "auth.access_attempted"},
		{"auth.access_denied", genv1.OperationalKind_OPERATIONAL_KIND_AUTH_ACCESS_DENIED, "auth.access_denied"},
		{"auth.key_created", genv1.OperationalKind_OPERATIONAL_KIND_AUTH_KEY_CREATED, "auth.key_created"},
		{"auth.key_revoked", genv1.OperationalKind_OPERATIONAL_KIND_AUTH_KEY_REVOKED, "auth.key_revoked"},
		{"auth.key_rotated", genv1.OperationalKind_OPERATIONAL_KIND_AUTH_KEY_ROTATED, "auth.key_rotated"},
		{"state_transition", genv1.OperationalKind_OPERATIONAL_KIND_STATE_TRANSITION, "state_transition"},
		{"work_started", genv1.OperationalKind_OPERATIONAL_KIND_WORK_STARTED, "work_started"},
		{"work_completed", genv1.OperationalKind_OPERATIONAL_KIND_WORK_COMPLETED, "work_completed"},
		{"work_rejected", genv1.OperationalKind_OPERATIONAL_KIND_WORK_REJECTED, "work_rejected"},
		{"heartbeat_lost", genv1.OperationalKind_OPERATIONAL_KIND_HEARTBEAT_LOST, "heartbeat_lost"},
		{"no_op_commit", genv1.OperationalKind_OPERATIONAL_KIND_NO_OP_COMMIT, "no_op_commit"},
		{"operator_override", genv1.OperationalKind_OPERATIONAL_KIND_OPERATOR_OVERRIDE, "operator_override"},
		{"unresolved_executor", genv1.OperationalKind_OPERATIONAL_KIND_UNRESOLVED_EXECUTOR, "unresolved_executor"},
		{"instance_terminated", genv1.OperationalKind_OPERATIONAL_KIND_INSTANCE_TERMINATED, "instance_terminated"},
		{"error", genv1.OperationalKind_OPERATIONAL_KIND_ERROR, "error"},
		{"lock_acquired", genv1.OperationalKind_OPERATIONAL_KIND_LOCK_ACQUIRED, "lock_acquired"},
		{"lock_released", genv1.OperationalKind_OPERATIONAL_KIND_LOCK_RELEASED, "lock_released"},
		{"lock_orphan_reaped", genv1.OperationalKind_OPERATIONAL_KIND_LOCK_ORPHAN_REAPED, "lock_orphan_reaped"},
		{"claim_acquired", genv1.OperationalKind_OPERATIONAL_KIND_CLAIM_ACQUIRED, "claim_acquired"},
		{"claim_held", genv1.OperationalKind_OPERATIONAL_KIND_CLAIM_HELD, "claim_held"},
		{"claim_resolved", genv1.OperationalKind_OPERATIONAL_KIND_CLAIM_RESOLVED, "claim_resolved"},
		{"orphaned_claim_released", genv1.OperationalKind_OPERATIONAL_KIND_ORPHANED_CLAIM_RELEASED, "orphaned_claim_released"},
		{"orphaned_claim_lost_race", genv1.OperationalKind_OPERATIONAL_KIND_ORPHANED_CLAIM_LOST_RACE, "orphaned_claim_lost_race"},
		{"claim_resolution.commit", genv1.OperationalKind_OPERATIONAL_KIND_CLAIM_RESOLUTION_COMMIT, "claim_resolution.commit"},
		{"claim_resolution.abandon", genv1.OperationalKind_OPERATIONAL_KIND_CLAIM_RESOLUTION_ABANDON, "claim_resolution.abandon"},
		{"attributes_substituted", genv1.OperationalKind_OPERATIONAL_KIND_ATTRIBUTES_SUBSTITUTED, "attributes_substituted"},
		{"attributes_committed", genv1.OperationalKind_OPERATIONAL_KIND_ATTRIBUTES_COMMITTED, "attributes_committed"},
		{"attributes_validation_failed", genv1.OperationalKind_OPERATIONAL_KIND_ATTRIBUTES_VALIDATION_FAILED, "attributes_validation_failed"},
		{"attributes_schema_failed", genv1.OperationalKind_OPERATIONAL_KIND_ATTRIBUTES_SCHEMA_FAILED, "attributes_schema_failed"},
		{"template_resolution_failed", genv1.OperationalKind_OPERATIONAL_KIND_TEMPLATE_RESOLUTION_FAILED, "template_resolution_failed"},
		{"template_validation_failed", genv1.OperationalKind_OPERATIONAL_KIND_TEMPLATE_VALIDATION_FAILED, "template_validation_failed"},
		{"executor_schema_unavailable", genv1.OperationalKind_OPERATIONAL_KIND_EXECUTOR_SCHEMA_UNAVAILABLE, "executor_schema_unavailable"},
		{"breakpoint.hit", genv1.OperationalKind_OPERATIONAL_KIND_BREAKPOINT_HIT, "breakpoint.hit"},
		{"message_emitted", genv1.OperationalKind_OPERATIONAL_KIND_MESSAGE_EMITTED, "message_emitted"},
		{"message_received", genv1.OperationalKind_OPERATIONAL_KIND_MESSAGE_RECEIVED, "message_received"},
		{"fan_out_dispatched", genv1.OperationalKind_OPERATIONAL_KIND_FAN_OUT_DISPATCHED, "fan_out_dispatched"},
		{"fanout.children_created", genv1.OperationalKind_OPERATIONAL_KIND_FANOUT_CHILDREN_CREATED, "fanout.children_created"},
		{"subclaim.begin_candidate", genv1.OperationalKind_OPERATIONAL_KIND_SUBCLAIM_BEGIN_CANDIDATE, "subclaim.begin_candidate"},
		{"subclaim.acquired", genv1.OperationalKind_OPERATIONAL_KIND_SUBCLAIM_ACQUIRED, "subclaim.acquired"},
		{"subgraph_internal_cascade_fired", genv1.OperationalKind_OPERATIONAL_KIND_SUBGRAPH_INTERNAL_CASCADE_FIRED, "subgraph_internal_cascade_fired"},
		{"subgraph.dispatched", genv1.OperationalKind_OPERATIONAL_KIND_SUBGRAPH_DISPATCHED, "subgraph.dispatched"},
		{"subgraph.exit_carry", genv1.OperationalKind_OPERATIONAL_KIND_SUBGRAPH_EXIT_CARRY, "subgraph.exit_carry"},
		{"park_timeout", genv1.OperationalKind_OPERATIONAL_KIND_PARK_TIMEOUT, "park_timeout"},
		{"parked_resume_started", genv1.OperationalKind_OPERATIONAL_KIND_PARKED_RESUME_STARTED, "parked_resume_started"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			k := events.OperationalKindFromProto(c.op)
			if got := k.String(); got != c.wire {
				t.Fatalf("String() = %q, want %q", got, c.wire)
			}
			back, err := events.ParseKindString(c.wire)
			if err != nil {
				t.Fatalf("ParseKindString(%q): %v", c.wire, err)
			}
			if back.Family() != events.FamilyOperational {
				t.Fatalf("Family() = %v, want FamilyOperational", back.Family())
			}
			if back.OperationalKind() != c.op {
				t.Fatalf("OperationalKind() = %v, want %v", back.OperationalKind(), c.op)
			}
			if back.String() != c.wire {
				t.Fatalf("round-trip String() = %q, want %q", back.String(), c.wire)
			}
		})
	}
}

// TestSignalKindRoundTrip exercises the signal-class round-trip:
// type-path → wire string → SignalKind, recovered SignalPath equal
// to the input. ParseKindString uses the "/" character to
// disambiguate signal from operational.
func TestSignalKindRoundTrip(t *testing.T) {
	cases := []string{
		"terminal/success",
		"terminal/error/http/timeout",
		"terminal/park/snooze",
		"terminal/park/await_callback",
		"terminal/infra/scheduler_killed",
		"transient/retry/3/agent/rate_limited",
		"transient/heartbeat_missed",
		"transient/await_async",
		"attribute/budget_cents/changed",
		"event/discovered",
	}
	for _, path := range cases {
		path := path
		t.Run(path, func(t *testing.T) {
			k := events.SignalKind(path)
			if k.Family() != events.FamilySignal {
				t.Fatalf("Family() = %v, want FamilySignal", k.Family())
			}
			if k.SignalPath() != path {
				t.Fatalf("SignalPath() = %q, want %q", k.SignalPath(), path)
			}
			if k.String() != path {
				t.Fatalf("String() = %q, want %q", k.String(), path)
			}
			back, err := events.ParseKindString(path)
			if err != nil {
				t.Fatalf("ParseKindString(%q): %v", path, err)
			}
			if back.Family() != events.FamilySignal {
				t.Fatalf("round-trip Family() = %v, want FamilySignal", back.Family())
			}
			if back.SignalPath() != path {
				t.Fatalf("round-trip SignalPath() = %q, want %q", back.SignalPath(), path)
			}
		})
	}
}

// TestParseKindStringErrors covers the rejection paths: empty input,
// a malformed operational-style string that doesn't match the
// canonical catalog AND has no slash (so it doesn't pretend to be a
// signal type-path either).
func TestParseKindStringErrors(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"unknown_operational", "totally_made_up_kind"},
		{"unknown_operational_dotted", "foo.bar.baz"},
		{"unknown_operational_typo", "state_transitionn"},
		{"unknown_operational_close_typo", "auth.access_attemped"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			_, err := events.ParseKindString(c.in)
			if err == nil {
				t.Fatalf("ParseKindString(%q) returned nil error, want ErrUnknownKind", c.in)
			}
			if !errors.Is(err, events.ErrUnknownKind) {
				t.Fatalf("ParseKindString(%q) error = %v, want ErrUnknownKind", c.in, err)
			}
		})
	}
}

// TestSignalKindPanicsOnEmpty exercises the construction-side guard:
// an empty signal type-path is a caller bug (an empty wire string
// indistinguishable from "no kind"), so SignalKind panics rather
// than persist a zero-equivalent value.
func TestSignalKindPanicsOnEmpty(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("SignalKind(\"\") did not panic")
		}
	}()
	_ = events.SignalKind("")
}

// TestZeroKindIsZero confirms the zero-value detection helper.
func TestZeroKindIsZero(t *testing.T) {
	var k events.Kind
	if !k.IsZero() {
		t.Fatal("zero Kind.IsZero() = false, want true")
	}
	if k.String() != "" {
		t.Fatalf("zero Kind.String() = %q, want \"\"", k.String())
	}
	op := events.KindStateTransition()
	if op.IsZero() {
		t.Fatal("op.IsZero() = true on operational kind")
	}
	sig := events.SignalKind("terminal/success")
	if sig.IsZero() {
		t.Fatal("sig.IsZero() = true on signal kind")
	}
}

// TestAllOperationalKindsMatchesCatalog asserts the AllOperationalKinds
// helper returns exactly the set of canonical operational wire forms
// that ParseKindString recognizes. If an entry drops out of the
// canonical map (e.g. a refactor accident), it disappears from the
// audit-read allowlist this helper backs, and operators lose audit
// visibility on that kind. This test catches that regression.
func TestAllOperationalKindsMatchesCatalog(t *testing.T) {
	all := events.AllOperationalKinds()
	for _, name := range all {
		k, err := events.ParseKindString(name)
		if err != nil {
			t.Fatalf("AllOperationalKinds returned %q which ParseKindString rejects: %v", name, err)
		}
		if k.Family() != events.FamilyOperational {
			t.Fatalf("AllOperationalKinds returned %q which parses as non-operational %v", name, k.Family())
		}
	}
}
