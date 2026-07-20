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
	ErrorClass      string         `json:"error_class"`
	ErrorPayload    map[string]any `json:"error_payload,omitempty"`
	Attempt         int            `json:"attempt"`
	RetriesSoFar    int            `json:"retries_so_far"`
	AttributesDelta map[string]any `json:"attributes_delta,omitempty"`
	Tags            []string       `json:"tags,omitempty"`
}

type TransientParkPayload struct {
	ResumeAt time.Time `json:"resume_at"`
	Tags     []string  `json:"tags,omitempty"`
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

func BuildTerminalErrorSignal(errorClass string, errorPayload map[string]any, attempt int, retriesSoFar int, attributesDelta map[string]any, tags []string) Signal {
	p := TerminalErrorPayload{
		ErrorClass:      errorClass,
		ErrorPayload:    errorPayload,
		Attempt:         attempt,
		RetriesSoFar:    retriesSoFar,
		AttributesDelta: orEmptyMap(attributesDelta),
		Tags:            tags,
	}
	return Signal{
		Type:    TypePath("terminal/error/" + errorClass),
		Payload: terminalErrorPayloadToMap(p),
	}
}

func terminalErrorPayloadToMap(p TerminalErrorPayload) map[string]any {
	return map[string]any{
		"error_class":      p.ErrorClass,
		"error_payload":    p.ErrorPayload,
		"attempt":          p.Attempt,
		"retries_so_far":   p.RetriesSoFar,
		"attributes_delta": p.AttributesDelta,
		"tags":             p.Tags,
	}
}

func BuildTerminalSuccessSignal(changed bool, attributesDelta map[string]any, changeSummary string, tags []string) Signal {
	p := TerminalSuccessPayload{
		Changed:         changed,
		AttributesDelta: orEmptyMap(attributesDelta),
		ChangeSummary:   changeSummary,
		Tags:            tags,
	}
	return Signal{
		Type:    TypePath("terminal/success"),
		Payload: terminalSuccessPayloadToMap(p),
	}
}

func terminalSuccessPayloadToMap(p TerminalSuccessPayload) map[string]any {
	return map[string]any{
		"changed":          p.Changed,
		"attributes_delta": p.AttributesDelta,
		"change_summary":   p.ChangeSummary,
		"tags":             p.Tags,
	}
}

func orEmptyMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

func PayloadSchemaForType(t TypePath) (reflect.Type, bool) {
	s := string(t)
	if s == "" {
		return nil, false
	}
	trimmed := strings.TrimSuffix(s, "*")
	switch {
	case s == "terminal/success":
		return reflect.TypeOf(TerminalSuccessPayload{}), true
	case s == "transient/park":
		return reflect.TypeOf(TransientParkPayload{}), true
	case s == "transient/await_async":
		return reflect.TypeOf(TransientAwaitAsyncPayload{}), true
	case strings.HasPrefix(trimmed, "terminal/error/"):
		return reflect.TypeOf(TerminalErrorPayload{}), true
	case strings.HasPrefix(trimmed, "transient/retry/"):
		return reflect.TypeOf(TransientRetryPayload{}), true
	case strings.HasPrefix(trimmed, "attribute/") && strings.HasSuffix(trimmed, "/changed"):
		return reflect.TypeOf(AttributeChangedPayload{}), true
	}
	return nil, false
}
