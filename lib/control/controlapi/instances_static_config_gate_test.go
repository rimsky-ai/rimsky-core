// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package controlapi

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLookupExecutorSchema_MalformedSchemaLogsWarn(t *testing.T) {
	prev := slog.Default()
	var buf bytes.Buffer
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	execCaps := func(string) ([]string, []string, []byte, bool) {
		return nil, nil, []byte(`{not valid json`), true
	}

	schema, ok := lookupExecutorSchema("broken-executor", execCaps)
	require.False(t, ok)
	require.Nil(t, schema)
	require.Contains(t, buf.String(), "malformed_executor_schema")
	require.Contains(t, buf.String(), "broken-executor")
}

func TestLookupExecutorSchema_NoSchemaDeclaredDoesNotLog(t *testing.T) {
	prev := slog.Default()
	var buf bytes.Buffer
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	execCaps := func(string) ([]string, []string, []byte, bool) {
		return nil, nil, nil, true
	}

	schema, ok := lookupExecutorSchema("no-schema-executor", execCaps)
	require.False(t, ok)
	require.Nil(t, schema)
	require.Empty(t, buf.String(),
		"the legitimate no-schema-declared case must be silent, distinct from a malformed-schema warning")
}

func TestLookupExecutorSchema_ValidSchemaReturnsIt(t *testing.T) {
	execCaps := func(string) ([]string, []string, []byte, bool) {
		return nil, nil, []byte(`{"type":"object"}`), true
	}

	schema, ok := lookupExecutorSchema("good-executor", execCaps)
	require.True(t, ok)
	require.Equal(t, "object", schema["type"])
}
