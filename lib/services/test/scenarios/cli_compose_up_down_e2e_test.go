// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli"
	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli/compose"
	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

const composeProject = "project-alpha"

const composePrefix = "compose:" + composeProject + ":"

func TestCLICompose_UpThenDown(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	netName := harness.NewNetwork(ctx, t)
	harness.StartExecutorStubOnNetwork(ctx, t, netName, "executor-stub")

	ep := harness.BringUpRimsky(ctx, t,
		harness.WithSQLite(),
		harness.WithExistingNetwork(netName),
		harness.WithExecutor("stub", "executor-stub:9300"),
	)

	manifestPath := writeComposeManifest(t)

	if code := compose.RunComposePlan(ctx, []string{"-f", manifestPath, "--endpoint", ep.BaseURL}); code != 3 {
		t.Fatalf("rimsky compose plan (pre-up) exited %d (want 3 for pending changes)", code)
	}

	if code := compose.RunComposeUp(ctx, []string{"-f", manifestPath, "--endpoint", ep.BaseURL, "--yes"}); code != 0 {
		t.Fatalf("rimsky compose up exited %d (want 0)", code)
	}

	if code := compose.RunComposePlan(ctx, []string{"-f", manifestPath, "--endpoint", ep.BaseURL}); code != 0 {
		t.Fatalf("rimsky compose plan (post-up) exited %d (want 0 for no changes)", code)
	}

	statusOut, statusExit := captureRun(t, func() int {
		return compose.RunComposeStatus(ctx, []string{"-f", manifestPath, "--endpoint", ep.BaseURL})
	})
	if statusExit != 0 {
		t.Fatalf("rimsky compose status exited %d (want 0)\n--- output ---\n%s\n--- end ---", statusExit, statusOut)
	}
	for _, expect := range []string{
		composePrefix + "tpl-a@1",
		composePrefix + "tpl-b@1",
		composePrefix + "inst-a",
		composePrefix + "inst-b",
	} {
		if !strings.Contains(statusOut, expect) {
			t.Fatalf("compose status output missing %q\n--- output ---\n%s\n--- end ---", expect, statusOut)
		}
	}
	if !strings.Contains(statusOut, "in-manifest") {
		t.Fatalf("compose status output missing `in-manifest` annotations (verb may not have queried live state)\n--- output ---\n%s\n--- end ---", statusOut)
	}

	c := cli.NewClient(ep.BaseURL)

	assertComposeTemplateDeployed(t, ctx, c, "tpl-a@1")
	assertComposeTemplateDeployed(t, ctx, c, "tpl-b@1")

	composeInstanceKeys := []string{composePrefix + "inst-a", composePrefix + "inst-b"}
	for _, key := range composeInstanceKeys {
		inst, err := c.GetInstance(ctx, key)
		if err != nil {
			t.Fatalf("compose up did not create instance %q: %v", key, err)
		}
		if inst.InstanceKey == nil || *inst.InstanceKey != key {
			t.Fatalf("instance %q has unexpected instance_key %v", key, inst.InstanceKey)
		}
	}

	manualHash := registerAndDeployManualTemplate(t, ep)
	const manualTag = "manual-keepsake"
	if _, err := c.CreateTag(ctx, cli.CreateTagRequest{Tag: manualTag, Template: manualHash}); err != nil {
		t.Fatalf("create manual non-compose tag %q: %v", manualTag, err)
	}

	for _, key := range composeInstanceKeys {
		if _, err := c.TerminateInstance(ctx, key, "compose teardown e2e"); err != nil {
			t.Fatalf("terminate compose instance %q: %v", key, err)
		}
	}
	waitForInstancesTerminal(t, ctx, c, composeInstanceKeys, 60*time.Second)

	if code := compose.RunComposeDown(ctx, []string{"-f", manifestPath, "--endpoint", ep.BaseURL, "--yes"}); code != 0 {
		t.Fatalf("rimsky compose down exited %d (want 0)", code)
	}

	for _, key := range composeInstanceKeys {
		if _, err := c.GetInstance(ctx, key); err == nil {
			t.Fatalf("compose down left instance %q behind", key)
		} else if !cli.IsNotFound(err) {
			t.Fatalf("unexpected error reading instance %q after down: %v", key, err)
		}
	}

	for _, tagName := range []string{composePrefix + "tpl-a@1", composePrefix + "tpl-b@1"} {
		if tagExists(t, ctx, c, tagName) {
			t.Fatalf("compose down left tag %q behind", tagName)
		}
	}

	if !tagExists(t, ctx, c, manualTag) {
		t.Fatalf("compose down removed the manual non-compose tag %q — reconcile was not project-scoped", manualTag)
	}
	if _, err := c.GetTemplate(ctx, manualHash); err != nil {
		t.Fatalf("compose down removed the manually-registered template %s — reconcile was not project-scoped: %v", manualHash, err)
	}
}

func writeComposeManifest(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	templateA := `# compose e2e template A — one stub-executor worker node.
name: compose-e2e-a
version: "1"
nodes:
  - type: worker
    executor: stub
`
	templateB := `# compose e2e template B — one stub-executor worker node.
name: compose-e2e-b
version: "1"
nodes:
  - type: worker
    executor: stub
`
	manifest := `# compose e2e manifest — two templates, one instance each.
project: ` + composeProject + `
templates:
  - path: ./template-a.yml
    tag: tpl-a@1
    state: deployed
  - path: ./template-b.yml
    tag: tpl-b@1
    state: deployed
instances:
  - template: tpl-a@1
    name: inst-a
  - template: tpl-b@1
    name: inst-b
`
	writeFile(t, filepath.Join(dir, "template-a.yml"), templateA)
	writeFile(t, filepath.Join(dir, "template-b.yml"), templateB)
	manifestPath := filepath.Join(dir, "rimsky-compose.yml")
	writeFile(t, manifestPath, manifest)
	return manifestPath
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func assertComposeTemplateDeployed(t *testing.T, ctx context.Context, c *cli.Client, bareTag string) {
	t.Helper()
	tag := composePrefix + bareTag
	tpl, err := c.GetTemplate(ctx, tag)
	if err != nil {
		t.Fatalf("compose tag %q does not resolve to a registered template: %v", tag, err)
	}
	if tpl.State != "deployed" {
		t.Fatalf("compose template behind tag %q is in state %q, want deployed", tag, tpl.State)
	}
}

func registerAndDeployManualTemplate(t *testing.T, ep harness.RimskyEndpoint) string {
	t.Helper()
	return deployScenarioTemplate(t, ep, map[string]any{
		"spec": map[string]any{
			"name":    "manual-keepsake-template",
			"version": "1",
			"nodes": []map[string]any{
				{"type": "keeper", "executor": "stub"},
			},
		},
	})
}

func tagExists(t *testing.T, ctx context.Context, c *cli.Client, name string) bool {
	t.Helper()
	q := cli.ListTagsQuery{}
	for {
		page, err := c.ListTags(ctx, q)
		if err != nil {
			t.Fatalf("list tags: %v", err)
		}
		for _, tg := range page.Tags {
			if tg.Tag == name {
				return true
			}
		}
		if page.NextCursor == "" {
			return false
		}
		q.Cursor = page.NextCursor
	}
}

func waitForInstancesTerminal(t *testing.T, ctx context.Context, c *cli.Client, keys []string, deadline time.Duration) {
	t.Helper()
	end := time.Now().Add(deadline)
	for _, key := range keys {
		for {
			inst, err := c.GetInstance(ctx, key)
			if err != nil {
				t.Fatalf("read instance %q while waiting for terminal: %v", key, err)
			}
			if inst.TerminatedAt != nil && strings.TrimSpace(*inst.TerminatedAt) != "" {
				break
			}
			if !time.Now().Before(end) {
				t.Fatalf("instance %q did not reach terminal (terminated_at) within %v", key, deadline)
			}
			time.Sleep(250 * time.Millisecond)
		}
	}
}
