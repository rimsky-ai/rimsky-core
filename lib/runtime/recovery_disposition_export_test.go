// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	shared "github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

type ErrorPolicyTestInput struct {
	NodeRunID  shared.UUID
	NodeID     shared.UUID
	InstanceID shared.UUID
	FrameID    shared.UUID
	RunScopeID shared.UUID
	NodeType   string
	Executor   string
	ErrorClass string
	NodeDef    *node.TemplateNodeDef
	Scratch    []byte
}

func ApplyErrorPolicyForTest(
	ctx context.Context, args RunArgs, tx persistence.Tx, in ErrorPolicyTestInput,
) (priorDispatchID *shared.UUID, priorDisposition string, err error) {
	acq := &acquisition{
		NodeRunID:  in.NodeRunID,
		NodeID:     in.NodeID,
		InstanceID: in.InstanceID,
		FrameID:    in.FrameID,
		RunScopeID: in.RunScopeID,
		NodeType:   in.NodeType,
		Executor:   in.Executor,
		NodeDef:    in.NodeDef,
	}
	_, err = applyErrorPolicyWithScratch(ctx, args, acq,
		in.ErrorClass, "", map[string]any{"error": in.ErrorClass}, nil, nil, in.Scratch, tx)
	return acq.PriorNodeRunID, acq.PriorDispatchDisposition, err
}
