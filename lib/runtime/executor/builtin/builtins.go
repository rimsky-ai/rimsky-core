// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: executor
// @concept: node
package builtin

import (
	"fmt"

	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
	loop_counter "github.com/rimsky-ai/rimsky-core/lib/runtime/executor/builtin/loop_counter"
)

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

func RegisterAllKindAliases(aliases *node.KindAliasMap) error {
	if aliases == nil {
		return fmt.Errorf("builtin.RegisterAllKindAliases: aliases required")
	}
	if err := aliases.Register(loop_counter.KindName, loop_counter.ExecutorAlias); err != nil {
		return fmt.Errorf("register loop_counter alias: %w", err)
	}
	return nil
}

func BuiltinExecutorAliases() map[string]executor.Endpoint {
	return map[string]executor.Endpoint{
		loop_counter.ExecutorAlias: {
			Transport: "inproc",
			URL:       loop_counter.InProcURL,
		},
	}
}
