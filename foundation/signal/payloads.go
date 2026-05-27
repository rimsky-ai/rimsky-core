// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package signal

import (
	"reflect"
	"strings"
	"time"

	foundationshared "github.com/rimsky-ai/rimsky-core/foundation/shared"
)

// Field-naming convention: the signal envelope's outer field is
// Payload. To avoid the bare-`payload` collision when a signal's
// payload itself wraps an opaque sub-object originally named `payload`
// on the wire (executor Error.payload, NamedEvent.payload,
// Park.payload, message envelope payload), the inner field is renamed
// with a domain prefix: error_payload, event_payload, park_payload,
// message_payload. This is a rimsky-side rename only; the wire
// protos keep their original field names.

// TerminalSuccessPayload is the payload schema for terminal/success.
type TerminalSuccessPayload struct {
	Changed         bool           `json:"changed"`
	AttributesDelta map[string]any `json:"attributes_delta"`
	ChangeSummary   string         `json:"change_summary,omitempty"`
}

// TerminalErrorPayload is the payload schema for terminal/error/<class>.
type TerminalErrorPayload struct {
	ErrorClass   string         `json:"error_class"`
	ErrorPayload map[string]any `json:"error_payload,omitempty"`
	Attempt      int            `json:"attempt"`
	RetriesSoFar int            `json:"retries_so_far"`
}

// TerminalParkSnoozePayload is the payload schema for
// terminal/park/snooze.
type TerminalParkSnoozePayload struct {
	ResumeAt          time.Time `json:"resume_at"`
	SessionToken      string    `json:"session_token,omitempty"`
	ParkPayload       []byte    `json:"park_payload,omitempty"`
	ParkedReasonLabel string    `json:"parked_reason_label,omitempty"`
	ParkedReasonNote  string    `json:"parked_reason_note,omitempty"`
}

// TerminalParkAwaitCallbackPayload is the payload schema for
// terminal/park/await_callback.
type TerminalParkAwaitCallbackPayload struct {
	ResumeAt          *time.Time `json:"resume_at,omitempty"`
	SessionToken      string     `json:"session_token,omitempty"`
	ParkPayload       []byte     `json:"park_payload,omitempty"`
	ParkedReasonLabel string     `json:"parked_reason_label,omitempty"`
	ParkedReasonNote  string     `json:"parked_reason_note,omitempty"`
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

// TransientHeartbeatMissedPayload is the payload schema for
// transient/heartbeat_missed.
type TransientHeartbeatMissedPayload struct {
	LastHeartbeatAt time.Time             `json:"last_heartbeat_at"`
	DispatchID      foundationshared.UUID `json:"dispatch_id"`
	ThresholdMs     int                   `json:"threshold_ms"`
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

// EventPayload is the payload schema for event/<name>.
type EventPayload struct {
	Name         string         `json:"name"`
	EventPayload map[string]any `json:"event_payload,omitempty"`
}

// MessagePayload is the payload schema for
// message/<kind>/<sender_kind>/<target>.
type MessagePayload struct {
	Kind           string         `json:"kind"`
	SenderKind     string         `json:"sender_kind"`
	Sender         string         `json:"sender"`
	Target         string         `json:"target"`
	MessagePayload map[string]any `json:"message_payload,omitempty"`
}

// PayloadSchemaForType returns the Go reflect.Type of the payload
// struct that matches the given exact TypePath, or (nil, false) when
// the path is a prefix pattern (trailing "*") — prefix subscriptions
// bind payload as CEL `dyn` per the spec.
//
// The mapping is exhaustive over the canonical taxonomy enumerated in
// taxonomy.go; an unrecognized exact path returns (nil, false).
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
	case s == "transient/heartbeat_missed":
		return reflect.TypeOf(TransientHeartbeatMissedPayload{}), true
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
	case strings.HasPrefix(s, "event/"):
		return reflect.TypeOf(EventPayload{}), true
	case strings.HasPrefix(s, "message/"):
		return reflect.TypeOf(MessagePayload{}), true
	}
	return nil, false
}
