// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package listarray_test

import (
	"bytes"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/services/claim_producers/shared/listarray"
)

func TestUnmarshal_ValidShape(t *testing.T) {
	t.Parallel()
	in := []byte(`{"list":[{"key":"a","payload":{"v":1}},{"key":"b","payload":{"v":2}}]}`)
	req, err := listarray.Unmarshal(in)
	if err != nil {
		t.Fatalf("Unmarshal: unexpected error: %v", err)
	}
	if got, want := len(req.List), 2; got != want {
		t.Fatalf("Unmarshal: len(list) = %d, want %d", got, want)
	}
	if got, want := req.List[0].Key, "a"; got != want {
		t.Fatalf("Unmarshal: list[0].Key = %q, want %q", got, want)
	}
	if got, want := req.List[1].Key, "b"; got != want {
		t.Fatalf("Unmarshal: list[1].Key = %q, want %q", got, want)
	}
	if !bytes.Equal(req.List[0].Payload, []byte(`{"v":1}`)) {
		t.Fatalf("Unmarshal: list[0].Payload = %s, want %s", string(req.List[0].Payload), `{"v":1}`)
	}
}

func TestUnmarshal_RejectsMissingKey(t *testing.T) {
	t.Parallel()
	in := []byte(`{"list":[{"key":"a","payload":{}},{"key":"","payload":{}}]}`)
	_, err := listarray.Unmarshal(in)
	if err == nil {
		t.Fatalf("Unmarshal: expected error for empty key, got nil")
	}
}

func TestUnmarshal_RejectsDuplicateKeys(t *testing.T) {
	t.Parallel()
	in := []byte(`{"list":[{"key":"a","payload":{"v":1}},{"key":"a","payload":{"v":2}}]}`)
	_, err := listarray.Unmarshal(in)
	if err == nil {
		t.Fatalf("Unmarshal: expected error for duplicate key, got nil")
	}
}

func TestUnmarshal_RejectsNotAnArray(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   []byte
	}{
		{name: "top-level array", in: []byte(`[{"key":"a"}]`)},
		{name: "top-level string", in: []byte(`"a"`)},
		{name: "missing list key", in: []byte(`{"items":[{"key":"a"}]}`)},
		{name: "empty bytes", in: []byte(``)},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := listarray.Unmarshal(tc.in)
			if err == nil {
				t.Fatalf("Unmarshal: expected error for %q, got nil", string(tc.in))
			}
		})
	}
}

func TestUnmarshal_RejectsEmptyList(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   []byte
	}{
		{name: "empty array", in: []byte(`{"list":[]}`)},
		{name: "null list", in: []byte(`{"list":null}`)},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := listarray.Unmarshal(tc.in)
			if err == nil {
				t.Fatalf("Unmarshal: expected error for %q, got nil", string(tc.in))
			}
		})
	}
}

func TestUnmarshal_RejectsUnknownTopLevelKeys(t *testing.T) {
	t.Parallel()
	in := []byte(`{"list":[{"key":"a"}],"list_too":[]}`)
	_, err := listarray.Unmarshal(in)
	if err == nil {
		t.Fatalf("Unmarshal: expected error for extra top-level key, got nil")
	}
}

func TestUnmarshal_RejectsUnknownElementKeys(t *testing.T) {
	t.Parallel()
	in := []byte(`{"list":[{"key":"a","garbage":"x"}]}`)
	_, err := listarray.Unmarshal(in)
	if err == nil {
		t.Fatalf("Unmarshal: expected error for unknown element field, got nil")
	}
}

func TestUnmarshal_RejectsTrailingDataAfterObject(t *testing.T) {
	t.Parallel()
	in := []byte(`{"list":[{"key":"a"}]}{"list":[{"key":"b"}]}`)
	_, err := listarray.Unmarshal(in)
	if err == nil {
		t.Fatalf("Unmarshal: expected error for trailing data after the JSON object, got nil")
	}
}

func TestToSubScopes_PassesThroughPayload(t *testing.T) {
	t.Parallel()
	in := []byte(`{"list":[{"key":"a","payload":{"v":1}},{"key":"b","payload":{"v":2}}]}`)
	req, err := listarray.Unmarshal(in)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	subs, err := listarray.ToSubScopes(req, func(key string) ([]byte, error) {
		return []byte("scope:" + key), nil
	})
	if err != nil {
		t.Fatalf("ToSubScopes: %v", err)
	}
	if got, want := len(subs), 2; got != want {
		t.Fatalf("ToSubScopes: len = %d, want %d", got, want)
	}
	if got, want := subs[0].PartitionKey, "a"; got != want {
		t.Fatalf("ToSubScopes: subs[0].PartitionKey = %q, want %q", got, want)
	}
	if !bytes.Equal(subs[0].Payload, []byte(`{"v":1}`)) {
		t.Fatalf("ToSubScopes: subs[0].Payload = %s, want %s", string(subs[0].Payload), `{"v":1}`)
	}
	if !bytes.Equal(subs[1].Payload, []byte(`{"v":2}`)) {
		t.Fatalf("ToSubScopes: subs[1].Payload = %s, want %s", string(subs[1].Payload), `{"v":2}`)
	}
	if !bytes.Equal(subs[0].ClaimScopeData, []byte("scope:a")) {
		t.Fatalf("ToSubScopes: subs[0].ClaimScopeData = %s, want %s", string(subs[0].ClaimScopeData), "scope:a")
	}
	if len(subs[0].Address) != 0 {
		t.Fatalf("ToSubScopes: subs[0].Address = %s, want empty (list shape carries data in payload)", string(subs[0].Address))
	}
}
