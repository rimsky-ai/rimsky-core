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
	attribute_passthrough "github.com/rimsky-ai/rimsky-core/lib/runtime/executor/builtin/attribute_passthrough"
	emit_message "github.com/rimsky-ai/rimsky-core/lib/runtime/executor/builtin/emit_message"
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
	if err := reg.Register(attribute_passthrough.InProcURL, attribute_passthrough.New()); err != nil {
		return fmt.Errorf("register attribute_passthrough handler: %w", err)
	}
	if err := aliases.Register(attribute_passthrough.KindName, attribute_passthrough.ExecutorAlias); err != nil {
		return fmt.Errorf("register attribute_passthrough alias: %w", err)
	}
	if err := reg.Register(emit_message.InProcURL, emit_message.New()); err != nil {
		return fmt.Errorf("register emit_message handler: %w", err)
	}
	if err := aliases.Register(emit_message.KindName, emit_message.ExecutorAlias); err != nil {
		return fmt.Errorf("register emit_message alias: %w", err)
	}
	return nil
}

func RegisterAllInProcessHandlers(reg *executor.InProcessRegistry) error {
	if reg == nil {
		return fmt.Errorf("builtin.RegisterAllInProcessHandlers: registry required")
	}
	if err := reg.Register(loop_counter.InProcURL, loop_counter.New()); err != nil {
		return fmt.Errorf("register loop_counter handler: %w", err)
	}
	if err := reg.Register(attribute_passthrough.InProcURL, attribute_passthrough.New()); err != nil {
		return fmt.Errorf("register attribute_passthrough handler: %w", err)
	}
	if err := reg.Register(emit_message.InProcURL, emit_message.New()); err != nil {
		return fmt.Errorf("register emit_message handler: %w", err)
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
	if err := aliases.Register(attribute_passthrough.KindName, attribute_passthrough.ExecutorAlias); err != nil {
		return fmt.Errorf("register attribute_passthrough alias: %w", err)
	}
	if err := aliases.Register(emit_message.KindName, emit_message.ExecutorAlias); err != nil {
		return fmt.Errorf("register emit_message alias: %w", err)
	}
	return nil
}

func BuiltinExecutorAliases() map[string]executor.Endpoint {
	return map[string]executor.Endpoint{
		loop_counter.ExecutorAlias: {
			Transport: "inproc",
			URL:       loop_counter.InProcURL,
		},
		attribute_passthrough.ExecutorAlias: {
			Transport: "inproc",
			URL:       attribute_passthrough.InProcURL,
		},
		emit_message.ExecutorAlias: {
			Transport: "inproc",
			URL:       emit_message.InProcURL,
		},
	}
}

func IsBuiltinAlias(name string) bool {
	switch name {
	case loop_counter.ExecutorAlias, attribute_passthrough.ExecutorAlias, emit_message.ExecutorAlias:
		return true
	}
	return false
}

func SchemaFor(alias string) ([]byte, bool) {
	switch alias {
	case loop_counter.ExecutorAlias:
		return loop_counter.SchemaBytes(), true
	case attribute_passthrough.ExecutorAlias:
		return attribute_passthrough.SchemaBytes(), true
	case emit_message.ExecutorAlias:
		return emit_message.SchemaBytes(), true
	}
	return nil, false
}

func DeclaredTagsFor(alias string) ([]string, bool) {
	switch alias {
	case loop_counter.ExecutorAlias:
		return loop_counter.DeclaredTags(), true
	case attribute_passthrough.ExecutorAlias:
		return attribute_passthrough.DeclaredTags(), true
	case emit_message.ExecutorAlias:
		return emit_message.DeclaredTags(), true
	}
	return nil, false
}
