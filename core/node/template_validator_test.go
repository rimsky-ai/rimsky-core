package node

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// knownStores is the default store-kind lookup used by most tests.
var knownStores = map[string]string{
	"content": "filesystem",
	"shared":  "filesystem",
	"topics":  "postgres",
	"inbound": "postgres",
}

func storeKindLookup(known map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		k, ok := known[name]
		return k, ok
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
	res := ValidateTemplate(spec, RegistryHooks{StoreKindOf: storeKindLookup(knownStores)})
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
	res := ValidateTemplate(spec, RegistryHooks{StoreKindOf: storeKindLookup(knownStores)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].dependencies[0]")
}

func TestValidateTemplate_Error_FrameResolutionMissing(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes:   []TemplateNodeDef{{Type: "a", Executor: "h"}},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreKindOf: storeKindLookup(knownStores)})
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
	res := ValidateTemplate(spec, RegistryHooks{StoreKindOf: storeKindLookup(knownStores)})
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
	res := ValidateTemplate(spec, RegistryHooks{StoreKindOf: storeKindLookup(knownStores)})
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
	res := ValidateTemplate(spec, RegistryHooks{StoreKindOf: storeKindLookup(knownStores)})
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
	res := ValidateTemplate(spec, RegistryHooks{StoreKindOf: storeKindLookup(knownStores)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].stores[0].name")
}

func TestValidateInheritance_Ok_HeldClaimWithResolutions(t *testing.T) {
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
				ClaimResolutions: map[string]ClaimResolution{
					"queue": {OnCommit: "delete", OnGiveUp: "release_to_head"},
				},
			},
			{
				Type: "process", Executor: "h",
				Dependencies: []string{"pick"},
				Inherits:     []InheritEntry{{Claim: "queue"}},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreKindOf: storeKindLookup(knownStores)})
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}

func TestValidateInheritance_Error_HeldClaimMissingResolutions(t *testing.T) {
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
				// no ClaimResolutions
			},
			{
				Type: "process", Executor: "h",
				Dependencies: []string{"pick"},
				Inherits:     []InheritEntry{{Claim: "queue"}},
			},
		},
	}
	res := ValidateTemplate(spec, RegistryHooks{StoreKindOf: storeKindLookup(knownStores)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].claim_resolutions")
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
	res := ValidateTemplate(spec, RegistryHooks{StoreKindOf: storeKindLookup(knownStores)})
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
				ClaimResolutions: map[string]ClaimResolution{
					"queue": {OnCommit: "delete", OnGiveUp: "release_to_head"},
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
	res := ValidateTemplate(spec, RegistryHooks{StoreKindOf: storeKindLookup(knownStores)})
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[1].inherits[0].claim")
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
