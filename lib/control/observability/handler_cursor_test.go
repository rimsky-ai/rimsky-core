// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package observability_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/control/observability"
)

func TestHandler_MalformedCursorAnswers400(t *testing.T) {
	d := newSQLiteDriver(t)
	store := d.Tables()
	ctx := context.Background()
	fix := seedInstanceWithNode(t, ctx, store, singleNodeTemplateSpec("fixture-node-type"))
	seedFrame(t, ctx, store, fix.InstanceID, fix.MainRunScopeID, "test/cursor")

	disc := observability.NewDiscovery(&nopProber{})
	r := newRouter(t, observability.Deps{Tables: store, Queue: d.Queue(), Discovery: disc})

	for _, path := range []string{
		"/v1/observability/frames?cursor=not-a-cursor",
		"/v1/observability/claim-handles?cursor=not-a-cursor",
	} {
		req := httptest.NewRequest("GET", path, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("GET %s = %d, want 400: a cursor that fails to decode is a client error, "+
				"the same as an invalid limit; body=%s", path, w.Code, w.Body.String())
			continue
		}
		body := w.Body.String()
		for _, leak := range []string{".list", "frames.", "claimhandles.", "base64", "json:"} {
			if strings.Contains(body, leak) {
				t.Errorf("GET %s: the 400 body %s names an internal operation or decoder", path, body)
			}
		}
	}
}
