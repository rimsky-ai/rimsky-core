// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// End-to-end proof that the control-api SERVER itself guards the reserved
// `compose:` tag / instance_key prefix against any foreign client, while
// compose-originated writes still succeed — against the REAL assembled
// product.
//
// S-control-api-mcp-compose-prefix-server-guard: as an operator, when any
// client (not just the bundled CLI) tries to create a template tag or
// instance whose tag/instance_key uses the reserved `compose:` prefix, the
// control-api server itself rejects it, so the compose-managed namespace
// stays disjoint from manually-authored artifacts no matter which client
// made the call.
//
// Unlike a handler-altitude unit test, this drives the REAL control-api
// handler inside the running rimsky-all-in-one image over real HTTP (a raw
// POST with no CLI in the loop — the precise "any client" the story names),
// so the guard proven here is the shipped server-side reservation in
// `handleCreateTag` / `handleCreateInstance` reached through the chi router,
// the auth middleware chain, and the baked SQLite backend. The control-api,
// scheduler, and supervisor are the real value-delivering components; the
// in-tree stub executor stands in for "whatever executor your deployment
// provides" so the manifest's worker node can be claimed/dispatched, but the
// thing under test — the reserved-prefix guard — is the real, shipped
// control-api code path.
//
// The story's three observable claims are each asserted at the wire:
//
//	(1) A raw HTTP POST /tags with a `compose:`-prefixed tag and NO
//	    compose-origin marker returns 400 with a reserved-prefix diagnostic,
//	    AND leaves no trace: a subsequent GET /tags omits the tag.
//	(2) A raw HTTP POST /instances supplying a `compose:`-prefixed
//	    instance_key with no compose-origin marker returns 400 with the same
//	    diagnostic, AND the instance is not created: GET /instances/{key}
//	    returns 404.
//	(3) Driving the SAME class of writes through the real compose engine
//	    (compose.RunComposeUp, which stamps the trusted compose-origin
//	    marker) SUCCEEDS: the compose-prefixed tag resolves to a deployed
//	    template and the compose-prefixed instance exists. So the guard
//	    DISCRIMINATES compose-originated writes from foreign ones — it does
//	    not block the prefix unconditionally.
//
// If the guard ever regresses (a foreign client's `compose:` write silently
// accepted, or a legitimate compose-origin write blocked), this test fails on
// the observable HTTP status / persisted state, not a Docker error.
package scenarios

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli"
	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli/compose"
	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

// guardProject is the compose project the success leg reconciles under; the
// engine namespaces every tag / instance_key it manages as
// `compose:<project>:<...>`, so the prefix below is what the server guard
// admits ONLY for compose-origin writes.
const guardProject = "project-alpha"

// guardPrefix is the reserved namespace the compose engine stamps on the
// resources it manages for guardProject. The raw (foreign) POSTs below use
// exactly this shape and MUST be rejected; the compose-origin writes using
// the same shape MUST succeed.
const guardPrefix = "compose:" + guardProject + ":"

// TestControlAPIComposePrefixGuard_E2E proves the server-side reserved-prefix
// guard end to end against the live control-api: a foreign (no-CLI, no
// compose-origin marker) raw POST of a `compose:`-prefixed tag or
// instance_key is rejected 400 and persists nothing, while the SAME writes
// driven through the real compose engine (which stamps the compose-origin
// marker) succeed — proving the guard discriminates on origin, not on the
// bare prefix.
func TestControlAPIComposePrefixGuard_E2E(t *testing.T) {
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

	// A real registered+deployed template gives the foreign POST /tags a
	// valid `template` to name. The reserved-prefix guard sits AHEAD of the
	// template lookup, so the rejection does not depend on the template — but
	// using a real hash keeps the request well-formed in every other respect,
	// isolating the prefix guard as the sole cause of the 400.
	foreignTemplateHash := deploySQLiteTemplate(t, ep, map[string]any{
		"spec": map[string]any{
			"name":                  "compose-prefix-guard-foreign",
			"version":               "1",
			"frame_resolution_mode": "serial_queue",
			"nodes": []map[string]any{
				{"type": "worker", "executor": "stub"},
			},
		},
	})

	// ---- (1) Foreign raw POST /tags with a compose: prefix → 400, nothing created.
	const foreignTag = guardPrefix + "v1"
	status, raw := ep.PostJSON(t, "/tags", map[string]any{
		"tag":      foreignTag,
		"template": foreignTemplateHash,
	})
	if status != http.StatusBadRequest {
		t.Fatalf("raw POST /tags %q (no compose-origin) returned %d, want 400 — the server must reject a foreign client's reserved-prefix tag\nbody: %s",
			foreignTag, status, string(raw))
	}
	if !strings.Contains(strings.ToLower(string(raw)), "reserved prefix") {
		t.Fatalf("raw POST /tags 400 body did not carry a reserved-prefix diagnostic; got: %s", string(raw))
	}
	// The rejected create must have left no row: GET /tags omits it.
	if tagListed(t, ep, foreignTag) {
		t.Fatalf("after a rejected foreign POST /tags, GET /tags still lists %q — the rejected create must persist nothing", foreignTag)
	}

	// ---- (2) Foreign raw POST /instances with a compose: instance_key → 400, not created.
	const foreignInstanceKey = guardPrefix + "i1"
	status, raw = ep.PostJSON(t, "/instances", map[string]any{
		"template":     foreignTemplateHash,
		"instance_key": foreignInstanceKey,
		"params":       map[string]any{},
	})
	if status != http.StatusBadRequest {
		t.Fatalf("raw POST /instances with instance_key %q (no compose-origin) returned %d, want 400 — the server must reject a foreign client's reserved-prefix instance_key\nbody: %s",
			foreignInstanceKey, status, string(raw))
	}
	if !strings.Contains(strings.ToLower(string(raw)), "reserved prefix") {
		t.Fatalf("raw POST /instances 400 body did not carry a reserved-prefix diagnostic; got: %s", string(raw))
	}
	// The rejected create must have left no instance: GET /instances/{key} → 404.
	if getStatus := instanceGetStatus(t, ep, foreignInstanceKey); getStatus != http.StatusNotFound {
		t.Fatalf("after a rejected foreign POST /instances, GET /instances/%s returned %d, want 404 — the instance must not exist",
			foreignInstanceKey, getStatus)
	}

	// ---- (3) The SAME writes through the real compose engine SUCCEED.
	// compose.RunComposeUp stamps the trusted compose-origin marker
	// (X-Rimsky-Compose-Origin: 1) on every write, which the server guard
	// admits for the reserved prefix — proving the guard discriminates on
	// origin, not on the bare prefix. The manifest declares one template
	// (tagged so the engine produces `compose:project-alpha:gtpl@1`) and one
	// instance (key `compose:project-alpha:ginst`).
	manifestPath := writeGuardComposeManifest(t)
	if code := compose.RunComposeUp(ctx, []string{"-f", manifestPath, "--endpoint", ep.BaseURL, "--yes"}); code != 0 {
		t.Fatalf("rimsky compose up exited %d (want 0) — the compose-origin write of a reserved-prefix tag/instance must be admitted by the server guard", code)
	}

	// A client identical to the engine's, for read-back assertions against
	// the live control-api. Bare BaseURL — the control-api serves bare paths.
	c := cli.NewClient(ep.BaseURL)

	// The compose-prefixed tag now resolves to a registered+deployed
	// template — the server admitted the compose-origin reserved-prefix tag
	// write that it rejected from the foreign client above.
	const composeTag = guardPrefix + "gtpl@1"
	tpl, err := c.GetTemplate(ctx, composeTag)
	if err != nil {
		t.Fatalf("compose-origin tag %q did not resolve to a template — the server guard wrongly blocked a compose-originated write: %v", composeTag, err)
	}
	if tpl.State != "deployed" {
		t.Fatalf("compose template behind tag %q is in state %q, want deployed", composeTag, tpl.State)
	}

	// The compose-prefixed instance now exists — same discrimination, on the
	// instance-create path.
	const composeInstanceKey = guardPrefix + "ginst"
	inst, err := c.GetInstance(ctx, composeInstanceKey)
	if err != nil {
		t.Fatalf("compose-origin instance %q was not created — the server guard wrongly blocked a compose-originated instance write: %v", composeInstanceKey, err)
	}
	if inst.InstanceKey == nil || *inst.InstanceKey != composeInstanceKey {
		t.Fatalf("compose instance has unexpected instance_key %v, want %q", inst.InstanceKey, composeInstanceKey)
	}
}

// writeGuardComposeManifest writes a rimsky-compose.yml plus its referenced
// template spec into a fresh temp dir and returns the manifest path. The
// engine namespaces the declared tag/instance under
// `compose:<project>:<...>`, so this manifest's tag `gtpl@1` becomes
// `compose:project-alpha:gtpl@1` and instance `ginst` becomes
// `compose:project-alpha:ginst` on the wire — exactly the prefixed names the
// foreign POSTs above were rejected for.
func writeGuardComposeManifest(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	template := `# compose prefix-guard e2e template — one stub-executor worker node.
name: compose-prefix-guard-engine
version: "1"
frame_resolution_mode: serial_queue
nodes:
  - type: worker
    executor: stub
`
	manifest := `# compose prefix-guard e2e manifest — one template, one instance.
project: ` + guardProject + `
templates:
  - path: ./template.yml
    tag: gtpl@1
    state: deployed
instances:
  - template: gtpl@1
    name: ginst
`
	writeFile(t, filepath.Join(dir, "template.yml"), template)
	manifestPath := filepath.Join(dir, "rimsky-compose.yml")
	writeFile(t, manifestPath, manifest)
	return manifestPath
}

// tagListed reports whether the named tag appears in GET /tags. Walks pages
// (GET /tags has no server-side name filter) so a tag on a later page is not
// missed. Used to prove a rejected foreign create persisted nothing.
func tagListed(t *testing.T, ep harness.RimskyEndpoint, name string) bool {
	t.Helper()
	cursor := ""
	for {
		path := "/tags"
		if cursor != "" {
			path += "?cursor=" + cursor
		}
		status, raw := ep.GetJSON(t, path, "")
		if status != http.StatusOK {
			t.Fatalf("GET %s returned %d, want 200\nbody: %s", path, status, string(raw))
		}
		var resp struct {
			Tags []struct {
				Tag string `json:"tag"`
			} `json:"tags"`
			NextCursor string `json:"next_cursor"`
		}
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatalf("decode GET %s response: %v\nbody: %s", path, err, string(raw))
		}
		for _, tg := range resp.Tags {
			if tg.Tag == name {
				return true
			}
		}
		if resp.NextCursor == "" {
			return false
		}
		cursor = resp.NextCursor
	}
}

// instanceGetStatus returns the HTTP status of GET /instances/{key}. The
// route resolves an instance_key as well as an id, so a 404 proves no
// instance with that key exists. Used to prove a rejected foreign
// instance-create persisted nothing.
func instanceGetStatus(t *testing.T, ep harness.RimskyEndpoint, key string) int {
	t.Helper()
	status, _ := ep.GetJSON(t, "/instances/"+key, "")
	return status
}
