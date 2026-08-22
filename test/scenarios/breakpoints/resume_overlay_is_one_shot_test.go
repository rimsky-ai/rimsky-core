// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: breakpoint

package breakpoints

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func overlayTemplate(t *testing.T, h *scenario.Harness, name string) string {
	t.Helper()
	return h.DeployTemplate(node.TemplateSpec{
		Name: name, Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "worker", Executor: "stub"},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"tag": map[string]any{"type": "string"},
						"ok":  map[string]any{"type": "boolean", "readOnly": true},
					},
				}),
			),
		},
	})
}

func workerDispatchTags(h *scenario.Harness) []string {
	var out []string
	for _, o := range h.Stub.Observed() {
		if o.NodeType != "worker" {
			continue
		}
		v, _ := o.Attributes["tag"].(string)
		out = append(out, v)
	}
	return out
}

// @concept: breakpoint
func TestResumeOverlayAppliesToOneDispatchAndNeverBecomesAnInstanceOverride(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("worker").Success(map[string]any{"ok": true}, true, "overlay-one-shot")

	tid := overlayTemplate(t, h, "bp-overlay-one-shot")
	iid := createInstanceWithPause(t, h, tid, "ck-overlay-one-shot", map[string]any{})
	bpID := breakpointCreate(t, h, iid, map[string]any{
		"checkpoint": "before_dispatch",
		"matcher":    map[string]any{"node_type": "worker"},
	})
	status, _ := instanceResume(t, h, iid)
	require.Equal(t, http.StatusOK, status)

	hit := waitForHitOnBreakpoint(t, h, bpID)
	status, out := breakpointResume(t, h, iid, bpID, map[string]any{
		"hit_id":  hit.ID.String(),
		"overlay": map[string]any{"tag": "overlay-value"},
	})
	require.Equal(t, http.StatusOK, status, "resume should succeed: %v", out)

	waitForStubObservedCount(h, "worker", 1)
	require.Equal(t, []string{"overlay-value"}, workerDispatchTags(h),
		"the overlay reaches the dispatch that hit the breakpoint")

	var inst *persistence.InstanceRow
	require.NoError(t, h.Persist.Transaction(h.Ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := h.Persist.Instances().Get(ctx, iid, tx)
		inst = r
		return err
	}))
	require.NotNil(t, inst)
	require.Empty(t, inst.AttributeOverrides,
		"a resume overlay is one-shot: rimsky must never write it into the instance's attribute overrides")

	breakpointDelete(t, h, iid, bpID)
	h.PostInstanceMessage(iid, "", nil, "overlay-one-shot-second-wake")

	waitForStubObservedCount(h, "worker", 2)
	h.WaitForSchedulerQuiescence()
	require.Equal(t, []string{"overlay-value", ""}, workerDispatchTags(h),
		"the next dispatch of the same node carries no trace of the overlay")
}

// @concept: breakpoint
func TestResumeOverlayJoinsTheBagALaterBreakpointMatchesOn(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("worker").Success(map[string]any{"ok": true}, true, "overlay-matcher")

	tid := overlayTemplate(t, h, "bp-overlay-feeds-matcher")
	iid := createInstanceWithPause(t, h, tid, "ck-overlay-feeds-matcher", map[string]any{})
	first := breakpointCreate(t, h, iid, map[string]any{
		"checkpoint": "before_dispatch",
		"matcher":    map[string]any{"node_type": "worker"},
	})
	second := breakpointCreate(t, h, iid, map[string]any{
		"checkpoint": "before_dispatch",
		"matcher":    map[string]any{"attrs": map[string]any{"tag": "overlay-value"}},
	})
	status, _ := instanceResume(t, h, iid)
	require.Equal(t, http.StatusOK, status)

	firstHit := waitForHitOnBreakpoint(t, h, first)
	require.Empty(t, listHitsForBreakpoint(t, h, second),
		"the second breakpoint matches an attribute value no dispatch carries yet")

	status, out := breakpointResume(t, h, iid, first, map[string]any{
		"hit_id":  firstHit.ID.String(),
		"overlay": map[string]any{"tag": "overlay-value"},
	})
	require.Equal(t, http.StatusOK, status, "resume should succeed: %v", out)

	secondHit := waitForHitOnBreakpoint(t, h, second)
	require.Equal(t, 0, stubObservedCount(h, "worker"),
		"the overlay joined the dispatch's effective bag, so the second breakpoint matched and paused the runner")

	status, out = breakpointResume(t, h, iid, second, map[string]any{"hit_id": secondHit.ID.String()})
	require.Equal(t, http.StatusOK, status, "resume should succeed: %v", out)

	waitForStubObservedCount(h, "worker", 1)
	require.Equal(t, []string{"overlay-value"}, workerDispatchTags(h),
		"the same overlay reaches the executor once both breakpoints release")
}
