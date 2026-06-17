// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package signal

import (
	"reflect"
	"strings"
	"time"
)

// TerminalSuccessPayload is the payload schema for terminal/success.
//
// @deliberate: the signal envelope's outer field is Payload. To
// avoid the bare-`payload` collision when a signal's payload itself
// wraps an opaque sub-object originally named `payload` on the wire
// (executor Error.payload, message envelope payload), the inner
// field is renamed with a domain prefix: error_payload,
// message_payload. This is a rimsky-side rename only; the wire
// protos keep their original field names.
//
// @constraint: Tags rides on every settling terminal under
// TD-collapse-named-event-to-tags — subscribers fire on
// `terminal/success when: "<tag>" in payload.tags`, replacing the
// retired `event/<name>` source-kind. The list is deduplicated at
// emit; CEL evaluates list membership directly.
//
// @concept: terminal-tag
type TerminalSuccessPayload struct {
	Changed         bool           `json:"changed"`
	AttributesDelta map[string]any `json:"attributes_delta"`
	ChangeSummary   string         `json:"change_summary,omitempty"`
	Tags            []string       `json:"tags,omitempty"`
}

// TerminalErrorPayload is the payload schema for terminal/error/<class>.
//
// @constraint: Tags mirrors the TerminalSuccessPayload field —
// subscribers can `when: "<tag>" in payload.tags`-filter against an
// errored terminal just as against a successful one.
//
// @concept: terminal-tag
type TerminalErrorPayload struct {
	ErrorClass   string         `json:"error_class"`
	ErrorPayload map[string]any `json:"error_payload,omitempty"`
	Attempt      int            `json:"attempt"`
	RetriesSoFar int            `json:"retries_so_far"`
	Tags         []string       `json:"tags,omitempty"`
}

// TerminalParkSnoozePayload is the payload schema for
// terminal/park/snooze.
//
// @constraint: Tags rides per TD-collapse-named-event-to-tags; resume
// state lives in attribute carry-forward (per TD-remove-resume-context),
// not on the Park payload.
//
// @concept: terminal-tag
type TerminalParkSnoozePayload struct {
	ResumeAt          time.Time `json:"resume_at"`
	ParkedReasonLabel string    `json:"parked_reason_label,omitempty"`
	ParkedReasonNote  string    `json:"parked_reason_note,omitempty"`
	Tags              []string  `json:"tags,omitempty"`
}

// TerminalParkAwaitCallbackPayload is the payload schema for
// terminal/park/await_callback.
//
// @constraint: Tags rides per TD-collapse-named-event-to-tags; resume
// state lives in attribute carry-forward (per TD-remove-resume-context),
// not on the Park payload.
//
// @concept: terminal-tag
type TerminalParkAwaitCallbackPayload struct {
	ResumeAt          *time.Time `json:"resume_at,omitempty"`
	ParkedReasonLabel string     `json:"parked_reason_label,omitempty"`
	ParkedReasonNote  string     `json:"parked_reason_note,omitempty"`
	Tags              []string   `json:"tags,omitempty"`
}

// TerminalInfraPayload is the payload schema for terminal/infra/<reason>.
type TerminalInfraPayload struct {
	Reason          string         `json:"reason"`
	LastHeartbeatAt *time.Time     `json:"last_heartbeat_at,omitempty"`
	Details         map[string]any `json:"details,omitempty"`
}

// TransientRetryPayload is the payload schema for
// transient/retry/<attempt>/<error_class>.
type TransientRetryPayload struct {
	Attempt         int            `json:"attempt"`
	Cap             int            `json:"cap"`
	ErrorClass      string         `json:"error_class"`
	DiscardedClaims bool           `json:"discarded_claims"`
	DelayMs         int            `json:"delay_ms"`
	ErrorPayload    map[string]any `json:"error_payload,omitempty"`
}

// TransientAwaitAsyncPayload is the payload schema for
// transient/await_async.
type TransientAwaitAsyncPayload struct {
	AsyncAckID  string `json:"async_ack_id"`
	CallbackURL string `json:"callback_url"`
}

// AttributeChangedPayload is the payload schema for
// attribute/<key>/changed.
type AttributeChangedPayload struct {
	Key      string `json:"key"`
	Value    any    `json:"value"`
	OldValue any    `json:"old_value,omitempty"`
}

// PayloadSchemaForType returns the Go reflect.Type of the payload
// struct that matches the given exact TypePath, or (nil, false) when
// the path is a prefix pattern (trailing "*") — prefix subscriptions
// bind payload as CEL `dyn` per the spec.
//
// The mapping is exhaustive over the canonical taxonomy enumerated in
// taxonomy.go; an unrecognized exact path returns (nil, false).
//
// @deliberate: the historic `event/<name>` branch is gone per
// TD-collapse-named-event-to-tags — observable non-terminal
// transitions ride as tags on the settling terminal verdict
// (concept:terminal-tag), not as their own signal kind.
func PayloadSchemaForType(t TypePath) (reflect.Type, bool) {
	s := string(t)
	if s == "" || strings.HasSuffix(s, "*") {
		return nil, false
	}
	switch {
	case s == "terminal/success":
		return reflect.TypeOf(TerminalSuccessPayload{}), true
	case s == "terminal/park/snooze":
		return reflect.TypeOf(TerminalParkSnoozePayload{}), true
	case s == "terminal/park/await_callback":
		return reflect.TypeOf(TerminalParkAwaitCallbackPayload{}), true
	case s == "transient/await_async":
		return reflect.TypeOf(TransientAwaitAsyncPayload{}), true
	case strings.HasPrefix(s, "terminal/error/"):
		return reflect.TypeOf(TerminalErrorPayload{}), true
	case strings.HasPrefix(s, "terminal/infra/"):
		return reflect.TypeOf(TerminalInfraPayload{}), true
	case strings.HasPrefix(s, "transient/retry/"):
		return reflect.TypeOf(TransientRetryPayload{}), true
	case strings.HasPrefix(s, "attribute/") && strings.HasSuffix(s, "/changed"):
		return reflect.TypeOf(AttributeChangedPayload{}), true
	}
	return nil, false
}
