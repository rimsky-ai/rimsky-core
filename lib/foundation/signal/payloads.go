// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package signal

import (
	"reflect"
	"strings"
	"time"
)

// @concept: terminal-tag
type TerminalSuccessPayload struct {
	Changed         bool           `json:"changed"`
	AttributesDelta map[string]any `json:"attributes_delta"`
	ChangeSummary   string         `json:"change_summary,omitempty"`
	Tags            []string       `json:"tags,omitempty"`
}

// @concept: terminal-tag
type TerminalErrorPayload struct {
	ErrorClass   string         `json:"error_class"`
	ErrorPayload map[string]any `json:"error_payload,omitempty"`
	Attempt      int            `json:"attempt"`
	RetriesSoFar int            `json:"retries_so_far"`
	Tags         []string       `json:"tags,omitempty"`
}

// @concept: terminal-tag
type TerminalParkSnoozePayload struct {
	ResumeAt          time.Time `json:"resume_at"`
	ParkedReasonLabel string    `json:"parked_reason_label,omitempty"`
	ParkedReasonNote  string    `json:"parked_reason_note,omitempty"`
	Tags              []string  `json:"tags,omitempty"`
}

// @concept: terminal-tag
type TerminalParkAwaitCallbackPayload struct {
	ResumeAt          *time.Time `json:"resume_at,omitempty"`
	ParkedReasonLabel string     `json:"parked_reason_label,omitempty"`
	ParkedReasonNote  string     `json:"parked_reason_note,omitempty"`
	Tags              []string   `json:"tags,omitempty"`
}

type TerminalInfraPayload struct {
	Reason          string         `json:"reason"`
	LastHeartbeatAt *time.Time     `json:"last_heartbeat_at,omitempty"`
	Details         map[string]any `json:"details,omitempty"`
}

type TransientRetryPayload struct {
	Attempt         int            `json:"attempt"`
	Cap             int            `json:"cap"`
	ErrorClass      string         `json:"error_class"`
	DiscardedClaims bool           `json:"discarded_claims"`
	DelayMs         int            `json:"delay_ms"`
	ErrorPayload    map[string]any `json:"error_payload,omitempty"`
}

type TransientAwaitAsyncPayload struct {
	AsyncAckID  string `json:"async_ack_id"`
	CallbackURL string `json:"callback_url"`
}

type AttributeChangedPayload struct {
	Key      string `json:"key"`
	Value    any    `json:"value"`
	OldValue any    `json:"old_value,omitempty"`
}

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
