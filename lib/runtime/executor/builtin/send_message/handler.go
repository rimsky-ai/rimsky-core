// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package send_message

import (
	"context"
	"encoding/json"
	"fmt"

	"google.golang.org/protobuf/types/known/structpb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
)

// @concept: executor
// @concept: message-sender-node
type Handler struct{}

func New() *Handler { return &Handler{} }

func (h *Handler) Execute(ctx context.Context, req *genv1.ExecuteRequest, hctx executor.HandlerContext) (*genv1.Outcome, error) {
	if hctx.SendCascadeMessage == nil {
		return nil, fmt.Errorf("send_message: HandlerContext.SendCascadeMessage is nil — runtime did not bind the send callback for this dispatch (node-def must declare sends_message)")
	}
	attrs := req.GetAttributes()
	body, err := marshalAttributes(attrs)
	if err != nil {
		return nil, fmt.Errorf("send_message: marshal body: %w", err)
	}
	if _, _, err := hctx.SendCascadeMessage(ctx, body); err != nil {
		return nil, fmt.Errorf("send_message: emit cascade message: %w", err)
	}
	return &genv1.Outcome{
		Outcome: &genv1.Outcome_Success{
			Success: &genv1.Success{
				Changed:       false,
				ChangeSummary: "send_message",
			},
		},
	}, nil
}

func marshalAttributes(attrs *structpb.Struct) ([]byte, error) {
	if attrs == nil {
		return json.Marshal(map[string]any{})
	}
	asMap := attrs.AsMap()
	return json.Marshal(asMap)
}
