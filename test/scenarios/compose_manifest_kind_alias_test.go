// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package scenarios

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli"
	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli/compose"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

// @story: compose-lifecycle
// @decision: template-identity-deployment-canonical
// @concept: template
func TestComposeManifestNamingANodeByKindAliasAppliesAndReconciles(t *testing.T) {
	h := scenario.Start(t, scenario.HarnessOpts{})
	ctx := context.Background()

	dir := t.TempDir()
	templatePath := filepath.Join(dir, "template.yml")
	templateBody := `name: kind-alias-compose
version: "1"
nodes:
  - type: passthrough
    kind: attribute_passthrough
`
	require.NoError(t, os.WriteFile(templatePath, []byte(templateBody), 0o600))

	manifest := &compose.Manifest{
		Project:   "kind-alias",
		Templates: []compose.TemplateRef{{Path: templatePath, Tag: "main", State: "deployed"}},
		Instances: []compose.InstanceRef{{Template: "main", Name: "one"}},
	}
	c := cli.NewClient(h.ControlBase)

	state, err := compose.QueryState(ctx, c, manifest.Project)
	require.NoError(t, err)
	plan, err := compose.ComputePlan(ctx, c, manifest, state)
	require.NoError(t, err)
	require.NotEmpty(t, plan.Steps, "the first plan over an empty deployment has work to do")

	_, applied, err := compose.ApplyPlan(ctx, c, plan, compose.ApplyOpts{Logger: io.Discard})
	require.NoError(t, err)
	require.Equal(t, len(plan.Steps), applied)

	state, err = compose.QueryState(ctx, c, manifest.Project)
	require.NoError(t, err)
	replan, err := compose.ComputePlan(ctx, c, manifest, state)
	require.NoError(t, err)
	require.Empty(t, replan.Steps,
		"the deployment owns the canonical hash, so a manifest naming a node by kind alias "+
			"reconciles: the second plan finds the template it just registered under the same identity")
}
