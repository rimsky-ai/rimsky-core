// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// structural_root.go — shared template-introspection helper used by the
// CLI verbs that emit a post-create empty-message wake. `rimsky run`
// (in cmd/rimsky/cli/run.go) and `rimsky compose run` (in
// cmd/rimsky/cli/compose/run.go) both need to decide whether to follow
// instance-create with an empty wake; the decision depends on whether
// the deployed template has at least one structural root. Hoisting the
// helper into the `cli` package keeps the two verbs from diverging.
package cli

import (
	"context"
	"encoding/json"

	rsignal "github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

// TemplateHasStructuralRoot returns true iff the deployed template
// identified by `hash` has at least one structural root — a node whose
// author-declared `subscribes:` block is empty or absent (per
// decision:structural-root-edge-injection-at-registration). Fetches
// the template spec via the control-api `GetTemplate` route,
// round-trips the loose `map[string]any` projection into
// `node.TemplateSpec`, runs `node.BuildSubscriptionEdges`, and looks
// for a `SenderBoundToEmpty=true` edge under sender="". On any lookup
// or parse failure the helper conservatively returns true so the
// caller proceeds with the wake (the run still works, it just pays a
// hang-budget on a no-root template).
//
// An empty `hash` is treated as a conservative pass-through (returns
// true with no error). Callers that resolve a template hash from a
// server response sometimes get an empty value back when the apply
// step did not surface a hash; matching the rest of the helper's
// failure modes, the empty case falls through to the wake path.
//
// @decision: structural-root-edge-injection-at-registration
// @decision: compose-driver-emits-empty-message-after-create
func TemplateHasStructuralRoot(ctx context.Context, c *Client, hash string) (bool, error) {
	if hash == "" {
		// @deliberate: empty hash is treated as a conservative
		// pass-through; fall through to the wake path so the caller
		// still emits the empty message and gets the original
		// behavior (a hang-budget on a no-root template at worst).
		return true, nil
	}
	tpl, err := c.GetTemplate(ctx, hash)
	if err != nil {
		return false, err
	}
	if tpl == nil || len(tpl.Spec) == 0 {
		// @deliberate: conservative — if the projection is missing the
		// spec field, fall back to the original behavior (post wake).
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
