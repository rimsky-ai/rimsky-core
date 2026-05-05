// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Minimal coverage of the supervisor's CallbackRegistry under the
// stores redesign.
//
// The pre-redesign callback_test.go drove the full callback HTTP
// server against the old AcquireLock/OpenHandle/ReleaseLock surface.
// The new
// release flow runs through Open/Commit/Abandon and the auto-terminal
// algorithm; coverage of the full callback pipeline belongs in the
// scenario suite, not here. This file pins the in-memory registry's
// register/pop semantics.

package integration_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/foundation/integration"
	"github.com/fallguy/rimsky/modeling/shared"
)

func TestCallbackRegistry_RegisterPopRoundTrip(t *testing.T) {
	t.Parallel()
	reg := integration.NewCallbackRegistry()

	ackID := "ack-" + uuid.NewString()
	want := integration.AsyncContext{
		NodeID:       shared.UUID(uuid.New()),
		InstanceID:   shared.UUID(uuid.New()),
		DispatchID:   shared.UUID(uuid.New()),
		SupervisorID: "sup-1",
		NodeType:     "worker",
		Executor:     "stub",
	}
	reg.Register(ackID, want)

	got, ok := reg.Pop(ackID)
	require.True(t, ok, "Pop should find the registered ackID")
	require.Equal(t, want.NodeID, got.NodeID)
	require.Equal(t, want.SupervisorID, got.SupervisorID)
	require.Equal(t, want.NodeType, got.NodeType)

	_, ok = reg.Pop(ackID)
	require.False(t, ok, "Pop is consume-once")
}

func TestCallbackRegistry_PopUnknown(t *testing.T) {
	t.Parallel()
	reg := integration.NewCallbackRegistry()

	_, ok := reg.Pop("does-not-exist")
	require.False(t, ok)
}
