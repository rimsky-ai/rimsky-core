// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package controlapi

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor/builtin"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor/builtin/loop_counter"
)

func builtinAliasKindMap(t *testing.T) *node.KindAliasMap {
	t.Helper()
	m := node.NewKindAliasMap()
	require.NoError(t, builtin.RegisterAllKindAliases(m))
	return m
}

func TestValidatorHooks_BuiltinDeclaredErrorClasses_CapabilitiesBranch(t *testing.T) {
	t.Parallel()
	deps := AppDeps{
		KindAliases: builtinAliasKindMap(t),
		ExecutorCapabilities: func(string) ([]string, []string, []byte, bool) {
			return nil, nil, nil, false
		},
	}
	hooks, _ := validatorHooksFor(deps, node.TemplateSpec{})
	classes, ok := hooks.ExecutorDeclaredErrorClasses(loop_counter.ExecutorAlias)
	require.True(t, ok, "builtin alias must advertise a known error-class vocabulary")
	require.Contains(t, classes, loop_counter.AttributesSchemaFailedClass)
}

func TestValidatorHooks_BuiltinDeclaredErrorClasses_KindAliasesBranch(t *testing.T) {
	t.Parallel()
	deps := AppDeps{KindAliases: builtinAliasKindMap(t)}
	hooks, _ := validatorHooksFor(deps, node.TemplateSpec{})
	classes, ok := hooks.ExecutorDeclaredErrorClasses(loop_counter.ExecutorAlias)
	require.True(t, ok, "builtin alias must advertise a known error-class vocabulary")
	require.Contains(t, classes, loop_counter.AttributesSchemaFailedClass)
}

func loopCounterErrorTypesSpec(className string) node.TemplateSpec {
	return node.TemplateSpec{
		Name:    "builtin-error-class-vocab",
		Version: "1",
		Nodes: []spec.TemplateNodeDef{
			{
				Type:     "counter",
				Executor: loop_counter.ExecutorAlias,
				Attributes: &spec.NodeAttributesDef{
					Schema: map[string]any{"max": 3},
				},
				ErrorTypes: map[string]spec.ErrorTypePolicy{
					className: {Action: spec.ActionGiveUp},
				},
			},
		},
	}
}

func vocabularyWarnings(res node.ValidationResult) []string {
	var msgs []string
	for _, w := range res.Warnings {
		if strings.Contains(w.Msg, "not in any declared vocabulary") {
			msgs = append(msgs, w.Path+": "+w.Msg)
		}
	}
	return msgs
}

func TestTemplateRegistration_LoopCounterEmittedErrorClassIsInVocabulary(t *testing.T) {
	t.Parallel()
	deps := AppDeps{KindAliases: builtinAliasKindMap(t)}
	tmpl := loopCounterErrorTypesSpec(loop_counter.AttributesSchemaFailedClass)
	hooks, _ := validatorHooksFor(deps, tmpl)
	res := node.ValidateTemplate(&tmpl, hooks)
	require.Empty(t, vocabularyWarnings(res),
		"error_types on loop_counter's genuinely-emitted class must not draw a vocabulary warning")
}

func TestTemplateRegistration_LoopCounterUndeclaredErrorClassStillWarns(t *testing.T) {
	t.Parallel()
	deps := AppDeps{KindAliases: builtinAliasKindMap(t)}
	tmpl := loopCounterErrorTypesSpec("no_such_class")
	hooks, _ := validatorHooksFor(deps, tmpl)
	res := node.ValidateTemplate(&tmpl, hooks)
	require.NotEmpty(t, vocabularyWarnings(res),
		"an error class outside the builtin's declared vocabulary must keep drawing the advisory warning")
}
