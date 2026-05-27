// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package embedded

import (
	"io/fs"
	"strings"
	"testing"
	"text/template"

	"gopkg.in/yaml.v3"

	"github.com/rimsky-ai/rimsky-core/graph/node"
)

func TestEmbeddedFiles_Present(t *testing.T) {
	wantPaths := []string{
		"deploy/docker-compose.yml",
		"deploy/store-filesystem.yml",
		"deploy/supervisor-config.yml",
		"graphs/example.yml",
		"rimsky-compose.yml.tmpl",
	}
	for _, p := range wantPaths {
		raw, err := fs.ReadFile(FS, p)
		if err != nil {
			t.Errorf("read %s: %v", p, err)
			continue
		}
		if len(raw) == 0 {
			t.Errorf("%s: empty", p)
		}
	}
}

func TestEmbedded_ComposeTemplate_Parses(t *testing.T) {
	raw, err := fs.ReadFile(FS, "rimsky-compose.yml.tmpl")
	if err != nil {
		t.Fatal(err)
	}
	tmpl, err := template.New("compose").Parse(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	if err := tmpl.Execute(&sb, map[string]string{"Project": "demo"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sb.String(), "project: demo") {
		t.Errorf("rendered: %s", sb.String())
	}
}

func TestEmbedded_ExampleGraph_ValidatesAgainstNode(t *testing.T) {
	raw, err := fs.ReadFile(FS, "graphs/example.yml")
	if err != nil {
		t.Fatal(err)
	}
	var spec node.TemplateSpec
	if err := yaml.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	// Validate against the declared executors / stores / locks shipped
	// in the embedded compose-template scaffold (rimsky-compose.yml.tmpl
	// rimsky_config.inline). Without these hooks the validator skips
	// executor-name checks, which means a misspelled executor in
	// example.yml would slip through and break a fresh `init && dev up`.
	hooks := node.RegistryHooks{
		ExecutorDeclared: func(n string) bool {
			// Mirror rimsky-compose.yml.tmpl's rimsky_config.inline
			// executors. Update both files in lockstep.
			return n == "http-node"
		},
		StoreDeclared: func(n string) bool {
			// Mirror rimsky-compose.yml.tmpl's rimsky_config.inline
			// stores.
			return n == "content"
		},
		NamedLockDeclared: func(_ string) bool {
			// rimsky-compose.yml.tmpl declares no named locks; any
			// reference would be a manifest/scaffold drift.
			return false
		},
	}
	res := node.ValidateTemplate(&spec, hooks)
	if !res.Ok() {
		t.Fatalf("validation errors: %+v", res.Errors)
	}
}
