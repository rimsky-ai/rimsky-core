// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
)

func TestHostAgentLateBindExecutorHappyPath(t *testing.T) {
	fx := newHostAgentFixture(t, fixtureOpts{withAgent: true})

	tid := fx.deployLateBindTemplate(t, "late-bind-happy")
	iid := fx.createLateBindInstance(t, tid, "ck-late-bind-happy", fx.stubBinary)

	worker := fx.h.FindNode(iid, "worker")
	require.NotNil(t, worker, "worker node should exist")

	fx.h.WaitForNodeState(worker.ID, cascade.NodeStateFresh)

	fx.waitForNodeEventKind(t, iid, "terminal/success")
}
