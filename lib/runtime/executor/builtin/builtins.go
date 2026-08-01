// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: executor
// @concept: node
package builtin

import (
	"fmt"

	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor/builtin/attribute_passthrough"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor/builtin/loop_counter"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor/builtin/send_message"
)

type builtinEntry struct {
	kindName      string
	executorAlias string
	inprocURL     string
	handler       executor.InProcessHandler
	schema        []byte
	tags          []string
	errorClasses  []string
}

func builtinEntries() []builtinEntry {
	return []builtinEntry{
		{
			kindName:      loop_counter.KindName,
			executorAlias: loop_counter.ExecutorAlias,
			inprocURL:     loop_counter.InProcURL,
			handler:       loop_counter.New(),
			schema:        loop_counter.SchemaBytes(),
			tags:          loop_counter.DeclaredTags(),
			errorClasses:  loop_counter.DeclaredErrorClasses(),
		},
		{
			kindName:      attribute_passthrough.KindName,
			executorAlias: attribute_passthrough.ExecutorAlias,
			inprocURL:     attribute_passthrough.InProcURL,
			handler:       attribute_passthrough.New(),
			schema:        attribute_passthrough.SchemaBytes(),
			tags:          attribute_passthrough.DeclaredTags(),
			errorClasses:  attribute_passthrough.DeclaredErrorClasses(),
		},
		{
			kindName:      send_message.KindName,
			executorAlias: send_message.ExecutorAlias,
			inprocURL:     send_message.InProcURL,
			handler:       send_message.New(),
			schema:        send_message.SchemaBytes(),
			tags:          send_message.DeclaredTags(),
			errorClasses:  send_message.DeclaredErrorClasses(),
		},
	}
}

func RegisterAllInProcessHandlers(reg *executor.InProcessRegistry) error {
	if reg == nil {
		return fmt.Errorf("builtin.RegisterAllInProcessHandlers: registry required")
	}
	return registerHandlers(reg)
}

func RegisterAllKindAliases(aliases *node.KindAliasMap) error {
	if aliases == nil {
		return fmt.Errorf("builtin.RegisterAllKindAliases: aliases required")
	}
	return registerAliases(aliases)
}

func registerHandlers(reg *executor.InProcessRegistry) error {
	for _, e := range builtinEntries() {
		if err := reg.Register(e.inprocURL, e.handler); err != nil {
			return fmt.Errorf("register %s handler: %w", e.kindName, err)
		}
	}
	return nil
}

func registerAliases(aliases *node.KindAliasMap) error {
	for _, e := range builtinEntries() {
		if err := aliases.Register(e.kindName, e.executorAlias); err != nil {
			return fmt.Errorf("register %s alias: %w", e.kindName, err)
		}
	}
	return nil
}

func BuiltinExecutorAliases() map[string]executor.Endpoint {
	out := make(map[string]executor.Endpoint, len(builtinEntries()))
	for _, e := range builtinEntries() {
		out[e.executorAlias] = executor.Endpoint{
			Transport: "inproc",
			URL:       e.inprocURL,
		}
	}
	return out
}

func IsBuiltinAlias(name string) bool {
	for _, e := range builtinEntries() {
		if e.executorAlias == name {
			return true
		}
	}
	return false
}

func SchemaFor(alias string) ([]byte, bool) {
	for _, e := range builtinEntries() {
		if e.executorAlias == alias {
			return e.schema, true
		}
	}
	return nil, false
}

func DeclaredTagsFor(alias string) ([]string, bool) {
	for _, e := range builtinEntries() {
		if e.executorAlias == alias {
			return e.tags, true
		}
	}
	return nil, false
}

func DeclaredErrorClassesFor(alias string) ([]string, bool) {
	for _, e := range builtinEntries() {
		if e.executorAlias == alias {
			return e.errorClasses, true
		}
	}
	return nil, false
}
