// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package cli

import (
	"context"
	"encoding/json"

	rsignal "github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

// @decision: structural-root-edge-injection-at-registration
// @decision: compose-driver-sends-empty-message-after-create
func TemplateHasStructuralRoot(ctx context.Context, c *Client, hash string) (bool, error) {
	if hash == "" {
		return true, nil
	}
	tpl, err := c.GetTemplate(ctx, hash)
	if err != nil {
		return false, err
	}
	if tpl == nil || len(tpl.Spec) == 0 {
		return true, nil
	}
	raw, err := json.Marshal(tpl.Spec)
	if err != nil {
		return false, err
	}
	var ts node.TemplateSpec
	if err := json.Unmarshal(raw, &ts); err != nil {
		return false, err
	}
	msgRefs := node.ExtractMessageRefsFromTemplate(ts)
	edges, err := node.BuildSubscriptionEdges(ts, msgRefs)
	if err != nil {
		return false, err
	}
	if edges == nil {
		return true, nil
	}
	matched := edges.Match("", rsignal.TypePath("terminal/success"))
	return len(matched) > 0, nil
}
