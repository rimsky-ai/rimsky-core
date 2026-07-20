// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package node

import (
	"fmt"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

// @concept: node-subscription
// @concept: observability
func validateSubscriptionDeclaredTags(
	s spec.SubscriptionEntry,
	sbase string,
	sender TemplateNodeDef,
	hooks RegistryHooks,
	res *ValidationResult,
) {
	senderExecutor := effectiveExecutor(sender, hooks)
	if s.When == "" || hooks.ExecutorDeclaredTags == nil || senderExecutor == "" {
		return
	}
	declaredTags, ok := hooks.ExecutorDeclaredTags(senderExecutor)
	if !ok {
		return
	}
	declaredSet := make(map[string]struct{}, len(declaredTags))
	for _, dt := range declaredTags {
		declaredSet[dt] = struct{}{}
	}
	for _, tag := range extractPayloadTagLiterals(s.When) {
		if _, present := declaredSet[tag]; present {
			continue
		}
		res.Errors = append(res.Errors, ValidationError{
			Path: sbase + ".when",
			Msg: fmt.Sprintf("tag %q referenced in `payload.tags` filter is not declared by sender %q's executor %q",
				tag, s.Node, senderExecutor),
		})
	}
}
