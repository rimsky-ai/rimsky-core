// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package controlapi

import (
	"net/http"
	"strings"
	"testing"
)

func TestMalformedPaginationCursorAnswers400(t *testing.T) {
	h, done := newHarness(t)
	defer done()

	for _, path := range []string{
		"/v1/instances?cursor=not-a-cursor",
		"/v1/templates?cursor=not-a-cursor",
	} {
		status, body := h.httpJSON(t, http.MethodGet, path, nil)
		if status != http.StatusBadRequest {
			t.Errorf("GET %s = %d, want 400: a cursor that fails to decode is a client error, "+
				"the same as an invalid limit; body=%v", path, status, body)
			continue
		}
		msg, _ := body["error"].(string)
		if msg == "" {
			t.Errorf("GET %s: 400 carried no error message", path)
			continue
		}
		for _, leak := range []string{".list", "instances.", "templates.", "frames.", "base64", "json:"} {
			if strings.Contains(msg, leak) {
				t.Errorf("GET %s: the 400 message %q names an internal operation or decoder; "+
					"the caller-safe message discloses none", path, msg)
			}
		}
	}
}
