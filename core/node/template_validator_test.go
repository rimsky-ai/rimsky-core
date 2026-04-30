package node

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// knownStores is the default store name set used by most tests. The
// v3 validator no longer looks up store kinds; it only checks that
// referenced names are declared.
var knownStores = map[string]string{
	"content": "filesystem",
	"shared":  "filesystem",
	"topics":  "postgres",
	"inbound": "postgres",
}

func storeDeclaredLookup(known map[string]string) func(string) bool {
	return func(name string) bool {
		_, ok := known[name]
		return ok
	}
}

// hasErrorAt asserts that the result carries an error whose Path
// starts with prefix.
func hasErrorAt(t *testing.T, res ValidationResult, prefix string) {
	t.Helper()
	for _, e := range res.Errors {
		if len(prefix) <= len(e.Path) && e.Path[:len(prefix)] == prefix {
			return
		}
	}
	t.Fatalf("expected error with path prefix %q, got %+v", prefix, res.Errors)
}

func TestValidateTemplate_Ok_MinimalExecutorNode(t *testing.T) {
	spec := &TemplateSpec{
		Name:            "demo",
		Version:         "1.0.0",
		FrameResolution: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "handler.a",
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}

func TestValidateTemplate_Error_DependencyToUnknownNode(t *testing.T) {
	spec := &TemplateSpec{
		Name:            "demo",
		Version:         "1.0.0",
		FrameResolution: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{{
			Type:         "a",
			Executor:     "handler.a",
			Dependencies: []string{"ghost"},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].dependencies[0]")
}

func TestValidateTemplate_Error_FrameResolutionMissing(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes:   []TemplateNodeDef{{Type: "a", Executor: "h"}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "frame_resolution")
}

func TestValidateStores_Ok_RegionClaimWithIntent(t *testing.T) {
	spec := &TemplateSpec{
		Name:            "demo",
		Version:         "1.0.0",
		FrameResolution: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "h",
			Stores: []NodeStoreRef{
				{Name: "content", Selector: "/data/x", Intent: "rw"},
			},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}

func TestValidateStores_Error_MissingIntent(t *testing.T) {
	spec := &TemplateSpec{
		Name:            "demo",
		Version:         "1.0.0",
		FrameResolution: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "h",
			Stores:   []NodeStoreRef{{Name: "content", Selector: "/data/x"}},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].stores[0].intent")
}

func TestValidateStores_Error_DuplicateAlias(t *testing.T) {
	spec := &TemplateSpec{
		Name:            "demo",
		Version:         "1.0.0",
		FrameResolution: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "h",
			Stores: []NodeStoreRef{
				{Name: "content", Selector: "/x", Intent: "r", Alias: "shared"},
				{Name: "shared", Selector: "/y", Intent: "r", Alias: "shared"},
			},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].stores[1].alias")
}

func TestValidateStores_Error_UnknownStoreKind(t *testing.T) {
	spec := &TemplateSpec{
		Name:            "demo",
		Version:         "1.0.0",
		FrameResolution: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "h",
			Stores:   []NodeStoreRef{{Name: "ghost", Selector: "/x", Intent: "r"}},
		}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].stores[0].name")
}

func TestValidateInheritance_Ok_HeldClaim(t *testing.T) {
	spec := &TemplateSpec{
		Name:            "demo",
		Version:         "1.0.0",
		FrameResolution: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{
			{
				Type: "pick", Executor: "h",
				Stores: []NodeStoreRef{
					{Name: "topics", Selector: "@queue", Intent: "rw", Alias: "queue"},
				},
			},
			{
				Type: "process", Executor: "h",
				Dependencies: []string{"pick"},
				Inherits:     []InheritEntry{{Claim: "queue"}},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}

func TestValidateInheritance_Error_UnknownAlias(t *testing.T) {
	spec := &TemplateSpec{
		Name:            "demo",
		Version:         "1.0.0",
		FrameResolution: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{
			{Type: "pick", Executor: "h"},
			{
				Type: "process", Executor: "h",
				Dependencies: []string{"pick"},
				Inherits:     []InheritEntry{{Claim: "ghost"}},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[1].inherits[0].claim")
}

func TestValidateInheritance_Error_AliasNotReachableViaDeps(t *testing.T) {
	spec := &TemplateSpec{
		Name:            "demo",
		Version:         "1.0.0",
		FrameResolution: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{
			{
				Type: "isolated", Executor: "h",
				Stores: []NodeStoreRef{
					{Name: "topics", Selector: "@queue", Intent: "rw", Alias: "queue"},
				},
			},
			{
				Type:     "downstream",
				Executor: "h",
				// No deps on "isolated".
				Inherits: []InheritEntry{{Claim: "queue"}},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[1].inherits[0].claim")
}

func TestValidateInheritance_Error_AmbiguousAcquirers(t *testing.T) {
	// Two distinct nodes both acquire alias "queue", and a downstream
	// node depends on BOTH and inherits "queue". The validator must
	// reject because the runtime cannot pick a deterministic acquirer.
	spec := &TemplateSpec{
		Name:            "demo",
		Version:         "1.0.0",
		FrameResolution: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{
			{
				Type: "pick_a", Executor: "h",
				Stores: []NodeStoreRef{
					{Name: "topics", Selector: "@queue", Intent: "rw", Alias: "queue"},
				},
			},
			{
				Type: "pick_b", Executor: "h",
				Stores: []NodeStoreRef{
					{Name: "topics", Selector: "@queue", Intent: "rw", Alias: "queue"},
				},
			},
			{
				Type:         "downstream",
				Executor:     "h",
				Dependencies: []string{"pick_a", "pick_b"},
				Inherits:     []InheritEntry{{Claim: "queue"}},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreDeclared: storeDeclaredLookup(knownStores)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[2].inherits[0].claim")
	// Confirm the error message specifically calls out the
	// reachable-acquirer count, distinguishing this from the
	// unknown-alias and not-reachable-via-deps cases.
	var found bool
	for _, e := range res.Errors {
		if e.Path == "nodes[2].inherits[0].claim" &&
			strings.Contains(e.Msg, "acquirers are reachable") {
			found = true
			break
		}
	}
	require.True(t, found, "expected error mentioning %q, got %+v", "acquirers are reachable", res.Errors)
}

func TestHoldingSubgraphsForTemplate_HeldChain(t *testing.T) {
	spec := &TemplateSpec{
		Nodes: []TemplateNodeDef{
			{
				Type: "pick",
				Stores: []NodeStoreRef{
					{Name: "topics", Selector: "@queue", Intent: "rw", Alias: "queue"},
				},
			},
			{
				Type: "process", Dependencies: []string{"pick"},
				Inherits: []InheritEntry{{Claim: "queue"}},
			},
		},
	}
	subs := HoldingSubgraphsForTemplate(spec)
	require.Len(t, subs, 1)
	require.Equal(t, "pick", subs[0].AcquirerType)
	require.Equal(t, "queue", subs[0].Alias)
	require.Equal(t, []string{"pick", "process"}, subs[0].Members)
	require.True(t, subs[0].IsHeld())
}

func TestHoldingSubgraphsForTemplate_NotHeld(t *testing.T) {
	spec := &TemplateSpec{
		Nodes: []TemplateNodeDef{
			{
				Type: "loner",
				Stores: []NodeStoreRef{
					{Name: "topics", Selector: "@queue", Intent: "rw", Alias: "queue"},
				},
			},
		},
	}
	subs := HoldingSubgraphsForTemplate(spec)
	require.Len(t, subs, 1)
	require.False(t, subs[0].IsHeld())
}
