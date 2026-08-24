// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package hostdaemon

import (
	"net/http"
	"reflect"
	"testing"
)

func TestJoinAndApplyHeaders_RoundTripsMultipleValues(t *testing.T) {
	src := http.Header{}
	src.Add("Cookie", "a=1; b=2")
	src.Add("X-Custom", "one")
	src.Add("X-Custom", "two")
	src.Add("X-Custom", "three")

	joined := JoinHeaderValues(src)

	dst := http.Header{}
	ApplyJoinedHeaders(dst, joined)

	if !reflect.DeepEqual(dst.Values("Cookie"), src.Values("Cookie")) {
		t.Errorf("Cookie: got %v, want %v", dst.Values("Cookie"), src.Values("Cookie"))
	}
	if !reflect.DeepEqual(dst.Values("X-Custom"), src.Values("X-Custom")) {
		t.Errorf("X-Custom: got %v, want %v", dst.Values("X-Custom"), src.Values("X-Custom"))
	}
}

func TestJoinAndApplyHeaders_SingleValuedHeaderUnaffected(t *testing.T) {
	src := http.Header{}
	src.Set("Content-Type", "application/json")

	joined := JoinHeaderValues(src)
	if joined["Content-Type"] != "application/json" {
		t.Errorf("Content-Type: got %q, want %q", joined["Content-Type"], "application/json")
	}

	dst := http.Header{}
	ApplyJoinedHeaders(dst, joined)
	if dst.Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type after ApplyJoinedHeaders: got %q, want %q", dst.Get("Content-Type"), "application/json")
	}
}
