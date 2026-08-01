// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package executor

import (
	"context"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// @concept: executor
type HandlerContext struct {
	// @concept: message-sender-node
	SendCascadeMessage func(ctx context.Context, body []byte) (messageID shared.UUID, replayed bool, err error)
}

// @concept: executor
// @decision: inproc-handler-interface
type InProcessHandler interface {
	Execute(ctx context.Context, req *genv1.ExecuteRequest, hctx HandlerContext) (*genv1.Outcome, error)
}

type dispatchExtrasKey struct{}

// @concept: executor
type DispatchExtras struct {
	InstanceID      shared.UUID
	NodeID          shared.UUID
	FrameID         shared.UUID
	SendMessageType string
}

func WithDispatchExtras(ctx context.Context, x DispatchExtras) context.Context {
	return context.WithValue(ctx, dispatchExtrasKey{}, x)
}

func DispatchExtrasFromContext(ctx context.Context) (DispatchExtras, bool) {
	v, ok := ctx.Value(dispatchExtrasKey{}).(DispatchExtras)
	return v, ok
}
