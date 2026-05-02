// Verifies blessed invariant 15 (spec §18): "Frame-resolution mode is
// mandatory and per-template. Control-api rejects template uploads
// missing frame_resolution; the field is one of coalesce | serial_queue."
//
// Mechanism: POST raw JSON to /v1/templates that omits the field, that
// uses an invalid value, and that uses an out-of-floor frame_timeout_ms.
// Each must return HTTP 400 with the field name in the response. A
// well-formed POST with serial_queue must succeed.
package frame_resolution

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/scenario"
)

func TestTemplateMissingFrameResolutionRejected(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	post := func(spec map[string]any) (int, string) {
		raw, err := json.Marshal(map[string]any{"spec": spec})
		require.NoError(t, err)
		resp, err := http.Post(h.ControlBase+"/templates", "application/json", bytes.NewReader(raw))
		require.NoError(t, err)
		defer resp.Body.Close()
		buf, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(buf)
	}

	baseNodes := []map[string]any{
		{
			"type":     "worker",
			"executor": "stub",
		},
	}

	// Missing frame_resolution.
	status, body := post(map[string]any{
		"name":    "missing-frame-res",
		"version": "1",
		"nodes":   baseNodes,
	})
	require.Equal(t, http.StatusBadRequest, status, "missing frame_resolution should be 400; body=%s", body)
	require.Contains(t, strings.ToLower(body), "frame_resolution",
		"error body should mention frame_resolution; got %s", body)

	// Invalid frame_resolution value.
	status, body = post(map[string]any{
		"name":             "invalid-frame-res",
		"version":          "1",
		"frame_resolution": "abort",
		"nodes":            baseNodes,
	})
	require.Equal(t, http.StatusBadRequest, status, "invalid frame_resolution should be 400; body=%s", body)
	require.Contains(t, strings.ToLower(body), "frame_resolution",
		"error body should mention frame_resolution; got %s", body)

	// frame_timeout_ms below the 60000 hard floor.
	status, body = post(map[string]any{
		"name":             "below-floor",
		"version":          "1",
		"frame_resolution": "serial_queue",
		"frame_timeout_ms": 30000,
		"nodes":            baseNodes,
	})
	require.Equal(t, http.StatusBadRequest, status, "frame_timeout_ms < 60000 should be 400; body=%s", body)
	require.Contains(t, strings.ToLower(body), "frame_timeout",
		"error body should mention frame_timeout; got %s", body)

	// Valid: serial_queue, no frame_timeout_ms (should default).
	status, body = post(map[string]any{
		"name":             "valid-serial",
		"version":          "1",
		"frame_resolution": "serial_queue",
		"nodes":            baseNodes,
	})
	require.Equal(t, http.StatusCreated, status, "valid serial_queue template should be 201; body=%s", body)
}
