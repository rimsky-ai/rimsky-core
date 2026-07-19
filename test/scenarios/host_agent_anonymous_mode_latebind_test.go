// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
)

func TestHostAgentAnonymousModeLateBind(t *testing.T) {
	fx := newHostAgentFixture(t, fixtureOpts{withAgent: true, anonymous: true})

	tid := fx.deployLateBindTemplate(t, "anon-late-bind")
	iid := fx.createLateBindInstance(t, tid, "ck-anon-late-bind", fx.stubBinary)

	worker := fx.h.FindNode(iid, "worker")
	require.NotNil(t, worker, "worker node should exist")

	fx.h.WaitForNodeState(worker.ID, cascade.NodeStateFresh)

	fx.waitForNodeEventKind(t, iid, "terminal/success")
}
