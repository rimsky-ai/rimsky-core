// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: lineage-record
// @story: lineage-admin
package scenarios

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestLineageAdminPrune(t *testing.T) {
	t.Parallel()

	h := scenario.Start(t, scenario.HarnessOpts{})

	h.Stub.WhenType("noop").Success(map[string]any{"ok": true}, true, "ok")
	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "lineage-admin-prune-e2e", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{
				Type: "noop", Executor: "stub",
			}),
		},
	})
	iid := h.CreateInstance(tid, "ck-lineage-admin-prune-e2e", map[string]any{})

	now := time.Now().UTC()
	const (
		oldProducer      = "prune7d"
		boundaryProducer = "prune24h"
		newProducer      = "prune1h"
	)
	oldObservedAt := now.Add(-7 * 24 * time.Hour)
	boundaryObservedAt := now.Add(-24 * time.Hour)
	newObservedAt := now.Add(-1 * time.Hour)

	seedClaimTerminal(t, h, iid.String(), oldProducer, oldObservedAt)
	seedClaimTerminal(t, h, iid.String(), boundaryProducer, boundaryObservedAt)
	seedClaimTerminal(t, h, iid.String(), newProducer, newObservedAt)

	require.Equal(t, 1, byProducerCount(t, h, oldProducer),
		"seed sanity: old producer must have exactly 1 row before prune")
	require.Equal(t, 1, byProducerCount(t, h, boundaryProducer),
		"seed sanity: boundary producer must have exactly 1 row before prune")
	require.Equal(t, 1, byProducerCount(t, h, newProducer),
		"seed sanity: new producer must have exactly 1 row before prune")

	cutoff := boundaryObservedAt

	pruneBody, err := json.Marshal(map[string]any{
		"before": cutoff.Format(time.RFC3339Nano),
	})
	require.NoError(t, err)
	resp, err := http.Post(
		h.ControlBase+"/v1/admin/lineage/prune",
		"application/json",
		bytes.NewReader(pruneBody),
	)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, "prune POST status")

	var pruneResp struct {
		Deleted int    `json:"deleted"`
		Before  string `json:"before"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&pruneResp))

	require.Equal(t, 1, pruneResp.Deleted,
		"prune must delete exactly the strictly-older row (1); count != 1 maps to a falsifier branch")
	require.Equal(t, cutoff.Format(time.RFC3339Nano), pruneResp.Before,
		"prune response must echo the cutoff")

	require.Equal(t, 0, byProducerCount(t, h, oldProducer),
		"FALSIFIER: the strictly-older row must be removed after prune")
	require.Equal(t, 1, byProducerCount(t, h, boundaryProducer),
		"FALSIFIER: the row AT the cutoff boundary must survive (predicate is strict `<`, not `<=`)")
	require.Equal(t, 1, byProducerCount(t, h, newProducer),
		"FALSIFIER: a row strictly newer than the cutoff must survive")

	t.Logf("STORY-lineage-admin GREEN: deleted=%d (old removed; boundary + new kept)", pruneResp.Deleted)
}

func seedClaimTerminal(t *testing.T, h *scenario.Harness, instanceID, producerName string, observedAt time.Time) {
	t.Helper()

	rowID := uuid.New()
	frameID := uuid.New()
	syntheticRunID := uuid.New().String()
	syntheticClaimHandleID := uuid.New().String()

	record := map[string]any{
		"producer_name":   producerName,
		"version_id":      "v1",
		"run_id":          syntheticRunID,
		"claim_handle_id": syntheticClaimHandleID,
	}
	recordJSON, err := json.Marshal(record)
	require.NoError(t, err)

	iidParsed, err := uuid.Parse(instanceID)
	require.NoError(t, err)

	const insertSQL = `
		INSERT INTO rimsky_lineage (
			id, record_kind, instance_id, frame_id, observed_at, record, outcome
		) VALUES ($1, 'claim_terminal', $2, $3, $4, $5, 'committed')`
	_, err = h.Pool.Exec(h.Ctx, insertSQL, rowID, iidParsed, frameID, observedAt, recordJSON)
	require.NoError(t, err, "seed lineage row for producer %s", producerName)
}

func byProducerCount(t *testing.T, h *scenario.Harness, producerName string) int {
	t.Helper()
	url := h.ControlBase + "/v1/lineage/by-producer/" + producerName
	status, body := httpGetJSON(t, url)
	require.Equal(t, http.StatusOK, status, "GET by-producer %s: %s", producerName, body)
	var out struct {
		Records []map[string]any `json:"records"`
	}
	require.NoError(t, json.Unmarshal(body, &out),
		fmt.Sprintf("decode by-producer response for %s: %s", producerName, body))
	return len(out.Records)
}
