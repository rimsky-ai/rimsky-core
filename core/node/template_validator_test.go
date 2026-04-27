package node

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/store"
)

// knownStores is the default store-kind lookup used by most tests.
// Centralised so tests can swap in narrower sets when exercising
// unknown-store paths.
var knownStores = map[string]string{
	"content":     "filesystem",
	"shared":      "filesystem",
	"topics-ring": "claim_store",
	"inbound":     "claim_store",
}

func storeKindLookup(known map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		k, ok := known[name]
		return k, ok
	}
}

// hasErrorAt asserts that the result carries an error whose Path starts with
// prefix. We match on prefix so tests don't couple to exact JSONPath indexing.
func hasErrorAt(t *testing.T, res ValidationResult, prefix string) {
	t.Helper()
	for _, e := range res.Errors {
		if len(prefix) <= len(e.Path) && e.Path[:len(prefix)] == prefix {
			return
		}
	}
	t.Fatalf("expected error with path prefix %q, got %+v", prefix, res.Errors)
}

func hasWarningAt(t *testing.T, res ValidationResult, prefix string) {
	t.Helper()
	for _, w := range res.Warnings {
		if len(prefix) <= len(w.Path) && w.Path[:len(prefix)] == prefix {
			return
		}
	}
	t.Fatalf("expected warning with path prefix %q, got %+v", prefix, res.Warnings)
}

// --------------------------------------------------------------------------
// preserved structural tests (top-level template wiring)
// --------------------------------------------------------------------------

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
	res := ValidateTemplate(spec, storeKindLookup(knownStores))
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
	assert.Empty(t, res.Warnings)
}

func TestValidateTemplate_Ok_PureCascadeNode(t *testing.T) {
	spec := &TemplateSpec{
		Name:            "demo",
		Version:         "1.0.0",
		FrameResolution: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{
			{Type: "leaf", Executor: "handler.leaf"},
			{Type: "fanout", Dependencies: []string{"leaf"}},
		},
	}
	res := ValidateTemplate(spec, storeKindLookup(knownStores))
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
	res := ValidateTemplate(spec, storeKindLookup(knownStores))
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].dependencies[0]")
}

func TestValidateTemplate_Error_DependencyCycle(t *testing.T) {
	spec := &TemplateSpec{
		Name:            "demo",
		Version:         "1.0.0",
		FrameResolution: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{
			{Type: "a", Executor: "h", Dependencies: []string{"b"}},
			{Type: "b", Executor: "h", Dependencies: []string{"c"}},
			{Type: "c", Executor: "h", Dependencies: []string{"a"}},
		},
	}
	res := ValidateTemplate(spec, storeKindLookup(knownStores))
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes")
	found := false
	for _, e := range res.Errors {
		if e.Path == "nodes" {
			found = true
			assert.Contains(t, e.Msg, "cycle")
		}
	}
	assert.True(t, found, "expected a cycle error on nodes path")
}

func TestValidateTemplate_Error_InvalidScheduleCron(t *testing.T) {
	spec := &TemplateSpec{
		Name:            "demo",
		Version:         "1.0.0",
		FrameResolution: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{{
			Type:     "timer",
			Executor: "h",
			Schedule: "not a cron expression",
		}},
	}
	res := ValidateTemplate(spec, storeKindLookup(knownStores))
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].schedule")
}

func TestValidateTemplate_Ok_ValidScheduleCron(t *testing.T) {
	spec := &TemplateSpec{
		Name:            "demo",
		Version:         "1.0.0",
		FrameResolution: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{{
			Type:     "timer",
			Executor: "h",
			Schedule: "*/5 * * * *",
		}},
	}
	res := ValidateTemplate(spec, storeKindLookup(knownStores))
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}

func TestValidateTemplate_Warning_PureCascadeHasUserdata(t *testing.T) {
	spec := &TemplateSpec{
		Name:            "demo",
		Version:         "1.0.0",
		FrameResolution: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{{
			Type:     "fanout",
			Userdata: map[string]any{"k": "v"},
		}},
	}
	res := ValidateTemplate(spec, storeKindLookup(knownStores))
	assert.True(t, res.Ok(), "unexpected errors: %+v", res.Errors)
	hasWarningAt(t, res, "nodes[0].userdata")
}

func TestValidateTemplate_Error_InvalidTargetsInErrorType(t *testing.T) {
	spec := &TemplateSpec{
		Name:            "demo",
		Version:         "1.0.0",
		FrameResolution: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "h",
			ErrorTypes: map[string]ErrorTypePolicy{
				"network": {Policy: []PolicyAction{{
					Action:  "invalidate",
					Targets: []string{"ghost"},
				}}},
			},
		}},
	}
	res := ValidateTemplate(spec, storeKindLookup(knownStores))
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].error_types[network].policy[0].targets[0]")
}

func TestValidateTemplate_Error_EmptyName(t *testing.T) {
	spec := &TemplateSpec{
		Name:            "",
		Version:         "1.0.0",
		FrameResolution: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "h",
		}},
	}
	res := ValidateTemplate(spec, storeKindLookup(knownStores))
	require.False(t, res.Ok())
	hasErrorAt(t, res, "name")
}

// --------------------------------------------------------------------------
// validateStores
// --------------------------------------------------------------------------

func TestValidateStores_Ok_FilesystemReadWrite(t *testing.T) {
	spec := &TemplateSpec{
		Name:            "demo",
		Version:         "1.0.0",
		FrameResolution: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{{
			Type:     "draft",
			Executor: "h",
			Stores: []NodeStoreRef{{
				Name:  "content",
				Write: []string{"items/{{params.area}}/{{params.subtopic}}.md"},
				Read:  []string{"items/**", "shared/**"},
			}},
		}},
	}
	res := ValidateTemplate(spec, storeKindLookup(knownStores))
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}

func TestValidateStores_Ok_ClaimStoreClaim(t *testing.T) {
	spec := &TemplateSpec{
		Name:            "demo",
		Version:         "1.0.0",
		FrameResolution: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{{
			Type: "claim-topic",
			Stores: []NodeStoreRef{{
				Name:  "topics-ring",
				Claim: true,
				Hold:  true,
			}},
			ClaimResolutions: []ClaimResolutionRef{
				{Source: "claim-topic", Store: "topics-ring"},
			},
		}},
	}
	res := ValidateTemplate(spec, storeKindLookup(knownStores))
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}

func TestValidateStores_Error_UnknownStore(t *testing.T) {
	spec := &TemplateSpec{
		Name:            "demo",
		Version:         "1.0.0",
		FrameResolution: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "h",
			Stores:   []NodeStoreRef{{Name: "mystery"}},
		}},
	}
	res := ValidateTemplate(spec, storeKindLookup(knownStores))
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].stores[0].name")
}

func TestValidateStores_Error_DuplicateStoreName(t *testing.T) {
	spec := &TemplateSpec{
		Name:            "demo",
		Version:         "1.0.0",
		FrameResolution: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "h",
			Stores: []NodeStoreRef{
				{Name: "content", Read: []string{"a/**"}},
				{Name: "content", Read: []string{"b/**"}},
			},
		}},
	}
	res := ValidateTemplate(spec, storeKindLookup(knownStores))
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].stores[1].name")
}

func TestValidateStores_Error_ClaimOnFilesystem(t *testing.T) {
	spec := &TemplateSpec{
		Name:            "demo",
		Version:         "1.0.0",
		FrameResolution: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{{
			Type: "a",
			Stores: []NodeStoreRef{{
				Name:  "content", // filesystem-kind, not claim_store
				Claim: true,
			}},
		}},
	}
	res := ValidateTemplate(spec, storeKindLookup(knownStores))
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].stores[0].claim")
}

func TestValidateStores_Error_WriteOnClaimStore(t *testing.T) {
	spec := &TemplateSpec{
		Name:            "demo",
		Version:         "1.0.0",
		FrameResolution: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{{
			Type: "a",
			Stores: []NodeStoreRef{{
				Name:  "topics-ring", // claim_store-kind
				Write: []string{"items/foo.md"},
			}},
		}},
	}
	res := ValidateTemplate(spec, storeKindLookup(knownStores))
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].stores[0].write")
}

func TestValidateStores_Error_ReadOnClaimStore(t *testing.T) {
	spec := &TemplateSpec{
		Name:            "demo",
		Version:         "1.0.0",
		FrameResolution: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{{
			Type: "a",
			Stores: []NodeStoreRef{{
				Name: "topics-ring",
				Read: []string{"items/**"},
			}},
		}},
	}
	res := ValidateTemplate(spec, storeKindLookup(knownStores))
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].stores[0].read")
}

func TestValidateStores_Error_HoldRequiresClaim(t *testing.T) {
	spec := &TemplateSpec{
		Name:            "demo",
		Version:         "1.0.0",
		FrameResolution: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{{
			Type: "a",
			Stores: []NodeStoreRef{{
				Name: "topics-ring",
				Hold: true, // missing Claim:true
			}},
		}},
	}
	res := ValidateTemplate(spec, storeKindLookup(knownStores))
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].stores[0].hold")
}

func TestValidateStores_Error_InvalidPlaceholderInRegion(t *testing.T) {
	spec := &TemplateSpec{
		Name:            "demo",
		Version:         "1.0.0",
		FrameResolution: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "h",
			Stores: []NodeStoreRef{{
				Name:  "content",
				Write: []string{"items/{not_a_placeholder}.md"},
			}},
		}},
	}
	res := ValidateTemplate(spec, storeKindLookup(knownStores))
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].stores[0].write[0]")
}

func TestValidateStores_Error_MalformedDispatchDirective(t *testing.T) {
	spec := &TemplateSpec{
		Name:            "demo",
		Version:         "1.0.0",
		FrameResolution: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "h",
			Stores: []NodeStoreRef{{
				Name:  "content",
				Write: []string{"items/{{badprefix.foo}}.md"},
			}},
		}},
	}
	res := ValidateTemplate(spec, storeKindLookup(knownStores))
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].stores[0].write[0]")
}

// --------------------------------------------------------------------------
// validateLocks
// --------------------------------------------------------------------------

func TestValidateLocks_Ok_MutexAndCounting(t *testing.T) {
	spec := &TemplateSpec{
		Name:            "demo",
		Version:         "1.0.0",
		FrameResolution: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "h",
			Locks: []NodeLockRef{
				{Name: "tenant-lock", Mode: store.LockModeMutex},
				{Name: "model-budget", Mode: store.LockModeCounting, Limit: 50},
			},
		}},
	}
	res := ValidateTemplate(spec, storeKindLookup(knownStores))
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}

func TestValidateLocks_Error_UnknownMode(t *testing.T) {
	spec := &TemplateSpec{
		Name:            "demo",
		Version:         "1.0.0",
		FrameResolution: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "h",
			Locks: []NodeLockRef{
				{Name: "weird", Mode: store.LockMode("rwlock")},
			},
		}},
	}
	res := ValidateTemplate(spec, storeKindLookup(knownStores))
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].locks[0].mode")
}

func TestValidateLocks_Error_MissingMode(t *testing.T) {
	spec := &TemplateSpec{
		Name:            "demo",
		Version:         "1.0.0",
		FrameResolution: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "h",
			Locks: []NodeLockRef{
				{Name: "weird"},
			},
		}},
	}
	res := ValidateTemplate(spec, storeKindLookup(knownStores))
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].locks[0].mode")
}

func TestValidateLocks_Error_CountingNeedsLimit(t *testing.T) {
	spec := &TemplateSpec{
		Name:            "demo",
		Version:         "1.0.0",
		FrameResolution: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "h",
			Locks: []NodeLockRef{
				{Name: "model-budget", Mode: store.LockModeCounting, Limit: 0},
			},
		}},
	}
	res := ValidateTemplate(spec, storeKindLookup(knownStores))
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].locks[0].limit")
}

func TestValidateLocks_Error_DuplicateLockName(t *testing.T) {
	spec := &TemplateSpec{
		Name:            "demo",
		Version:         "1.0.0",
		FrameResolution: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "h",
			Locks: []NodeLockRef{
				{Name: "model-budget", Mode: store.LockModeCounting, Limit: 50},
				{Name: "model-budget", Mode: store.LockModeCounting, Limit: 100},
			},
		}},
	}
	res := ValidateTemplate(spec, storeKindLookup(knownStores))
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].locks[1].name")
}

// --------------------------------------------------------------------------
// validateAttributesSchema
// --------------------------------------------------------------------------

func TestValidateAttributes_Ok_DepsAndClaimAndParams(t *testing.T) {
	spec := &TemplateSpec{
		Name:            "demo",
		Version:         "1.0.0",
		FrameResolution: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{
			{
				Type: "claim-topic",
				Stores: []NodeStoreRef{
					{Name: "topics-ring", Claim: true, Hold: true},
				},
				Attributes: NodeAttributesDef{
					Schema: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"area":     map[string]any{"type": "string", "source": "{{claim.topics-ring.payload.area}}"},
							"subtopic": map[string]any{"type": "string", "source": "{{claim.topics-ring.payload.subtopic}}"},
							"region":   map[string]any{"type": "string", "source": "{{params.region}}"},
						},
						"required": []any{"area", "subtopic"},
					},
				},
			},
			{
				Type:         "scope",
				Executor:     "h",
				Dependencies: []string{"claim-topic"},
				ClaimResolutions: []ClaimResolutionRef{
					{Source: "claim-topic", Store: "topics-ring"},
				},
				Attributes: NodeAttributesDef{
					Schema: map[string]any{
						"properties": map[string]any{
							"area": map[string]any{"type": "string", "source": "{{deps.claim-topic.area}}"},
						},
					},
				},
			},
		},
	}
	res := ValidateTemplate(spec, storeKindLookup(knownStores))
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}

func TestValidateAttributes_Error_UnparseableSchema(t *testing.T) {
	// `properties` must be an object per JSON Schema; passing a non-object
	// trips the compile step.
	spec := &TemplateSpec{
		Name:            "demo",
		Version:         "1.0.0",
		FrameResolution: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "h",
			Attributes: NodeAttributesDef{
				Schema: map[string]any{
					"type":       "object",
					"properties": "not an object",
				},
			},
		}},
	}
	res := ValidateTemplate(spec, storeKindLookup(knownStores))
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].attributes.schema")
}

func TestValidateAttributes_Error_DepsToUnknownNode(t *testing.T) {
	spec := &TemplateSpec{
		Name:            "demo",
		Version:         "1.0.0",
		FrameResolution: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "h",
			Attributes: NodeAttributesDef{
				Schema: map[string]any{
					"properties": map[string]any{
						"x": map[string]any{"type": "string", "source": "{{deps.ghost.field}}"},
					},
				},
			},
		}},
	}
	res := ValidateTemplate(spec, storeKindLookup(knownStores))
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].attributes.schema.properties.x.source")
}

func TestValidateAttributes_Error_ClaimToUndeclaredStore(t *testing.T) {
	spec := &TemplateSpec{
		Name:            "demo",
		Version:         "1.0.0",
		FrameResolution: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "h",
			// no Stores entry for topics-ring on this node
			Attributes: NodeAttributesDef{
				Schema: map[string]any{
					"properties": map[string]any{
						"area": map[string]any{"type": "string", "source": "{{claim.topics-ring.payload.area}}"},
					},
				},
			},
		}},
	}
	res := ValidateTemplate(spec, storeKindLookup(knownStores))
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].attributes.schema.properties.area.source")
}

func TestValidateAttributes_Error_ClaimMissingPayloadSegment(t *testing.T) {
	spec := &TemplateSpec{
		Name:            "demo",
		Version:         "1.0.0",
		FrameResolution: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{{
			Type: "a",
			Stores: []NodeStoreRef{
				{Name: "topics-ring", Claim: true, Hold: true},
			},
			ClaimResolutions: []ClaimResolutionRef{
				{Source: "a", Store: "topics-ring"},
			},
			Attributes: NodeAttributesDef{
				Schema: map[string]any{
					"properties": map[string]any{
						"area": map[string]any{"type": "string", "source": "{{claim.topics-ring.area}}"},
					},
				},
			},
		}},
	}
	res := ValidateTemplate(spec, storeKindLookup(knownStores))
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].attributes.schema.properties.area.source")
}

func TestValidateAttributes_Error_UnknownDirectiveKind(t *testing.T) {
	spec := &TemplateSpec{
		Name:            "demo",
		Version:         "1.0.0",
		FrameResolution: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "h",
			Attributes: NodeAttributesDef{
				Schema: map[string]any{
					"properties": map[string]any{
						"x": map[string]any{"type": "string", "source": "{{userdata.foo}}"},
					},
				},
			},
		}},
	}
	res := ValidateTemplate(spec, storeKindLookup(knownStores))
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].attributes.schema.properties.x.source")
}

func TestValidateAttributes_Error_SourceNotJustOneDirective(t *testing.T) {
	spec := &TemplateSpec{
		Name:            "demo",
		Version:         "1.0.0",
		FrameResolution: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{
			{Type: "up", Executor: "h"},
			{
				Type:         "a",
				Executor:     "h",
				Dependencies: []string{"up"},
				Attributes: NodeAttributesDef{
					Schema: map[string]any{
						"properties": map[string]any{
							"x": map[string]any{"type": "string", "source": "prefix-{{deps.up.field}}"},
						},
					},
				},
			},
		},
	}
	res := ValidateTemplate(spec, storeKindLookup(knownStores))
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[1].attributes.schema.properties.x.source")
}

// --------------------------------------------------------------------------
// validateClaimResolutions — §11.4 DAG walk
// --------------------------------------------------------------------------

// Linear chain: claim-topic → scope → review. review is the only terminal,
// so review must carry the resolution.
func TestValidateClaimResolutions_Ok_LinearChain(t *testing.T) {
	spec := &TemplateSpec{
		Name:            "demo",
		Version:         "1.0.0",
		FrameResolution: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{
			{
				Type: "claim-topic",
				Stores: []NodeStoreRef{
					{Name: "topics-ring", Claim: true, Hold: true},
				},
			},
			{
				Type:         "scope",
				Executor:     "h",
				Dependencies: []string{"claim-topic"},
			},
			{
				Type:         "review",
				Executor:     "h",
				Dependencies: []string{"scope"},
				ClaimResolutions: []ClaimResolutionRef{
					{Source: "claim-topic", Store: "topics-ring"},
				},
			},
		},
	}
	res := ValidateTemplate(spec, storeKindLookup(knownStores))
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}

// Fan-out: claim-topic → {leaf-a, leaf-b}. Both leaves must resolve.
func TestValidateClaimResolutions_Ok_FanOut(t *testing.T) {
	spec := &TemplateSpec{
		Name:            "demo",
		Version:         "1.0.0",
		FrameResolution: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{
			{
				Type: "claim-topic",
				Stores: []NodeStoreRef{
					{Name: "topics-ring", Claim: true, Hold: true},
				},
			},
			{
				Type:         "leaf-a",
				Executor:     "h",
				Dependencies: []string{"claim-topic"},
				ClaimResolutions: []ClaimResolutionRef{
					{Source: "claim-topic", Store: "topics-ring"},
				},
			},
			{
				Type:         "leaf-b",
				Executor:     "h",
				Dependencies: []string{"claim-topic"},
				ClaimResolutions: []ClaimResolutionRef{
					{Source: "claim-topic", Store: "topics-ring"},
				},
			},
		},
	}
	res := ValidateTemplate(spec, storeKindLookup(knownStores))
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}

// Fan-out where only one leaf resolves: the unresolved leaf must be flagged.
func TestValidateClaimResolutions_Error_FanOutMissingLeaf(t *testing.T) {
	spec := &TemplateSpec{
		Name:            "demo",
		Version:         "1.0.0",
		FrameResolution: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{
			{
				Type: "claim-topic",
				Stores: []NodeStoreRef{
					{Name: "topics-ring", Claim: true, Hold: true},
				},
			},
			{
				Type:         "leaf-a",
				Executor:     "h",
				Dependencies: []string{"claim-topic"},
				ClaimResolutions: []ClaimResolutionRef{
					{Source: "claim-topic", Store: "topics-ring"},
				},
			},
			{
				Type:         "leaf-b",
				Executor:     "h",
				Dependencies: []string{"claim-topic"},
				// missing resolution
			},
		},
	}
	res := ValidateTemplate(spec, storeKindLookup(knownStores))
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].stores")
	// Confirm the message names the missing leaf so authors know where to
	// add the resolution.
	found := false
	for _, e := range res.Errors {
		if e.Path == "nodes[0].stores" {
			found = true
			assert.Contains(t, e.Msg, "leaf-b")
		}
	}
	assert.True(t, found, "expected error msg to name leaf-b; got %+v", res.Errors)
}

// Linear chain with no resolution at the terminal — the terminal must be
// flagged.
func TestValidateClaimResolutions_Error_LinearChainMissingTerminal(t *testing.T) {
	spec := &TemplateSpec{
		Name:            "demo",
		Version:         "1.0.0",
		FrameResolution: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{
			{
				Type: "claim-topic",
				Stores: []NodeStoreRef{
					{Name: "topics-ring", Claim: true, Hold: true},
				},
			},
			{
				Type:         "scope",
				Executor:     "h",
				Dependencies: []string{"claim-topic"},
			},
			{
				Type:         "review",
				Executor:     "h",
				Dependencies: []string{"scope"},
				// missing resolution
			},
		},
	}
	res := ValidateTemplate(spec, storeKindLookup(knownStores))
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].stores")
	found := false
	for _, e := range res.Errors {
		if e.Path == "nodes[0].stores" {
			found = true
			assert.Contains(t, e.Msg, "review")
		}
	}
	assert.True(t, found, "expected error msg to name review; got %+v", res.Errors)
}

// Hold:true on a node with no descendants — the source itself is the leaf
// and must self-resolve.
func TestValidateClaimResolutions_Ok_SourceSelfResolves(t *testing.T) {
	spec := &TemplateSpec{
		Name:            "demo",
		Version:         "1.0.0",
		FrameResolution: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{{
			Type: "claim-topic",
			Stores: []NodeStoreRef{
				{Name: "topics-ring", Claim: true, Hold: true},
			},
			ClaimResolutions: []ClaimResolutionRef{
				{Source: "claim-topic", Store: "topics-ring"},
			},
		}},
	}
	res := ValidateTemplate(spec, storeKindLookup(knownStores))
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}

// Hold:false (claim-and-forget) does not require resolutions at terminals.
func TestValidateClaimResolutions_Ok_ForgetClaimNeedsNoResolution(t *testing.T) {
	spec := &TemplateSpec{
		Name:            "demo",
		Version:         "1.0.0",
		FrameResolution: FrameResolutionSerialQueue,
		Nodes: []TemplateNodeDef{
			{
				Type: "claim-topic",
				Stores: []NodeStoreRef{
					{Name: "topics-ring", Claim: true, Hold: false},
				},
			},
			{
				Type:         "leaf",
				Executor:     "h",
				Dependencies: []string{"claim-topic"},
			},
		},
	}
	res := ValidateTemplate(spec, storeKindLookup(knownStores))
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}

// findHoldingTerminals — direct-test the helper (algorithm-level test).
func TestFindHoldingTerminals(t *testing.T) {
	spec := &TemplateSpec{
		Nodes: []TemplateNodeDef{
			{Type: "claim-topic"},
			{Type: "scope", Dependencies: []string{"claim-topic"}},
			{Type: "draft", Dependencies: []string{"claim-topic", "scope"}},
			{Type: "review", Dependencies: []string{"claim-topic", "scope", "draft"}},
		},
	}
	leaves := findHoldingTerminals(spec, "claim-topic", "topics-ring")
	assert.Equal(t, []string{"review"}, leaves)
}

func TestFindHoldingTerminals_FanOut(t *testing.T) {
	spec := &TemplateSpec{
		Nodes: []TemplateNodeDef{
			{Type: "src"},
			{Type: "a", Dependencies: []string{"src"}},
			{Type: "b", Dependencies: []string{"src"}},
			{Type: "c", Dependencies: []string{"src"}},
		},
	}
	leaves := findHoldingTerminals(spec, "src", "any")
	// Sorted alphabetically by sortStrings.
	assert.Equal(t, []string{"a", "b", "c"}, leaves)
}

// --------------------------------------------------------------------------
// validateFrameResolution — per docs/specs/2026-04-26-frame-resolution-design.md
// --------------------------------------------------------------------------

func TestValidateTemplate_FrameResolution_Missing(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		// FrameResolution intentionally empty.
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "handler.a",
		}},
	}
	res := ValidateTemplate(spec, storeKindLookup(knownStores))
	require.False(t, res.Ok())
	hasErrorAt(t, res, "frame_resolution")
}

func TestValidateTemplate_FrameResolution_InvalidValue(t *testing.T) {
	spec := &TemplateSpec{
		Name:            "demo",
		Version:         "1.0.0",
		FrameResolution: "abort",
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "handler.a",
		}},
	}
	res := ValidateTemplate(spec, storeKindLookup(knownStores))
	require.False(t, res.Ok())
	hasErrorAt(t, res, "frame_resolution")
}

func TestValidateTemplate_FrameResolution_DefaultsTimeout(t *testing.T) {
	spec := &TemplateSpec{
		Name:            "demo",
		Version:         "1.0.0",
		FrameResolution: FrameResolutionSerialQueue,
		// FrameTimeoutMs intentionally zero — accepted by validator;
		// ApplyFrameResolutionDefaults fills it in at the deploy boundary.
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "handler.a",
		}},
	}
	res := ValidateTemplate(spec, storeKindLookup(knownStores))
	require.True(t, res.Ok(), "errors: %+v", res.Errors)
	// Validator must not mutate.
	assert.Equal(t, int64(0), spec.FrameTimeoutMs,
		"validator must not mutate spec.FrameTimeoutMs")
	// Default-fill at the deploy boundary.
	ApplyFrameResolutionDefaults(spec)
	assert.Equal(t, FrameTimeoutDefaultMs, spec.FrameTimeoutMs)
	// Idempotent — re-validation does not re-mutate.
	res = ValidateTemplate(spec, storeKindLookup(knownStores))
	require.True(t, res.Ok())
	assert.Equal(t, FrameTimeoutDefaultMs, spec.FrameTimeoutMs)
}

func TestValidateTemplate_FrameResolution_BelowFloor(t *testing.T) {
	spec := &TemplateSpec{
		Name:            "demo",
		Version:         "1.0.0",
		FrameResolution: FrameResolutionSerialQueue,
		FrameTimeoutMs:  30000, // below 60000 floor
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "handler.a",
		}},
	}
	res := ValidateTemplate(spec, storeKindLookup(knownStores))
	require.False(t, res.Ok())
	hasErrorAt(t, res, "frame_timeout_ms")
}

func TestFindHoldingTerminals_NoDescendants(t *testing.T) {
	spec := &TemplateSpec{
		Nodes: []TemplateNodeDef{
			{Type: "lonely"},
			{Type: "unrelated"},
		},
	}
	leaves := findHoldingTerminals(spec, "lonely", "any")
	assert.Equal(t, []string{"lonely"}, leaves)
}
