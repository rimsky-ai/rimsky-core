// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// In-package coverage of parseAsyncCallback: the async-callback body
// parser the supervisor's /v1/callback/{ack} route feeds. Two contracts
// this pins:
//
//   - The AsyncCallbackBody `events[]` array (the path claude-agent uses
//     for named events — it closes its gRPC stream at dispatch, so events
//     ride the callback body, not the stream) is extracted with the
//     payload base64-decoded per the proto-JSON `bytes` convention. This
//     is the Go consumption side of the named-event-emission feature
//     (spec:2026-05-28-quality-of-life-features Feature 5).
//   - The body MUST be the outcome-oneof shape (success | error | park);
//     the legacy `{type: ...}` discriminator is rejected. Both executor
//     transports (gRPC server.ts + HTTP bridge) emit the oneof shape, so
//     this guards against a regression back to the legacy form.

package runtime

import (
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseAsyncCallback_ExtractsEventsWithBase64Payload(t *testing.T) {
	p1 := base64.StdEncoding.EncodeToString([]byte(`{"pct":50}`))
	p2 := base64.StdEncoding.EncodeToString([]byte(`null`))
	raw := []byte(`{
		"events": [
			{"name": "progress", "payload": "` + p1 + `"},
			{"name": "ping", "payload": "` + p2 + `"}
		],
		"success": {"changed": true, "change_summary": "did", "attributes_delta": {"k": "v"}}
	}`)

	term, events, err := parseAsyncCallback(raw)
	require.NoError(t, err)
	require.Equal(t, terminalKindComplete, term.Kind)
	require.True(t, term.Changed)

	require.Len(t, events, 2)
	require.Equal(t, "progress", events[0].Name)
	require.JSONEq(t, `{"pct":50}`, string(events[0].PayloadInline))
	require.Equal(t, "ping", events[1].Name)
	require.Equal(t, `null`, string(events[1].PayloadInline))
}

func TestParseAsyncCallback_NoEventsKeyYieldsEmpty(t *testing.T) {
	raw := []byte(`{"success": {"changed": false}}`)
	term, events, err := parseAsyncCallback(raw)
	require.NoError(t, err)
	require.Equal(t, terminalKindComplete, term.Kind)
	require.Empty(t, events)
}

func TestParseAsyncCallback_EventsRideErrorAndParkVerdicts(t *testing.T) {
	pl := base64.StdEncoding.EncodeToString([]byte(`{"detail":"x"}`))

	errRaw := []byte(`{"events":[{"name":"diag","payload":"` + pl + `"}],` +
		`"error":{"error_class":"agent/blocked","payload":{"reason":"stuck"}}}`)
	term, events, err := parseAsyncCallback(errRaw)
	require.NoError(t, err)
	require.Equal(t, terminalKindErrored, term.Kind)
	require.Len(t, events, 1)
	require.Equal(t, "diag", events[0].Name)

	parkRaw := []byte(`{"events":[{"name":"diag","payload":"` + pl + `"}],` +
		`"park":{"reason":"await_callback","session_token":"s"}}`)
	term, events, err = parseAsyncCallback(parkRaw)
	require.NoError(t, err)
	require.Equal(t, terminalKindPark, term.Kind)
	require.Len(t, events, 1)
}

func TestParseAsyncCallback_RejectsLegacyTypeDiscriminator(t *testing.T) {
	// The legacy `{type: ...}` shape sets no outcome-oneof variant, so the
	// parser sees zero outcomes and rejects the body.
	raw := []byte(`{"type":"complete","attributes_delta":{"k":"v"},"changed":true}`)
	_, _, err := parseAsyncCallback(raw)
	require.Error(t, err)
}

func TestParseAsyncCallback_RejectsMultipleOutcomes(t *testing.T) {
	raw := []byte(`{"success":{"changed":true},"error":{"error_class":"x"}}`)
	_, _, err := parseAsyncCallback(raw)
	require.Error(t, err)
}
