// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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

func TestParseAsyncCallback_Park_AttributesDeltaAndTags(t *testing.T) {
	raw := []byte(`{"park":{"reason":"await_callback","attributes_delta":{"session_token":"s"},"tags":["await"]}}`)
	term, err := parseAsyncCallback(raw)
	require.NoError(t, err)
	require.Equal(t, terminalKindPark, term.Kind)
	require.Equal(t, []string{"await"}, term.Tags)
	require.Equal(t, "s", term.AttributesDel["session_token"])
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
