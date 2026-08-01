// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package controlapi

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInstanceCreate_TagSubstitution(t *testing.T) {
	t.Parallel()

	t.Run("static tag passes through", func(t *testing.T) {
		t.Parallel()
		got, err := resolveNodeTags([]string{"setup", "recurring"}, nil)
		require.NoError(t, err)
		require.Equal(t, []string{"setup", "recurring"}, got)
	})

	t.Run("embedded mode with string param", func(t *testing.T) {
		t.Parallel()
		params := json.RawMessage(`{"domain": "alpha.example.com"}`)
		got, err := resolveNodeTags([]string{"domain:{{params.domain}}"}, params)
		require.NoError(t, err)
		require.Equal(t, "domain:alpha.example.com", got[0])
	})

	t.Run("whole-directive lift with string param", func(t *testing.T) {
		t.Parallel()
		params := json.RawMessage(`{"region": "us-west"}`)
		got, err := resolveNodeTags([]string{"{{params.region}}"}, params)
		require.NoError(t, err)
		require.Equal(t, "us-west", got[0])
	})

	t.Run("whole-directive lift with non-string param fails", func(t *testing.T) {
		t.Parallel()
		params := json.RawMessage(`{"config": {"a": 1}}`)
		_, err := resolveNodeTags([]string{"{{params.config}}"}, params)
		require.Error(t, err, "expected error for non-string lifted tag value")
		require.Contains(t, err.Error(), "non-string")
	})

	t.Run("missing param fails", func(t *testing.T) {
		t.Parallel()
		got, err := resolveNodeTags([]string{"{{params.missing}}"}, json.RawMessage(`{}`))
		require.Error(t, err, "expected error for missing param, got %v", got)
		require.True(t, strings.Contains(err.Error(), "missing") || strings.Contains(err.Error(), "param"),
			"error should reference the missing source: %v", err)
	})

	t.Run("embedded mode with numeric param stringifies", func(t *testing.T) {
		t.Parallel()
		params := json.RawMessage(`{"version": 7}`)
		got, err := resolveNodeTags([]string{"v{{params.version}}"}, params)
		require.NoError(t, err)
		require.Equal(t, "v7", got[0])
	})
}
