// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package cli

import (
	"context"
	"encoding/json"

	rsignal "github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

// @decision: structural-root-edge-injection-at-registration
// @decision: compose-driver-emits-empty-message-after-create
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
		return true, nil
	}
	var ts node.TemplateSpec
	if uerr := json.Unmarshal(raw, &ts); uerr != nil {
		return true, nil
	}
	subRefs := node.ExtractSubstitutionRefsFromTemplate(ts)
	msgRefs := node.ExtractMessageRefsFromTemplate(ts)
	edges, err := node.BuildSubscriptionEdges(ts, subRefs, msgRefs)
	if err != nil || edges == nil {
		return true, nil
	}
	matched := edges.Match("", rsignal.TypePath("terminal/success"))
	for _, e := range matched {
		if e.SenderBoundToEmpty {
			return true, nil
		}
	}
	return false, nil
}
