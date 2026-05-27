// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// Tests for the `source_file:` resolution pass in readSpecFile /
// resolveSourceFileRefs. Per spec
// .ok-planner/specs/2026-05-19-multi-instance-template-ergonomics-design.md
// Item 2.

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rimsky-ai/rimsky-core/graph/node"
)

func TestResolveSourceFileRefs_SimpleInline(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "prompt.md"), []byte("you are an agent"), 0o644); err != nil {
		t.Fatal(err)
	}
	tree := map[string]any{
		"system_prompt": map[string]any{"source_file": "prompt.md"},
	}
	got, err := resolveSourceFileRefs(tree, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	gotMap, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", got)
	}
	if gotMap["system_prompt"] != "you are an agent" {
		t.Fatalf("got %v want %q", gotMap["system_prompt"], "you are an agent")
	}
}

func TestResolveSourceFileRefs_NestedUnderAttributesDefault(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "discover.md"), []byte("discover prompt"), 0o644); err != nil {
		t.Fatal(err)
	}
	tree := map[string]any{
		"nodes": []any{
			map[string]any{
				"type":     "discover",
				"executor": "claude-agent",
				"attributes": map[string]any{
					"schema": map[string]any{
						"properties": map[string]any{
							"system_prompt": map[string]any{
								"type":    "string",
								"default": map[string]any{"source_file": "discover.md"},
							},
						},
					},
				},
			},
		},
	}
	got, err := resolveSourceFileRefs(tree, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resolved := got.(map[string]any)["nodes"].([]any)[0].(map[string]any)["attributes"].(map[string]any)["schema"].(map[string]any)["properties"].(map[string]any)["system_prompt"].(map[string]any)["default"]
	if resolved != "discover prompt" {
		t.Fatalf("got %v want %q", resolved, "discover prompt")
	}
}

func TestResolveSourceFileRefs_NestedUnderAttributeSchema(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "desc.md"), []byte("schema description"), 0o644); err != nil {
		t.Fatal(err)
	}
	tree := map[string]any{
		"nodes": []any{
			map[string]any{
				"attributes": map[string]any{
					"schema": map[string]any{
						"description": map[string]any{"source_file": "desc.md"},
					},
				},
			},
		},
	}
	got, err := resolveSourceFileRefs(tree, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resolved := got.(map[string]any)["nodes"].([]any)[0].(map[string]any)["attributes"].(map[string]any)["schema"].(map[string]any)["description"]
	if resolved != "schema description" {
		t.Fatalf("got %v want %q", resolved, "schema description")
	}
}

func TestResolveSourceFileRefs_MultipleFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte("alpha"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.md"), []byte("beta"), 0o644); err != nil {
		t.Fatal(err)
	}
	tree := map[string]any{
		"a": map[string]any{"source_file": "a.md"},
		"b": map[string]any{"source_file": "b.md"},
	}
	got, err := resolveSourceFileRefs(tree, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := got.(map[string]any)
	if m["a"] != "alpha" || m["b"] != "beta" {
		t.Fatalf("got %v want a=alpha,b=beta", m)
	}
}

func TestResolveSourceFileRefs_MissingFile(t *testing.T) {
	dir := t.TempDir()
	tree := map[string]any{"x": map[string]any{"source_file": "missing.md"}}
	_, err := resolveSourceFileRefs(tree, dir)
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !strings.Contains(err.Error(), "missing.md") {
		t.Fatalf("error should name the input path: %v", err)
	}
}

func TestResolveSourceFileRefs_PathEscape(t *testing.T) {
	dir := t.TempDir()
	tree := map[string]any{"x": map[string]any{"source_file": "../../etc/passwd"}}
	_, err := resolveSourceFileRefs(tree, dir)
	if err == nil {
		t.Fatal("expected error for path escape")
	}
	if !strings.Contains(err.Error(), "escape") && !strings.Contains(err.Error(), "..") {
		t.Fatalf("error should call out the escape: %v", err)
	}
}

func TestResolveSourceFileRefs_AbsolutePath(t *testing.T) {
	dir := t.TempDir()
	tree := map[string]any{"x": map[string]any{"source_file": "/etc/passwd"}}
	_, err := resolveSourceFileRefs(tree, dir)
	if err == nil {
		t.Fatal("expected error for absolute path")
	}
	if !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("error should call out absolute path: %v", err)
	}
}

func TestResolveSourceFileRefs_NoRef(t *testing.T) {
	dir := t.TempDir()
	tree := map[string]any{
		"name":     "demo",
		"version":  "1.0",
		"executor": "claude-agent",
	}
	got, err := resolveSourceFileRefs(tree, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := got.(map[string]any)
	if m["name"] != "demo" || m["version"] != "1.0" || m["executor"] != "claude-agent" {
		t.Fatalf("tree mutated unexpectedly: %v", m)
	}
}

func TestResolveSourceFileRefs_WithSiblings_NotRecognized(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte("alpha"), 0o644); err != nil {
		t.Fatal(err)
	}
	tree := map[string]any{
		"x": map[string]any{"source_file": "a.md", "foo": "bar"},
	}
	got, err := resolveSourceFileRefs(tree, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := got.(map[string]any)["x"].(map[string]any)
	// Not recognized as a ref — left intact (siblings present).
	if m["source_file"] != "a.md" || m["foo"] != "bar" {
		t.Fatalf("object with siblings should be left intact: %v", m)
	}
}

func TestResolveSourceFileRefs_NonStringValue_NotRecognized(t *testing.T) {
	dir := t.TempDir()
	tree := map[string]any{
		"x": map[string]any{"source_file": 42},
	}
	got, err := resolveSourceFileRefs(tree, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := got.(map[string]any)["x"].(map[string]any)
	if m["source_file"] != 42 {
		t.Fatalf("non-string source_file value should be left intact: %v", m)
	}
}

// TestReadSpecFile_HashStability_IdenticalContent confirms that two
// templates with different `source_file:` paths whose targets carry
// identical content resolve to specs that hash identically.
// (The hash is computed over the resolved bytes per spec §Item 2 "Hash
// semantics".)
func TestReadSpecFile_HashStability_IdenticalContent(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()
	identicalContent := []byte("you are an agent")
	if err := os.WriteFile(filepath.Join(dirA, "system_a.md"), identicalContent, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirB, "system_b.md"), identicalContent, 0o644); err != nil {
		t.Fatal(err)
	}
	specA := filepath.Join(dirA, "spec.yml")
	specB := filepath.Join(dirB, "spec.yml")
	yamlA := `name: demo
version: "1.0"
frame_resolution_mode: coalesce
nodes:
  - type: a
    executor: claude-agent
    attributes:
      schema:
        properties:
          system_prompt:
            type: string
            default:
              source_file: system_a.md
`
	yamlB := `name: demo
version: "1.0"
frame_resolution_mode: coalesce
nodes:
  - type: a
    executor: claude-agent
    attributes:
      schema:
        properties:
          system_prompt:
            type: string
            default:
              source_file: system_b.md
`
	if err := os.WriteFile(specA, []byte(yamlA), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(specB, []byte(yamlB), 0o644); err != nil {
		t.Fatal(err)
	}
	parsedA, err := readSpecFile(specA)
	if err != nil {
		t.Fatalf("readSpecFile A: %v", err)
	}
	parsedB, err := readSpecFile(specB)
	if err != nil {
		t.Fatalf("readSpecFile B: %v", err)
	}
	defA, _ := propDefault(parsedA.Nodes[0].Attributes, "system_prompt")
	defB, _ := propDefault(parsedB.Nodes[0].Attributes, "system_prompt")
	if defA != defB {
		t.Fatalf("resolved content differs: %v vs %v", defA, defB)
	}
}

// TestReadSpecFile_HashStability_DifferentContent_ChangesHash confirms
// that re-registering after editing a referenced file produces a
// distinguishable spec (which the canonical-hash predicate will hash
// differently).
func TestReadSpecFile_HashStability_DifferentContent_ChangesHash(t *testing.T) {
	dir := t.TempDir()
	promptPath := filepath.Join(dir, "system.md")
	specPath := filepath.Join(dir, "spec.yml")
	yaml := `name: demo
version: "1.0"
frame_resolution_mode: coalesce
nodes:
  - type: a
    executor: claude-agent
    attributes:
      schema:
        properties:
          system_prompt:
            type: string
            default:
              source_file: system.md
`
	if err := os.WriteFile(specPath, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(promptPath, []byte("you are an agent v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	specV1, err := readSpecFile(specPath)
	if err != nil {
		t.Fatalf("readSpecFile v1: %v", err)
	}
	if err := os.WriteFile(promptPath, []byte("you are an agent v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	specV2, err := readSpecFile(specPath)
	if err != nil {
		t.Fatalf("readSpecFile v2: %v", err)
	}
	defV1, _ := propDefault(specV1.Nodes[0].Attributes, "system_prompt")
	defV2, _ := propDefault(specV2.Nodes[0].Attributes, "system_prompt")
	if defV1 == defV2 {
		t.Fatalf("expected resolved content to differ across edits, got %v", defV1)
	}
}

// End-to-end via readSpecFile: write a small template YAML that
// references a sibling prompt file; confirm the decoded TemplateSpec
// carries the resolved content (not the reference).
func TestReadSpecFile_SourceFileResolved(t *testing.T) {
	dir := t.TempDir()
	promptPath := filepath.Join(dir, "system.md")
	if err := os.WriteFile(promptPath, []byte("you are an agent"), 0o644); err != nil {
		t.Fatal(err)
	}
	specPath := filepath.Join(dir, "spec.yml")
	yaml := `name: demo
version: "1.0"
frame_resolution_mode: coalesce
nodes:
  - type: a
    executor: claude-agent
    attributes:
      schema:
        properties:
          system_prompt:
            type: string
            default:
              source_file: system.md
`
	if err := os.WriteFile(specPath, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	spec, err := readSpecFile(specPath)
	if err != nil {
		t.Fatalf("readSpecFile: %v", err)
	}
	if len(spec.Nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(spec.Nodes))
	}
	def, ok := propDefault(spec.Nodes[0].Attributes, "system_prompt")
	if !ok {
		t.Fatalf("expected attributes.schema.properties.system_prompt.default to be set")
	}
	if def != "you are an agent" {
		t.Fatalf("expected resolved content, got %v", def)
	}
}

// propDefault is a test helper: returns the `default:` value for the
// named property on a NodeAttributesDef. Returns (nil, false) on any
// missing intermediate.
func propDefault(def *node.NodeAttributesDef, name string) (any, bool) {
	if def == nil || def.Schema == nil {
		return nil, false
	}
	props, ok := def.Schema["properties"].(map[string]any)
	if !ok {
		return nil, false
	}
	prop, ok := props[name].(map[string]any)
	if !ok {
		return nil, false
	}
	v, ok := prop["default"]
	return v, ok
}
