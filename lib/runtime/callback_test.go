// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package runtime_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
)

func TestCallbackRegistry_RegisterPopRoundTrip(t *testing.T) {
	t.Parallel()
	reg := runtime.NewCallbackRegistry()

	ackID := "ack-" + uuid.NewString()
	want := runtime.AsyncContext{
		NodeID:       shared.UUID(uuid.New()),
		InstanceID:   shared.UUID(uuid.New()),
		NodeRunID:    shared.UUID(uuid.New()),
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

// @decision: async-callback-persistent-registry
func TestCallbackRegistry_RegisterRejectsCollisionInsteadOfOverwriting(t *testing.T) {
	t.Parallel()
	reg := runtime.NewCallbackRegistry()

	ackID := "ack-" + uuid.NewString()
	first := runtime.AsyncContext{
		NodeID:       shared.UUID(uuid.New()),
		SupervisorID: "sup-first",
	}
	second := runtime.AsyncContext{
		NodeID:       shared.UUID(uuid.New()),
		SupervisorID: "sup-second",
	}

	require.True(t, reg.Register(ackID, first), "first registration for a fresh ack id must succeed")
	require.False(t, reg.Register(ackID, second),
		"a colliding ack id must be rejected, not silently overwrite the pending entry from the first registration")

	got, ok := reg.Pop(ackID)
	require.True(t, ok)
	require.Equal(t, first.SupervisorID, got.SupervisorID,
		"the original entry must survive a rejected collision, not be clobbered by the second caller")
}

func TestCallbackRegistry_PopUnknown(t *testing.T) {
	t.Parallel()
	reg := runtime.NewCallbackRegistry()

	_, ok := reg.Pop("does-not-exist")
	require.False(t, ok)
}
