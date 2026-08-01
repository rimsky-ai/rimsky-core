// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package runtime

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseAsyncCallback_Success_TagsOnVerdict(t *testing.T) {
	raw := []byte(`{"success":{"changed":true,"change_summary":"did","attributes_delta":{"k":"v"},"tags":["a","b","a"]}}`)
	term, err := parseAsyncCallback(raw)
	require.NoError(t, err)
	require.Equal(t, terminalKindComplete, term.Kind)
	require.True(t, term.Changed)
	require.Equal(t, []string{"a", "b"}, term.Tags)
}

func TestParseAsyncCallback_Error_AttributesDeltaAndTags(t *testing.T) {
	raw := []byte(`{"error":{"error_class":"agent/blocked","payload":{"reason":"stuck"},"attributes_delta":{"session_token":"s"},"tags":["diag"]}}`)
	term, err := parseAsyncCallback(raw)
	require.NoError(t, err)
	require.Equal(t, terminalKindErrored, term.Kind)
	require.Equal(t, "agent/blocked", term.ErrorClass)
	require.Equal(t, []string{"diag"}, term.Tags)
	require.Equal(t, "s", term.AttributesDel["session_token"])
}

func TestParseAsyncCallback_Park_ResumeAtAndTags(t *testing.T) {
	raw := []byte(`{"park":{"resume_at":"2026-07-19T10:00:00Z","tags":["await"]}}`)
	term, err := parseAsyncCallback(raw)
	require.NoError(t, err)
	require.Equal(t, terminalKindPark, term.Kind)
	require.Equal(t, []string{"await"}, term.Tags)
	require.False(t, term.ParkResumeAt.IsZero())
}

func TestParseAsyncCallback_Park_RejectsMissingResumeAt(t *testing.T) {
	raw := []byte(`{"park":{"tags":["await"]}}`)
	_, err := parseAsyncCallback(raw)
	require.Error(t, err)
	require.Contains(t, err.Error(), "resume_at")
}

func TestParseAsyncCallback_Park_RejectsMalformedResumeAt(t *testing.T) {
	raw := []byte(`{"park":{"resume_at":"tomorrow-ish"}}`)
	_, err := parseAsyncCallback(raw)
	require.Error(t, err)
	require.Contains(t, err.Error(), "resume_at")
}

func TestParseAsyncCallback_Park_IgnoresAttributesDelta(t *testing.T) {
	raw := []byte(`{"park":{"resume_at":"2026-07-19T10:00:00Z","attributes_delta":{"session_token":"s"},"tags":["await"]}}`)
	term, err := parseAsyncCallback(raw)
	require.NoError(t, err)
	require.Equal(t, terminalKindPark, term.Kind)
	require.Equal(t, []string{"await"}, term.Tags)
	require.Nil(t, term.AttributesDel, "Park outcomes do not carry attributes_delta; the field is parsed-but-discarded on the JSON wire (proto reserved on the gRPC wire)")
}

func TestParseAsyncCallback_RejectsLegacyTypeDiscriminator(t *testing.T) {
	raw := []byte(`{"type":"complete","attributes_delta":{"k":"v"},"changed":true}`)
	_, err := parseAsyncCallback(raw)
	require.Error(t, err)
}

func TestParseAsyncCallback_RejectsMultipleOutcomes(t *testing.T) {
	raw := []byte(`{"success":{"changed":true},"error":{"error_class":"x"}}`)
	_, err := parseAsyncCallback(raw)
	require.Error(t, err)
}
