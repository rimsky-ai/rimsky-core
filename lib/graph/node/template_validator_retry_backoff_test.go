// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package node

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	foundationspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

// @concept: error-policy
func TestValidateRetryBackoff_RejectsUnknownKind(t *testing.T) {
	for _, kind := range []string{"quadratic", "flat", "expo", "Linear"} {
		t.Run(kind, func(t *testing.T) {
			spec := &TemplateSpec{
				Name: "demo", Version: "1",
				Nodes: []TemplateNodeDef{
					{Type: "a", Executor: "h"},
					{Type: "b", Executor: "h", RetryBackoff: &foundationspec.RetryBackoffConfig{
						Kind: foundationspec.BackoffKind(kind),
					}},
				},
			}
			res := ValidateTemplate(spec, RegistryHooks{})
			require.False(t, res.Ok())
			hasErrorAt(t, res, "nodes[1].retry_backoff.kind")
		})
	}
}

func TestValidateRetryBackoff_RejectsUnknownJitter(t *testing.T) {
	for _, jitter := range []string{"full", "plusminus", "None"} {
		t.Run(jitter, func(t *testing.T) {
			spec := &TemplateSpec{
				Name: "demo", Version: "1",
				Nodes: []TemplateNodeDef{
					{Type: "a", Executor: "h"},
					{Type: "b", Executor: "h", RetryBackoff: &foundationspec.RetryBackoffConfig{
						Kind:   foundationspec.BackoffLinear,
						Jitter: foundationspec.JitterKind(jitter),
					}},
				},
			}
			res := ValidateTemplate(spec, RegistryHooks{})
			require.False(t, res.Ok())
			hasErrorAt(t, res, "nodes[1].retry_backoff.jitter")
		})
	}
}

func TestValidateRetryBackoff_AcceptsCanonicalAndUnset(t *testing.T) {
	cases := []struct {
		name    string
		backoff *foundationspec.RetryBackoffConfig
	}{
		{"absent", nil},
		{"empty_kind_and_jitter", &foundationspec.RetryBackoffConfig{BaseDelayMs: 100}},
		{"linear", &foundationspec.RetryBackoffConfig{Kind: foundationspec.BackoffLinear}},
		{"exponential", &foundationspec.RetryBackoffConfig{Kind: foundationspec.BackoffExponential}},
		{"jitter_none", &foundationspec.RetryBackoffConfig{Jitter: foundationspec.JitterNone}},
		{"jitter_plus_minus", &foundationspec.RetryBackoffConfig{Kind: foundationspec.BackoffExponential, Jitter: foundationspec.JitterPlusMinus}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := &TemplateSpec{
				Name: "demo", Version: "1",
				Nodes: []TemplateNodeDef{
					{Type: "a", Executor: "h"},
					{Type: "b", Executor: "h", RetryBackoff: tc.backoff},
				},
			}
			res := ValidateTemplate(spec, RegistryHooks{})
			for _, e := range res.Errors {
				if e.Path == "nodes[1].retry_backoff.kind" || e.Path == "nodes[1].retry_backoff.jitter" {
					t.Fatalf("unexpected retry-backoff vocabulary error: %+v", e)
				}
			}
		})
	}
}

// @concept: error-policy
func TestValidateRetryBackoff_RejectsBadNumerics(t *testing.T) {
	cases := []struct {
		name    string
		backoff *foundationspec.RetryBackoffConfig
		wantAt  string
	}{
		{"negative_base", &foundationspec.RetryBackoffConfig{BaseDelayMs: -1}, "nodes[1].retry_backoff.base_delay_ms"},
		{"zero_base_with_kind", &foundationspec.RetryBackoffConfig{Kind: foundationspec.BackoffExponential}, "nodes[1].retry_backoff.base_delay_ms"},
		{"zero_base_with_ceiling", &foundationspec.RetryBackoffConfig{MaxDelayMs: 5000}, "nodes[1].retry_backoff.base_delay_ms"},
		{"negative_ceiling", &foundationspec.RetryBackoffConfig{BaseDelayMs: 100, MaxDelayMs: -1}, "nodes[1].retry_backoff.max_delay_ms"},
		{"ceiling_below_base", &foundationspec.RetryBackoffConfig{BaseDelayMs: 5000, MaxDelayMs: 100}, "nodes[1].retry_backoff.max_delay_ms"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := &TemplateSpec{
				Name: "demo", Version: "1",
				Nodes: []TemplateNodeDef{
					{Type: "a", Executor: "h"},
					{Type: "b", Executor: "h", RetryBackoff: tc.backoff},
				},
			}
			res := ValidateTemplate(spec, RegistryHooks{})
			require.False(t, res.Ok())
			hasErrorAt(t, res, tc.wantAt)
		})
	}
}

// @concept: error-policy
func TestValidateRetryBackoff_AcceptsSaneNumerics(t *testing.T) {
	cases := []*foundationspec.RetryBackoffConfig{
		{BaseDelayMs: 100},
		{Kind: foundationspec.BackoffExponential, BaseDelayMs: 100, MaxDelayMs: 30000},
		{Kind: foundationspec.BackoffLinear, BaseDelayMs: 100, MaxDelayMs: 100},
	}
	for i, backoff := range cases {
		t.Run(fmt.Sprintf("case_%d", i), func(t *testing.T) {
			spec := &TemplateSpec{
				Name: "demo", Version: "1",
				Nodes: []TemplateNodeDef{
					{Type: "a", Executor: "h"},
					{Type: "b", Executor: "h", RetryBackoff: backoff},
				},
			}
			res := ValidateTemplate(spec, RegistryHooks{})
			require.True(t, res.Ok(), "sane backoff numerics must register cleanly; got: %+v", res.Errors)
		})
	}
}
