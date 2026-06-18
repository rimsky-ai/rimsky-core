// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package executor

import (
	"context"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// @concept: executor
type HandlerContext struct {
	Scratch *ScratchWriter
}

// @concept: executor
type InProcessHandler interface {
	Execute(ctx context.Context, req *genv1.ExecuteRequest, hctx HandlerContext) (*genv1.Outcome, error)
}
