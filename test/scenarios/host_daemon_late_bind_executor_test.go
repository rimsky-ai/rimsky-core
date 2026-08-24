// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package scenarios

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
)

func TestHostDaemonLateBindExecutorHappyPath(t *testing.T) {
	fx := newHostDaemonFixture(t, fixtureOpts{withDaemon: true})

	tid := fx.deployLateBindTemplate(t, "late-bind-happy")
	iid := fx.createLateBindInstance(t, tid, "ck-late-bind-happy", fx.stubBinary)

	worker := fx.h.FindNode(iid, "worker")
	require.NotNil(t, worker, "worker node should exist")

	fx.h.WaitForNodeState(worker.ID, cascade.NodeStateFresh)

	fx.waitForNodeEventKind(t, iid, "terminal/success")
}
