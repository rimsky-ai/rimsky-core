// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"bytes"
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func TestCallbackServer_MidDispatchScratchRouteIsGone(t *testing.T) {
	t.Parallel()
	c := &CallbackServer{
		Logger:       shared.SilentLogger{},
		SupervisorID: "sup-1",
		Persist:      keepaliveStubTables{},
		Queue:        &keepaliveStubQueue{found: true},
	}
	addr, err := c.Start("127.0.0.1", 0)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = c.Close(context.Background()) })

	resp, err := http.Post(
		"http://"+addr+"/v1/runs/"+uuid.NewString()+"/scratch",
		"application/octet-stream", bytes.NewReader([]byte("bytes")))
	if err != nil {
		t.Fatalf("POST scratch: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("POST /v1/runs/{id}/scratch = %d, want 404 (the mid-dispatch scratch route is retired; scratch rides Park outcomes only)", resp.StatusCode)
	}
}
