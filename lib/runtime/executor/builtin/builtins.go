// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Package builtin is the single registration site for every rimsky-
// bundled in-process utility executor. The supervisor seeds its
// InProcessRegistry + KindAliasMap via RegisterAll; the control-API
// (which validates templates but does not dispatch) seeds only its
// KindAliasMap via RegisterAllKindAliases. Both call sites consume the
// same per-handler constants, so a new utility executor added under
// lib/runtime/executor/builtin/<name>/ becomes visible to every
// process by adding one line here — not by hunting down per-role
// wiring sites that drift over time.
//
// @concept: executor
// @concept: node
package builtin

import (
	"fmt"

	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
	loop_counter "github.com/rimsky-ai/rimsky-core/lib/runtime/executor/builtin/loop_counter"
)

// RegisterAll seeds the dispatch-side registry + the template-side
// kind-alias map with every rimsky-bundled utility executor. Called by
// the supervisor at startup. Returns a wrapped error naming the
// failing builtin on the first failure so a misconfigured registration
// surfaces clearly at startup rather than as a dispatch-time
// "unresolved_executor" much later.
//
// New builtins join here: add the registry.Register + aliases.Register
// pair in alphabetical order alongside the existing entries.
func RegisterAll(reg *executor.InProcessRegistry, aliases *node.KindAliasMap) error {
	if reg == nil {
		return fmt.Errorf("builtin.RegisterAll: registry required")
	}
	if aliases == nil {
		return fmt.Errorf("builtin.RegisterAll: aliases required")
	}
	if err := reg.Register(loop_counter.InProcURL, loop_counter.New()); err != nil {
		return fmt.Errorf("register loop_counter handler: %w", err)
	}
	if err := aliases.Register(loop_counter.KindName, loop_counter.ExecutorAlias); err != nil {
		return fmt.Errorf("register loop_counter alias: %w", err)
	}
	return nil
}

// RegisterAllKindAliases seeds only the template-side kind-alias map —
// the surface the control-API needs for template registration but does
// not dispatch on. Used by the control-API startup; keeps the
// supervisor's RegisterAll authoritative for the (kind, alias) pairs
// so the two roles can never drift out of step.
//
// New builtins join the RegisterAll helper above; this function
// mirrors RegisterAll's alias half so both surfaces stay seeded from
// the same constants.
func RegisterAllKindAliases(aliases *node.KindAliasMap) error {
	if aliases == nil {
		return fmt.Errorf("builtin.RegisterAllKindAliases: aliases required")
	}
	if err := aliases.Register(loop_counter.KindName, loop_counter.ExecutorAlias); err != nil {
		return fmt.Errorf("register loop_counter alias: %w", err)
	}
	return nil
}

// BuiltinExecutorAliases returns the (executor alias → inproc endpoint)
// pairs each bundled utility executor contributes so the dispatch-side
// resolver can be seeded uniformly at supervisor startup. The
// supervisor walks the result and calls Resolver.Register for each
// entry (handling LateBindResolver unwrap on its side).
//
// Mirrors RegisterAll's structure so a new builtin landing under
// lib/runtime/executor/builtin/<name>/ joins all three surfaces
// (registry, kind-alias, resolver-endpoint) by editing this one file.
func BuiltinExecutorAliases() map[string]executor.Endpoint {
	return map[string]executor.Endpoint{
		loop_counter.ExecutorAlias: {
			Transport: "inproc",
			URL:       loop_counter.InProcURL,
		},
	}
}
