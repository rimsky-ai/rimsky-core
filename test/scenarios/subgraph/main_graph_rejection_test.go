// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package subgraph

import (
	"strings"
	"testing"

	tmplspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

func TestMainGraphWithEntryRejected(t *testing.T) {
	t.Parallel()
	tmpl := &node.TemplateSpec{
		Name:    "main-has-entry",
		Version: "1",
		Graphs: []tmplspec.GraphSpec{
			{
				Name:  tmplspec.MainGraphName,
				Entry: "root",
				Nodes: []node.TemplateNodeDef{{Type: "root"}},
			},
		},
	}
	res := node.ValidateTemplate(tmpl, node.RegistryHooks{})
	var found bool
	for _, e := range res.Errors {
		if strings.Contains(e.Msg, "subgraph_main_has_entry_or_exit") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected subgraph_main_has_entry_or_exit rejection; got: %v", res.Errors)
	}
}

func TestMainGraphWithExitRejected(t *testing.T) {
	t.Parallel()
	tmpl := &node.TemplateSpec{
		Name:    "main-has-exit",
		Version: "1",
		Graphs: []tmplspec.GraphSpec{
			{
				Name:  tmplspec.MainGraphName,
				Exit:  "root",
				Nodes: []node.TemplateNodeDef{{Type: "root"}},
			},
		},
	}
	res := node.ValidateTemplate(tmpl, node.RegistryHooks{})
	var found bool
	for _, e := range res.Errors {
		if strings.Contains(e.Msg, "subgraph_main_has_entry_or_exit") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected subgraph_main_has_entry_or_exit rejection; got: %v", res.Errors)
	}
}
