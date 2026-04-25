package node

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// knownImpls is the default resource-implementation lookup used by most
// tests. Keeping it centralized makes it easy to swap in empty/alternate sets
// where we want to exercise the "unknown implementation" path.
var knownImpls = map[string]bool{
	"inline-jsonb": true,
	"external-sql": true,
}

func implLookup(known map[string]bool) func(string) bool {
	return func(name string) bool { return known[name] }
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

func TestValidateTemplate_Ok_MinimalExecutorNode(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "handler.a",
			OwnsResources: []ResourceDef{{
				Path:           []string{"a", "{instance_id}"},
				Implementation: "inline-jsonb",
			}},
		}},
	}
	res := ValidateTemplate(spec, implLookup(knownImpls))
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
	assert.Empty(t, res.Warnings)
}

func TestValidateTemplate_Ok_PureCascadeNode(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{
			{
				Type:     "leaf",
				Executor: "handler.leaf",
				OwnsResources: []ResourceDef{{
					Path:           []string{"leaf"},
					Implementation: "inline-jsonb",
				}},
			},
			{
				Type:         "fanout",
				Dependencies: []string{"leaf"},
			},
		},
	}
	res := ValidateTemplate(spec, implLookup(knownImpls))
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}

func TestValidateTemplate_Error_DependencyToUnknownNode(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{{
			Type:         "a",
			Executor:     "handler.a",
			Dependencies: []string{"ghost"},
		}},
	}
	res := ValidateTemplate(spec, implLookup(knownImpls))
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].dependencies[0]")
}

func TestValidateTemplate_Error_DependencyCycle(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{
			{Type: "a", Executor: "h", Dependencies: []string{"b"}},
			{Type: "b", Executor: "h", Dependencies: []string{"c"}},
			{Type: "c", Executor: "h", Dependencies: []string{"a"}},
		},
	}
	res := ValidateTemplate(spec, implLookup(knownImpls))
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
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{{
			Type:     "timer",
			Executor: "h",
			Schedule: "not a cron expression",
		}},
	}
	res := ValidateTemplate(spec, implLookup(knownImpls))
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].schedule")
}

func TestValidateTemplate_Ok_ValidScheduleCron(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{{
			Type:     "timer",
			Executor: "h",
			Schedule: "*/5 * * * *",
		}},
	}
	res := ValidateTemplate(spec, implLookup(knownImpls))
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}

func TestValidateTemplate_Error_PureCascadeOwnsResources(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{{
			Type: "fanout",
			OwnsResources: []ResourceDef{{
				Path:           []string{"x"},
				Implementation: "inline-jsonb",
			}},
		}},
	}
	res := ValidateTemplate(spec, implLookup(knownImpls))
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].owns_resources")
}

func TestValidateTemplate_Warning_PureCascadeHasUserdata(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{{
			Type:     "fanout",
			Userdata: map[string]any{"k": "v"},
		}},
	}
	res := ValidateTemplate(spec, implLookup(knownImpls))
	assert.True(t, res.Ok(), "unexpected errors: %+v", res.Errors)
	hasWarningAt(t, res, "nodes[0].userdata")
}

func TestValidateTemplate_Error_UnknownResourceImpl(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "h",
			OwnsResources: []ResourceDef{{
				Path:           []string{"a"},
				Implementation: "mystery-backend",
			}},
		}},
	}
	res := ValidateTemplate(spec, implLookup(knownImpls))
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].owns_resources[0].implementation")
}

func TestValidateTemplate_Error_InvalidPlaceholderInPath(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "h",
			OwnsResources: []ResourceDef{{
				Path:           []string{"a", "{not_a_real_placeholder}"},
				Implementation: "inline-jsonb",
			}},
		}},
	}
	res := ValidateTemplate(spec, implLookup(knownImpls))
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].owns_resources[0].path[1]")
}

func TestValidateTemplate_Error_InvalidPlaceholderInConfig(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "h",
			OwnsResources: []ResourceDef{{
				Path:           []string{"a"},
				Implementation: "inline-jsonb",
				Config: map[string]any{
					"table": "t_{params.tenant_id}_{bad}",
				},
			}},
		}},
	}
	res := ValidateTemplate(spec, implLookup(knownImpls))
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].owns_resources[0].config.table")
}

func TestValidateTemplate_Error_InvalidTargetsInErrorType(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
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
	res := ValidateTemplate(spec, implLookup(knownImpls))
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].error_types[network].policy[0].targets[0]")
}

func TestValidateTemplate_Ok_ConcurrencyTagsPlaceholders(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "h",
			ConcurrencyTags: []string{
				"tenant:{consumer_key}",
				"instance:{instance_id}",
				"custom:{params.region}",
			},
		}},
	}
	res := ValidateTemplate(spec, implLookup(knownImpls))
	assert.True(t, res.Ok(), "errors: %+v", res.Errors)
}

func TestValidateTemplate_Error_InvalidPlaceholderInConcurrencyTag(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "demo",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{{
			Type:            "a",
			Executor:        "h",
			ConcurrencyTags: []string{"tenant:{oops}"},
		}},
	}
	res := ValidateTemplate(spec, implLookup(knownImpls))
	require.False(t, res.Ok())
	hasErrorAt(t, res, "nodes[0].concurrency_tags[0]")
}

func TestValidateTemplate_Error_EmptyName(t *testing.T) {
	spec := &TemplateSpec{
		Name:    "",
		Version: "1.0.0",
		Nodes: []TemplateNodeDef{{
			Type:     "a",
			Executor: "h",
		}},
	}
	res := ValidateTemplate(spec, implLookup(knownImpls))
	require.False(t, res.Ok())
	hasErrorAt(t, res, "name")
}
