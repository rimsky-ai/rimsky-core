// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// End-to-end proof that `rimsky compose up` / `rimsky compose down` actually
// back the reserved `compose:` prefix against the REAL assembled product.
//
// S-cli-onboarding-compose-up-down: an operator declares a project's
// templates and instances in a rimsky-compose.yml manifest and reconciles
// them into an ALREADY-RUNNING rimsky with `compose up` (register + deploy +
// instantiate every declared member under the `compose:<project>:` namespace),
// then tears them down with `compose down`, leaving the stack clean.
//
// Compose is purely application-layer: it operates against a running rimsky
// and NEVER starts it, invokes no docker/terraform/kubectl, and materializes
// no rimsky config. That no-infra-side-effect property is structurally
// guaranteed here: the test drives the real `compose.RunComposeUp` /
// `compose.RunComposeDown` entrypoints, which only ever speak to the live
// control-api over the bundled cli.Client — there is no infra surface in the
// call path to invoke.
//
// The value-delivering components are all real: the control-api, scheduler,
// and supervisor run inside the rimsky-all-in-one image (on its baked SQLite
// default), the in-tree stub executor stands in for "whatever executor your
// deployment provides" (the manifest's nodes use `executor: stub`), and the
// reconcile is the real compose engine — no member operation is stubbed.
//
// The manifest declares TWO templates (one single `executor: stub` node each)
// plus one instance per template, project `project-alpha`. After `compose up`
// the test asserts, via the same cli.Client the engine itself uses, that each
// declared template is registered+deployed and that one instance per member
// exists, each carrying a `compose:project-alpha:`-prefixed tag / instance_key.
//
// Project-scoping is proven against a real foreign artifact: BEFORE `compose
// down`, the test creates a manual, NON-compose tag bound to an unrelated
// manually-registered template. `compose down` is project-scoped — it touches
// only `compose:project-alpha:`-prefixed resources belonging to this
// manifest's project — so the manual tag and its template MUST survive the
// teardown. After `compose down` the test asserts the compose instances and
// templates are gone and the manual artifact remains.
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

// composeProject is the manifest's project; every compose-owned tag and
// instance_key is namespaced under `compose:<project>:` by the engine.
const composeProject = "project-alpha"

// composePrefix is the reserved namespace prefix the engine stamps on every
// resource it manages for this project. The down reconcile is scoped to it,
// so a tag/instance_key NOT carrying this prefix is invisible to compose.
const composePrefix = "compose:" + composeProject + ":"

// TestCLICompose_UpThenDown drives `rimsky compose up` then `rimsky compose
// down` against a live all-in-one stack and proves the full reconcile cycle:
// up registers+deploys both declared templates and instantiates one instance
// per member under the `compose:project-alpha:` namespace; a manual
// non-compose tag created in between survives (project-scoped reconcile);
// down removes the compose instances + templates and leaves the stack clean.
func TestCLICompose_UpThenDown(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// The stub executor must be reachable on the shared network before
	// rimsky/all starts — the control-api fires a Capabilities handshake
	// against declared executors at startup. Network first, then the
	// executor peer, then rimsky on the baked SQLite default.
	netName := harness.NewNetwork(ctx, t)
	harness.StartExecutorStubOnNetwork(ctx, t, netName, "executor-stub")

	ep := harness.BringUpRimsky(ctx, t,
		harness.WithSQLite(),
		harness.WithExistingNetwork(netName),
		harness.WithExecutor("stub", "executor-stub:9300"),
	)

	manifestPath := writeComposeManifest(t)

	// `compose plan` BEFORE up: the manifest has not been applied yet, so
	// every member is a pending change. Spec'd exit code is 3 (mirrors
	// `terraform plan -detailed-exitcode`). Confirms the verb performs a
	// real plan/diff against live state — a stubbed verb that returned 0
	// would fail here.
	if code := compose.RunComposePlan(ctx, []string{"-f", manifestPath, "--endpoint", ep.BaseURL}); code != 3 {
		t.Fatalf("rimsky compose plan (pre-up) exited %d (want 3 for pending changes)", code)
	}

	// `compose up` reconciles the manifest into the already-running stack.
	// --yes confirms the (here empty) destructive set non-interactively; the
	// engine builds its own compose-origin cli.Client from --endpoint.
	if code := compose.RunComposeUp(ctx, []string{"-f", manifestPath, "--endpoint", ep.BaseURL, "--yes"}); code != 0 {
		t.Fatalf("rimsky compose up exited %d (want 0)", code)
	}

	// `compose plan` AFTER up: state matches manifest, so the verb must
	// report zero changes and exit 0. A verb that always exits 3 (cached
	// or stubbed) would fail this assertion — it must perform a fresh
	// diff against the live state the previous `compose up` wrote.
	if code := compose.RunComposePlan(ctx, []string{"-f", manifestPath, "--endpoint", ep.BaseURL}); code != 0 {
		t.Fatalf("rimsky compose plan (post-up) exited %d (want 0 for no changes)", code)
	}

	// `compose status` AFTER up reports manifest-vs-state annotations.
	// Capture stdout via the package-shared captureRun (which holds
	// stdoutCaptureMu so parallel CLI captures don't race) to assert the
	// verb actually inspects live state — every manifest tag and instance
	// must surface annotated `in-manifest`, proving the verb queried state
	// (not just printed the manifest).
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

	// A client identical to the engine's, for read-back assertions against the
	// live control-api. Bare BaseURL — the control-api serves bare paths.
	c := cli.NewClient(ep.BaseURL)

	// Each declared template (tpl-a@1, tpl-b@1) must be registered AND
	// deployed, reachable by its project-prefixed compose tag.
	assertComposeTemplateDeployed(t, ctx, c, "tpl-a@1")
	assertComposeTemplateDeployed(t, ctx, c, "tpl-b@1")

	// One instance per declared member must exist, each carrying a
	// `compose:project-alpha:`-prefixed instance_key.
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

	// Create a manual, NON-compose tag bound to an unrelated manually
	// registered+deployed template. Project-scoped `compose down` must leave
	// this foreign artifact untouched. The tag carries no `compose:` prefix,
	// so a plain (non-compose-origin) client write is admitted by the
	// server-side reserved-prefix guard.
	manualHash := registerAndDeployManualTemplate(t, ep)
	const manualTag = "manual-keepsake"
	if _, err := c.CreateTag(ctx, cli.CreateTagRequest{Tag: manualTag, Template: manualHash}); err != nil {
		t.Fatalf("create manual non-compose tag %q: %v", manualTag, err)
	}

	// The compose instances are durable by default (the engine does not set
	// terminate_after_run), so they never self-terminate; `compose down`
	// deliberately refuses to abort a non-terminal compose-owned instance
	// (a documented safety property). Drive the real operator sequence:
	// force-terminate the compose instances first, then `compose down`.
	for _, key := range composeInstanceKeys {
		if _, err := c.TerminateInstance(ctx, key, "compose teardown e2e"); err != nil {
			t.Fatalf("terminate compose instance %q: %v", key, err)
		}
	}
	waitForInstancesTerminal(t, ctx, c, composeInstanceKeys, 60*time.Second)

	// `compose down` removes the compose instances and undeploys/removes the
	// compose templates, leaving the stack clean.
	if code := compose.RunComposeDown(ctx, []string{"-f", manifestPath, "--endpoint", ep.BaseURL, "--yes"}); code != 0 {
		t.Fatalf("rimsky compose down exited %d (want 0)", code)
	}

	// The compose instances are gone.
	for _, key := range composeInstanceKeys {
		if _, err := c.GetInstance(ctx, key); err == nil {
			t.Fatalf("compose down left instance %q behind", key)
		} else if !cli.IsNotFound(err) {
			t.Fatalf("unexpected error reading instance %q after down: %v", key, err)
		}
	}

	// The compose tags are gone.
	for _, tagName := range []string{composePrefix + "tpl-a@1", composePrefix + "tpl-b@1"} {
		if tagExists(t, ctx, c, tagName) {
			t.Fatalf("compose down left tag %q behind", tagName)
		}
	}

	// The manual, non-compose artifact survives — proof the reconcile is
	// project-scoped and never touched a foreign resource.
	if !tagExists(t, ctx, c, manualTag) {
		t.Fatalf("compose down removed the manual non-compose tag %q — reconcile was not project-scoped", manualTag)
	}
	if _, err := c.GetTemplate(ctx, manualHash); err != nil {
		t.Fatalf("compose down removed the manually-registered template %s — reconcile was not project-scoped: %v", manualHash, err)
	}
}

// writeComposeManifest writes a rimsky-compose.yml plus its two referenced
// template specs into a fresh temp dir and returns the manifest path. The
// templates use `executor: stub` (the wired peer); the manifest references
// them by relative path, which the engine resolves against the manifest dir.
func writeComposeManifest(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	templateA := `# compose e2e template A — one stub-executor worker node.
name: compose-e2e-a
version: "1"
frame_resolution_mode: serial_queue
nodes:
  - type: worker
    executor: stub
`
	templateB := `# compose e2e template B — one stub-executor worker node.
name: compose-e2e-b
version: "1"
frame_resolution_mode: serial_queue
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

// assertComposeTemplateDeployed verifies the project-prefixed compose tag
// resolves to a template whose state is `deployed`. The tag → template
// resolution and the deployed-state read are both against the live
// control-api, so a half-applied compose up (registered but not deployed, or
// tag missing) fails here.
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

// registerAndDeployManualTemplate registers + deploys a distinct template via
// the raw control-api (the same map-POST path the SQLite all-in-one scenario
// uses) and returns its hash. It is intentionally disjoint from the compose
// templates so the manual tag bound to it is a clean foreign artifact for the
// project-scoping assertion.
func registerAndDeployManualTemplate(t *testing.T, ep harness.RimskyEndpoint) string {
	t.Helper()
	return deployScenarioTemplate(t, ep, map[string]any{
		"spec": map[string]any{
			"name":                  "manual-keepsake-template",
			"version":               "1",
			"frame_resolution_mode": "serial_queue",
			"nodes": []map[string]any{
				{"type": "keeper", "executor": "stub"},
			},
		},
	})
}

// tagExists reports whether the named tag is present in the control-api's tag
// list. Lists all tags (paging) and scans — `GET /tags` has no server-side
// name filter.
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

// waitForInstancesTerminal polls each instance until terminated_at is set.
// Terminate is synchronous at the control-api (it marks the row terminal in
// the request), so this resolves quickly; the poll guards against any small
// projection lag so `compose down` (which refuses non-terminal compose-owned
// instances) sees a clean terminal set.
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
