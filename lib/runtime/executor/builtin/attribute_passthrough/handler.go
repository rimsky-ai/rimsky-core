// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package attribute_passthrough

import (
	"context"
	"fmt"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
)

// @concept: executor
type Handler struct{}

func New() *Handler { return &Handler{} }

func (h *Handler) Execute(_ context.Context, req *genv1.ExecuteRequest, _ executor.HandlerContext) (*genv1.Outcome, error) {
	deltaStruct := req.GetAttributes()
	count := 0
	if deltaStruct != nil {
		count = len(deltaStruct.GetFields())
	}
	return &genv1.Outcome{
		Outcome: &genv1.Outcome_Success{
			Success: &genv1.Success{
				Changed:         count > 0,
				ChangeSummary:   fmt.Sprintf("passthrough: %d attribute(s)", count),
				AttributesDelta: deltaStruct,
			},
		},
	}, nil
}
