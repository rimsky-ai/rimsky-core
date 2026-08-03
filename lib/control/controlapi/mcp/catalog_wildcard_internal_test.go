// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package mcp

import (
	"encoding/json"
	"testing"
)

// @decision: mcp-http-parity
func TestSubstitutePathParamsFillsAWildcardFromPathSuffix(t *testing.T) {
	for _, tc := range []struct {
		name    string
		suffix  string
		want    string
		wantErr bool
	}{
		{name: "single segment", suffix: "executors", want: "/v1/observability/executors"},
		{name: "nested segments", suffix: "executors/claude-agent/trace/d-1", want: "/v1/observability/executors/claude-agent/trace/d-1"},
		{name: "leading and trailing slashes trimmed", suffix: "/executors/", want: "/v1/observability/executors"},
		{name: "segment needing escaping", suffix: "executors/a b", want: "/v1/observability/executors/a%20b"},
		{name: "empty suffix rejected", suffix: "", wantErr: true},
		{name: "parent traversal rejected", suffix: "../auth/keys", wantErr: true},
		{name: "empty interior segment rejected", suffix: "executors//trace", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := map[string]json.RawMessage{pathSuffixArgName: json.RawMessage(`"` + tc.suffix + `"`)}
			got, remaining, err := substitutePathParams("/v1/observability/*", args)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("suffix %q should have been rejected, got path %q", tc.suffix, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("suffix %q: %v", tc.suffix, err)
			}
			if got != tc.want {
				t.Errorf("path = %q, want %q", got, tc.want)
			}
			if _, still := remaining[pathSuffixArgName]; still {
				t.Error("path_suffix must be consumed by the path, not re-sent as a query param")
			}
		})
	}
}

// @decision: mcp-http-parity
func TestSubstitutePathParamsRequiresPathSuffixForAWildcardRoute(t *testing.T) {
	if _, _, err := substitutePathParams("/v1/observability/*", map[string]json.RawMessage{}); err == nil {
		t.Fatal("a wildcard route invoked without path_suffix must fail rather than dispatch to the bare prefix")
	}
}
