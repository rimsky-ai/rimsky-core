// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @story: compose-lifecycle
package scenarios

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli"
	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli/compose"
	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
	"github.com/rimsky-ai/rimsky-core/test/support/awaited"
)

type planExpectation int

const (
	planChanges planExpectation = iota
	planIsANoOp
)

const planPreviewProject = "plan-previews"

const planPreviewTemplateFile = "t.yml"

const planPreviewManifestFile = "rimsky-compose.yml"

func TestComposePlanPreviewsExactlyWhatComposeUpApplies(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	stack := harness.StartAllInOneZeroConfig(ctx, t, "")
	project := newPlanPreviewProject(t, stack.Endpoint.BaseURL)

	project.writeManifest(t, planPreviewManifestWith(`templates:
  - path: `+planPreviewTemplateFile+`
    tag: probe
    state: deployed
instances:
  - template: probe
    name: one
`))
	project.requirePlanMatchesApply(t, "first apply", planChanges)

	project.requirePlanMatchesApply(t, "no-op re-apply", planIsANoOp)

	project.terminateEveryInstance(t)
	project.writeManifest(t, planPreviewManifestWith(`templates:
  - path: `+planPreviewTemplateFile+`
    tag: probe
    state: deployed
`))
	project.requirePlanMatchesApply(t, "instance entry removed", planChanges)

	project.writeManifest(t, planPreviewManifestWith(""))
	project.requirePlanMatchesApply(t, "template entry removed", planChanges)
}

type planPreviewRun struct {
	dir      string
	endpoint harness.RimskyEndpoint
}

func newPlanPreviewProject(t *testing.T, baseURL string) *planPreviewRun {
	t.Helper()
	dir := t.TempDir()
	spec := "name: plan-previews-probe\nversion: \"1\"\nnodes:\n  - type: work\n    executor: verifier-shape-checks\n"
	if err := os.WriteFile(filepath.Join(dir, planPreviewTemplateFile), []byte(spec), 0o644); err != nil {
		t.Fatalf("write the manifest's template spec: %v", err)
	}
	return &planPreviewRun{dir: dir, endpoint: harness.RimskyEndpoint{BaseURL: baseURL}}
}

func planPreviewManifestWith(body string) string {
	return "project: " + planPreviewProject + "\n" + body
}

func (p *planPreviewRun) writeManifest(t *testing.T, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(p.dir, planPreviewManifestFile), []byte(body), 0o644); err != nil {
		t.Fatalf("write the compose manifest: %v", err)
	}
}

func (p *planPreviewRun) requirePlanMatchesApply(t *testing.T, round string, expect planExpectation) {
	t.Helper()
	before := p.worldFingerprint(t)
	planOut, _ := captureRun(t, func() int {
		return compose.RunComposePlan(context.Background(), p.composeArgs())
	})
	if after := p.worldFingerprint(t); after != before {
		t.Fatalf("%s: compose plan changed the live world\nbefore: %s\nafter:  %s", round, before, after)
	}
	upOut, code := captureRun(t, func() int {
		return compose.RunComposeUp(context.Background(), append(p.composeArgs(), "--yes"))
	})
	if code != 0 {
		t.Fatalf("%s: compose up exited %d:\n%s", round, code, upOut)
	}
	planned := plannedOpsByKind(t, planOut)
	applied := appliedOpsByKind(t, upOut)
	if expect == planChanges && planned.total() == 0 {
		t.Fatalf("%s: the plan lists no operation, so comparing it against the apply proves nothing:\n%s",
			round, planOut)
	}
	if expect == planIsANoOp && planned.total() != 0 {
		t.Errorf("%s: re-applying an unchanged manifest planned %v, and compose owes a no-op here",
			round, planned)
	}
	for _, kind := range composeObjectKinds {
		if !slices.Equal(planned[kind], applied[kind]) {
			t.Errorf("%s: the plan's %s operations are not what up applied, in order. The plan groups its "+
				"steps by object kind for reading, and up reports them in execution order. This comparison "+
				"therefore takes one kind at a time, and the order within a kind must match\n"+
				"planned: %v\napplied: %v\n--- plan ---\n%s\n--- up ---\n%s",
				round, kind, planned[kind], applied[kind], planOut, upOut)
		}
	}
}

func (p *planPreviewRun) composeArgs() []string {
	return []string{"-f", filepath.Join(p.dir, planPreviewManifestFile), "--endpoint", p.endpoint.BaseURL}
}

type composeObjectKind string

const (
	templateKind composeObjectKind = "template"
	tagKind      composeObjectKind = "tag"
	instanceKind composeObjectKind = "instance"
)

var composeObjectKinds = []composeObjectKind{templateKind, tagKind, instanceKind}

type opsByKind map[composeObjectKind][]string

func (o opsByKind) total() int {
	n := 0
	for _, kind := range composeObjectKinds {
		n += len(o[kind])
	}
	return n
}

var planHeaderKinds = map[string]composeObjectKind{
	"Templates:": templateKind,
	"Tags:":      tagKind,
	"Instances:": instanceKind,
}

var appliedVerbKinds = map[string]composeObjectKind{
	"register":        templateKind,
	"deploy":          templateKind,
	"undeploy":        templateKind,
	"template-delete": templateKind,
	"tag":             tagKind,
	"tag-move":        tagKind,
	"tag-rm":          tagKind,
	"create":          instanceKind,
	"instance-delete": instanceKind,
}

func plannedOpsByKind(t *testing.T, out string) opsByKind {
	t.Helper()
	ops := opsByKind{}
	var kind composeObjectKind
	for _, line := range strings.Split(out, "\n") {
		if header, ok := planHeaderKinds[strings.TrimSpace(line)]; ok {
			kind = header
			continue
		}
		fields := strings.Fields(line)
		if !strings.HasPrefix(line, "  ") || len(fields) < 3 {
			continue
		}
		if fields[0] != "+" && fields[0] != "-" && fields[0] != "~" {
			continue
		}
		if kind == "" {
			t.Fatalf("the plan lists %q before any of the %v headers, so this test cannot say which object "+
				"kind it belongs to:\n%s", strings.TrimSpace(line), planHeaderKinds, out)
		}
		ops[kind] = append(ops[kind], canonicalComposeOp(fields[1], fields[2]))
	}
	return ops
}

func appliedOpsByKind(t *testing.T, out string) opsByKind {
	t.Helper()
	ops := opsByKind{}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if !strings.HasPrefix(line, "  ") || len(fields) < 3 || fields[len(fields)-1] != "ok" {
			continue
		}
		kind, ok := appliedVerbKinds[fields[0]]
		if !ok {
			t.Fatalf("up applied %q, an operation this test cannot map to an object kind:\n%s", fields[0], out)
		}
		ops[kind] = append(ops[kind], canonicalComposeOp(fields[0], fields[1]))
	}
	return ops
}

func canonicalComposeOp(op, object string) string {
	if op == "tag-rm" {
		op = "tag-delete"
	}
	return op + " " + cli.TruncHash(object)
}

func (p *planPreviewRun) worldFingerprint(t *testing.T) string {
	t.Helper()
	return strings.Join([]string{
		strings.Join(listTemplateIDs(t, p.endpoint), ","),
		strings.Join(p.tagNames(t), ","),
		strings.Join(listInstanceKeys(t, p.endpoint), ","),
	}, " | ")
}

func (p *planPreviewRun) tagNames(t *testing.T) []string {
	t.Helper()
	var resp struct {
		Tags []struct {
			Tag          string `json:"tag"`
			TemplateHash string `json:"template_hash"`
		} `json:"tags"`
	}
	readSurveyJSON(t, p.endpoint, "/v1/tags", &resp)
	names := []string{}
	for _, tag := range resp.Tags {
		names = append(names, tag.Tag+"→"+tag.TemplateHash)
	}
	slices.Sort(names)
	return names
}

func (p *planPreviewRun) terminateEveryInstance(t *testing.T) {
	t.Helper()
	for _, id := range liveInstanceIDs(t, p.endpoint) {
		status, raw := p.endpoint.PostJSON(t, "/v1/instances/"+id+"/terminate?force=true", map[string]any{})
		if status != http.StatusOK && status != http.StatusAccepted && status != http.StatusNoContent {
			t.Fatalf("terminate instance %s: %d %s", id, status, string(raw))
		}
	}
	awaited.Until(t, "every compose instance to reach a terminated state, so the manifest may drop its entry",
		func() bool { return len(liveInstanceIDs(t, p.endpoint)) == 0 })
}
