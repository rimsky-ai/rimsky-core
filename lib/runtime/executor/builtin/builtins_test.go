// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package builtin

import (
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor/builtin/attribute_passthrough"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor/builtin/loop_counter"
)

func TestRegisterAll_RegistersBothBuiltins(t *testing.T) {
	t.Parallel()
	reg := executor.NewInProcessRegistry()
	aliases := node.NewKindAliasMap()
	if err := RegisterAll(reg, aliases); err != nil {
		t.Fatalf("RegisterAll: %v", err)
	}
	for _, url := range []string{loop_counter.InProcURL, attribute_passthrough.InProcURL} {
		if _, ok := reg.Lookup(url); !ok {
			t.Errorf("registry does not contain %s after RegisterAll", url)
		}
	}
}

func TestIsBuiltinAlias_BothBuiltins(t *testing.T) {
	t.Parallel()
	for _, alias := range []string{loop_counter.ExecutorAlias, attribute_passthrough.ExecutorAlias} {
		if !IsBuiltinAlias(alias) {
			t.Errorf("IsBuiltinAlias(%q) = false, want true", alias)
		}
	}
	if IsBuiltinAlias("rimsky.not_a_real_builtin") {
		t.Errorf("IsBuiltinAlias of unknown alias returned true, want false")
	}
}

func TestSchemaFor_BothBuiltins(t *testing.T) {
	t.Parallel()
	for _, alias := range []string{loop_counter.ExecutorAlias, attribute_passthrough.ExecutorAlias} {
		schema, ok := SchemaFor(alias)
		if !ok {
			t.Errorf("SchemaFor(%q) = !ok, want ok", alias)
			continue
		}
		if len(schema) == 0 {
			t.Errorf("SchemaFor(%q) returned empty bytes", alias)
		}
	}
	if _, ok := SchemaFor("rimsky.not_a_real_builtin"); ok {
		t.Errorf("SchemaFor of unknown alias returned ok, want !ok")
	}
}

func TestDeclaredTagsFor_BothBuiltins(t *testing.T) {
	t.Parallel()
	for _, alias := range []string{loop_counter.ExecutorAlias, attribute_passthrough.ExecutorAlias} {
		_, ok := DeclaredTagsFor(alias)
		if !ok {
			t.Errorf("DeclaredTagsFor(%q) = !ok, want ok", alias)
		}
	}
	if _, ok := DeclaredTagsFor("rimsky.not_a_real_builtin"); ok {
		t.Errorf("DeclaredTagsFor of unknown alias returned ok, want !ok")
	}
}

func TestBuiltinExecutorAliases_BothBuiltins(t *testing.T) {
	t.Parallel()
	aliases := BuiltinExecutorAliases()
	for _, want := range []string{loop_counter.ExecutorAlias, attribute_passthrough.ExecutorAlias} {
		ep, ok := aliases[want]
		if !ok {
			t.Errorf("BuiltinExecutorAliases missing %q", want)
			continue
		}
		if ep.Transport != "inproc" {
			t.Errorf("alias %q: transport = %q, want \"inproc\"", want, ep.Transport)
		}
		if ep.URL == "" {
			t.Errorf("alias %q: URL is empty", want)
		}
	}
}
