// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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

const guardProject = "project-alpha"

const guardPrefix = "compose:" + guardProject + ":"

func TestControlAPIComposePrefixGuard_E2E(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	netName := harness.SharedNetworkName(ctx, t)
	harness.StartExecutorStubOnNetwork(ctx, t, netName)

	ep := harness.BringUpRimsky(ctx, t,
		harness.WithSQLite(),
		harness.WithExistingNetwork(netName),
		harness.WithExecutor("stub", "executor-stub:9300"),
	)

	foreignTemplateHash := deployScenarioTemplate(t, ep, map[string]any{
		"spec": map[string]any{
			"name":    "compose-prefix-guard-foreign",
			"version": "1",
			"nodes": []map[string]any{
				{"type": "worker", "executor": "stub"},
			},
		},
	})

	const foreignTag = guardPrefix + "v1"
	status, raw := ep.PostJSON(t, "/v1/tags", map[string]any{
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
	if tagListed(t, ep, foreignTag) {
		t.Fatalf("after a rejected foreign POST /tags, GET /tags still lists %q — the rejected create must persist nothing", foreignTag)
	}

	const foreignInstanceKey = guardPrefix + "i1"
	status, raw = ep.PostJSON(t, "/v1/instances", map[string]any{
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
	if getStatus := instanceGetStatus(t, ep, foreignInstanceKey); getStatus != http.StatusNotFound {
		t.Fatalf("after a rejected foreign POST /instances, GET /instances/%s returned %d, want 404 — the instance must not exist",
			foreignInstanceKey, getStatus)
	}

	manifestPath := writeGuardComposeManifest(t)
	if code := compose.RunComposeUp(ctx, []string{"-f", manifestPath, "--endpoint", ep.BaseURL, "--yes"}); code != 0 {
		t.Fatalf("rimsky compose up exited %d (want 0) — the compose-origin write of a reserved-prefix tag/instance must be admitted by the server guard", code)
	}

	c := cli.NewClient(ep.BaseURL)

	const composeTag = guardPrefix + "gtpl@1"
	tpl, err := c.GetTemplate(ctx, composeTag)
	if err != nil {
		t.Fatalf("compose-origin tag %q did not resolve to a template — the server guard wrongly blocked a compose-originated write: %v", composeTag, err)
	}
	if tpl.State != "deployed" {
		t.Fatalf("compose template behind tag %q is in state %q, want deployed", composeTag, tpl.State)
	}

	const composeInstanceKey = guardPrefix + "ginst"
	inst, err := c.GetInstance(ctx, composeInstanceKey)
	if err != nil {
		t.Fatalf("compose-origin instance %q was not created — the server guard wrongly blocked a compose-originated instance write: %v", composeInstanceKey, err)
	}
	if inst.InstanceKey == nil || *inst.InstanceKey != composeInstanceKey {
		t.Fatalf("compose instance has unexpected instance_key %v, want %q", inst.InstanceKey, composeInstanceKey)
	}
}

func TestControlAPIComposePrefixGuard_PermissionGated_E2E(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	netName := harness.SharedNetworkName(ctx, t)
	harness.StartExecutorStubOnNetwork(ctx, t, netName)

	ep := harness.BringUpRimsky(ctx, t,
		harness.WithSQLite(),
		harness.WithExistingNetwork(netName),
		harness.WithExecutor("stub", "executor-stub:9300"),
	)

	adminKey := mintAPIKey(t, ep, "", "compose-guard-admin", []map[string]any{
		{"action": "*"},
	})

	nonAdminKey := mintAPIKey(t, ep, adminKey, "compose-guard-nonadmin", []map[string]any{
		{"action": "tag:create"},
		{"action": "tag:read"},
		{"action": "instance:create"},
		{"action": "instance:read"},
		{"action": "template:register"},
		{"action": "template:deploy"},
		{"action": "template:read"},
	})

	templateHash := deployScenarioTemplateAuth(t, ep, adminKey, map[string]any{
		"spec": map[string]any{
			"name":    "compose-prefix-perm-guard",
			"version": "1",
			"nodes": []map[string]any{
				{"type": "worker", "executor": "stub"},
			},
		},
	})

	const foreignTag = "compose:project-alpha:perm-v1"
	status, raw := ep.PostJSONWithHeaders(t, "/v1/tags", map[string]any{
		"tag":      foreignTag,
		"template": templateHash,
	}, map[string]string{
		"Authorization":           "Bearer " + nonAdminKey,
		"X-Rimsky-Compose-Origin": "1",
	})
	if status != http.StatusBadRequest {
		t.Fatalf("POST /tags %q with non-admin key + header returned %d, want 400 — `compose:origin` permission is missing so the header must NOT bypass the reserved-prefix guard\nbody: %s",
			foreignTag, status, string(raw))
	}
	if !strings.Contains(strings.ToLower(string(raw)), "reserved prefix") {
		t.Fatalf("POST /tags 400 body did not carry a reserved-prefix diagnostic; got: %s", string(raw))
	}
	if tagListedAuth(t, ep, adminKey, foreignTag) {
		t.Fatalf("after a rejected non-admin POST /tags, GET /tags still lists %q — the rejected create must persist nothing", foreignTag)
	}

	const foreignInstanceKey = "compose:project-alpha:perm-inst"
	status, raw = ep.PostJSONWithHeaders(t, "/v1/instances", map[string]any{
		"template":     templateHash,
		"instance_key": foreignInstanceKey,
		"params":       map[string]any{},
	}, map[string]string{
		"Authorization":           "Bearer " + nonAdminKey,
		"X-Rimsky-Compose-Origin": "1",
	})
	if status != http.StatusBadRequest {
		t.Fatalf("POST /instances with instance_key %q + non-admin key + header returned %d, want 400 — `compose:origin` permission is missing so the header must NOT bypass the reserved-prefix guard\nbody: %s",
			foreignInstanceKey, status, string(raw))
	}
	if !strings.Contains(strings.ToLower(string(raw)), "reserved prefix") {
		t.Fatalf("POST /instances 400 body did not carry a reserved-prefix diagnostic; got: %s", string(raw))
	}
	if got := instanceGetStatusAuth(t, ep, adminKey, foreignInstanceKey); got != http.StatusNotFound {
		t.Fatalf("after a rejected non-admin POST /instances, GET /instances/%s returned %d, want 404",
			foreignInstanceKey, got)
	}

	status, raw = ep.PostJSONWithHeaders(t, "/v1/tags", map[string]any{
		"tag":      foreignTag,
		"template": templateHash,
	}, map[string]string{
		"Authorization": "Bearer " + nonAdminKey,
	})
	if status != http.StatusBadRequest {
		t.Fatalf("POST /tags %q with non-admin key + NO header returned %d, want 400 — reserved-prefix guard must reject unmarked foreign writes too\nbody: %s",
			foreignTag, status, string(raw))
	}
	if !strings.Contains(strings.ToLower(string(raw)), "reserved prefix") {
		t.Fatalf("POST /tags (no-header) 400 body did not carry a reserved-prefix diagnostic; got: %s", string(raw))
	}

	originKey := mintAPIKey(t, ep, adminKey, "compose-guard-origin", []map[string]any{
		{"action": "tag:create"},
		{"action": "tag:read"},
		{"action": "instance:create"},
		{"action": "instance:read"},
		{"action": "compose:origin"},
	})

	const admittedTag = "compose:project-alpha:perm-v2"
	status, raw = ep.PostJSONWithHeaders(t, "/v1/tags", map[string]any{
		"tag":      admittedTag,
		"template": templateHash,
	}, map[string]string{
		"Authorization":           "Bearer " + originKey,
		"X-Rimsky-Compose-Origin": "1",
	})
	if status != http.StatusCreated {
		t.Fatalf("POST /tags %q with compose:origin-granted key + header returned %d, want 201 — a bearer actually holding compose:origin must be admitted\nbody: %s",
			admittedTag, status, string(raw))
	}
	if !tagListedAuth(t, ep, adminKey, admittedTag) {
		t.Fatalf("after an admitted compose:origin POST /tags, GET /tags does not list %q", admittedTag)
	}

	const admittedInstanceKey = "compose:project-alpha:perm-inst2"
	status, raw = ep.PostJSONWithHeaders(t, "/v1/instances", map[string]any{
		"template":     templateHash,
		"instance_key": admittedInstanceKey,
		"params":       map[string]any{},
	}, map[string]string{
		"Authorization":           "Bearer " + originKey,
		"X-Rimsky-Compose-Origin": "1",
	})
	if status != http.StatusCreated {
		t.Fatalf("POST /instances with instance_key %q + compose:origin-granted key + header returned %d, want 201 — a bearer actually holding compose:origin must be admitted\nbody: %s",
			admittedInstanceKey, status, string(raw))
	}
	if got := instanceGetStatusAuth(t, ep, adminKey, admittedInstanceKey); got != http.StatusOK {
		t.Fatalf("after an admitted compose:origin POST /instances, GET /instances/%s returned %d, want 200", admittedInstanceKey, got)
	}
}

func mintAPIKey(t *testing.T, ep harness.RimskyEndpoint, callerKey, name string, perms []map[string]any) string {
	t.Helper()
	headers := map[string]string{}
	if callerKey != "" {
		headers["Authorization"] = "Bearer " + callerKey
	}
	status, raw := ep.PostJSONWithHeaders(t, "/v1/auth/keys", map[string]any{
		"name":        name,
		"permissions": perms,
	}, headers)
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("POST /auth/keys (mint %q): %d %s", name, status, string(raw))
	}
	var resp struct {
		Plaintext string `json:"plaintext"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode mint %q response: %v: %s", name, err, string(raw))
	}
	if resp.Plaintext == "" {
		t.Fatalf("mint %q: plaintext empty: %s", name, string(raw))
	}
	return resp.Plaintext
}

func deployScenarioTemplateAuth(t *testing.T, ep harness.RimskyEndpoint, bearer string, body map[string]any) string {
	t.Helper()
	authHeader := map[string]string{"Authorization": "Bearer " + bearer}
	status, raw := ep.PostJSONWithHeaders(t, "/v1/templates", body, authHeader)
	if status != http.StatusCreated {
		t.Fatalf("POST /templates: %d %s", status, string(raw))
	}
	var resp struct {
		TemplateID string `json:"template_id"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode template response: %v: %s", err, string(raw))
	}
	if resp.TemplateID == "" {
		t.Fatalf("template_id empty: %s", string(raw))
	}
	deployStatus, deployRaw := ep.PostJSONWithHeaders(t,
		"/v1/templates/"+resp.TemplateID+"/deploy", map[string]any{}, authHeader)
	if deployStatus != http.StatusOK {
		t.Fatalf("POST /templates/%s/deploy: %d %s", resp.TemplateID, deployStatus, string(deployRaw))
	}
	return resp.TemplateID
}

func tagListedAuth(t *testing.T, ep harness.RimskyEndpoint, bearer, name string) bool {
	t.Helper()
	cursor := ""
	for {
		path := "/v1/tags"
		if cursor != "" {
			path += "?cursor=" + cursor
		}
		status, raw := ep.GetJSON(t, path, bearer)
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

func instanceGetStatusAuth(t *testing.T, ep harness.RimskyEndpoint, bearer, key string) int {
	t.Helper()
	status, _ := ep.GetJSON(t, "/v1/instances/"+key, bearer)
	return status
}

func writeGuardComposeManifest(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	template := `# compose prefix-guard e2e template — one stub-executor worker node.
name: compose-prefix-guard-engine
version: "1"
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

func tagListed(t *testing.T, ep harness.RimskyEndpoint, name string) bool {
	t.Helper()
	return tagListedAuth(t, ep, "", name)
}

func instanceGetStatus(t *testing.T, ep harness.RimskyEndpoint, key string) int {
	t.Helper()
	return instanceGetStatusAuth(t, ep, "", key)
}
