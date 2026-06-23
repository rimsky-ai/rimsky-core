// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package controlapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks/storetest"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	pgtest "github.com/rimsky-ai/rimsky-core/test/support/pgmigrate"
)

type stubDataProcessor struct {
	name         string
	versions     []runtime.DataProcessingVersion
	listVersions []runtime.ListVersionsInput
}

func (s *stubDataProcessor) Name() string { return s.name }
func (s *stubDataProcessor) BeginCandidate(context.Context, runtime.BeginCandidateInput) (runtime.BeginCandidateOutput, error) {
	return runtime.BeginCandidateOutput{}, nil
}
func (s *stubDataProcessor) CommitCandidate(context.Context, runtime.CommitCandidateInput) (runtime.CommitCandidateOutput, error) {
	return runtime.CommitCandidateOutput{}, nil
}
func (s *stubDataProcessor) AbandonCandidate(context.Context, runtime.AbandonCandidateInput) error {
	return nil
}
func (s *stubDataProcessor) ListVersions(_ context.Context, in runtime.ListVersionsInput) (runtime.ListVersionsOutput, error) {
	s.listVersions = append(s.listVersions, in)
	return runtime.ListVersionsOutput{Versions: s.versions}, nil
}
func (s *stubDataProcessor) ListPartitions(context.Context, runtime.ListPartitionsInput) (runtime.ListPartitionsOutput, error) {
	return runtime.ListPartitionsOutput{}, nil
}
func (s *stubDataProcessor) GetVersionSchema(context.Context, runtime.GetVersionSchemaInput) (runtime.GetVersionSchemaOutput, error) {
	return runtime.GetVersionSchemaOutput{}, nil
}

type stubDataProcessorRegistry struct {
	clients map[string]runtime.DataProcessingClient
}

func (r *stubDataProcessorRegistry) Get(name string) (runtime.DataProcessingClient, bool) {
	c, ok := r.clients[name]
	return c, ok
}

type assetHarness struct {
	*harness
	dp      *stubDataProcessor
	content *storetest.Fake
}

func newAssetHarness(t *testing.T, versions []runtime.DataProcessingVersion) (*assetHarness, func()) {
	t.Helper()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)

	reg := locks.NewRegistry()
	contentFake := storetest.NewFake("content", claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
		Protocols:             []string{claimproducer.ProtocolDataProcessing},
	})
	topicsFake := storetest.NewFake("topics-ring", claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}})
	reg.Add("content", contentFake)
	reg.Add("topics-ring", topicsFake)

	lcReg := locks.NewLifecycleRegistry()
	lcReg.Add("content", contentFake)
	lcReg.Add("topics-ring", topicsFake)

	dp := &stubDataProcessor{name: "content", versions: versions}
	dpReg := &stubDataProcessorRegistry{clients: map[string]runtime.DataProcessingClient{"content": dp}}

	capLog := shared.NewCapturingLogger()
	app := NewApp(AppDeps{
		Persist:        d.Tables(),
		Queue:          d.Queue(),
		Clock:          shared.SystemClock{},
		Logger:         capLog,
		ClaimProducers: reg,
		LifecycleSubs:  lcReg,
		DataProcessors: dpReg,
		Executors: map[string]ExecutorEntry{
			"worker": {Transport: "grpc", Endpoint: "localhost:0"},
		},
	})
	srv := httptest.NewServer(app)

	h := &harness{srv: srv, driver: d, persist: d.Tables(), stores: reg, logger: capLog}
	ah := &assetHarness{harness: h, dp: dp, content: contentFake}
	return ah, func() { srv.Close() }
}

func assetTemplateBody(name string) map[string]any {
	return map[string]any{
		"spec": map[string]any{
			"name":    name,
			"version": "v1",
			"nodes": []map[string]any{
				{
					"type":     "producer",
					"executor": "worker",
					"claim_producers": []map[string]any{
						{"name": "content", "selector": "items/x", "intent": "rw", "alias": "dataset"},
					},
				},
				{"type": "downstream", "executor": "worker", "subscribes": []map[string]any{{"node": "producer", "type": "terminal/*", "force_upstream_refresh": false}}},
			},
		},
	}
}

func (ah *assetHarness) seedAsset(t *testing.T, namePrefix string) (instID uuid.UUID, claimID uuid.UUID, producerNodeID uuid.UUID, frameID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	h := ah.harness

	_, out := h.httpJSON(t, "POST", "/v1/templates", assetTemplateBody(namePrefix+"-"+uuid.NewString()))
	tplID, _ := out["template_id"].(string)
	require.NotEmpty(t, tplID)
	deployStatus, _ := h.httpJSON(t, "POST", "/v1/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, deployStatus)
	status, out := h.httpJSON(t, "POST", "/v1/instances", map[string]any{
		"template":     tplID,
		"instance_key": namePrefix + "-ck-" + uuid.NewString(),
	})
	require.Equal(t, http.StatusCreated, status, out)
	instStr, _ := out["instance_id"].(string)
	instID, err := uuid.Parse(instStr)
	require.NoError(t, err)

	var (
		mainScopeID shared.UUID
	)
	require.NoError(t, h.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		nodes, err := h.persist.Nodes().ListByInstance(ctx, shared.UUID(instID), tx)
		if err != nil {
			return err
		}
		var prodNodeUUID shared.UUID
		for _, n := range nodes {
			if n.NodeType == "producer" {
				prodNodeUUID = n.ID
			}
		}
		producerNodeID = uuid.UUID(prodNodeUUID)
		msgID := shared.UUID(uuid.New())
		if err := h.persist.Messages().Insert(ctx, tx, persistence.EnqueueMessageRequest{
			ID:         msgID,
			InstanceID: shared.UUID(instID),
			Type:       "test/seed",
			Sender:     "test",
			SenderKind: "operator",
		}); err != nil {
			return err
		}
		_ = prodNodeUUID
		fid, err := h.persist.Frames().InsertFrame(ctx, shared.UUID(instID), msgID, 600000, tx)
		if err != nil {
			return err
		}
		frameID = uuid.UUID(fid)
		return nil
	}))
	require.NotEqual(t, uuid.Nil, producerNodeID)

	pgtest.QueryRowForTest(ctx, t, h.driver,
		`SELECT main_run_scope_id FROM rimsky_instances WHERE id = $1`,
		[]any{instID}, &mainScopeID)

	nodeRunID := uuid.New()
	pgtest.ExecForTest(ctx, t, h.driver, `
		INSERT INTO rimsky_node_runs
			(id, node_id, executor_name, required_stores, enqueued_at, state, frame_id, run_scope_id, sequence)
		VALUES ($1, $2, 'worker', ARRAY[]::text[], now(), 'fresh', $3, $4, 0)
	`, nodeRunID, producerNodeID, frameID, mainScopeID)

	claimID = uuid.New()
	pgtest.ExecForTest(ctx, t, h.driver, `
		INSERT INTO rimsky_claim_handles
			(id, node_run_id, lock_kind, producer_name, claim_scope_data, intent,
			 holder_node_id, expires_at, lifetime, state, version_id, resolved_at)
		VALUES ($1, $2, 'claim_scope', 'content', $3::jsonb, 'rw',
			 $4, now() + interval '1 hour', 'durable', 'committed', 'v-001', now())
	`, claimID, nodeRunID, `{"area":"north"}`, producerNodeID)

	return instID, claimID, producerNodeID, frameID
}

func TestAssetEndpoints_ListSurfacesDurableCommittedRows(t *testing.T) {
	t.Parallel()
	ah, teardown := newAssetHarness(t, nil)
	t.Cleanup(teardown)

	instID, claimID, _, _ := ah.seedAsset(t, "asset-list")

	status, out := ah.harness.httpJSON(t, "GET", "/v1/instances/"+instID.String()+"/assets", nil)
	require.Equal(t, http.StatusOK, status, out)
	assets, _ := out["assets"].([]any)
	require.Len(t, assets, 1, "exactly the seeded durable/committed asset must surface")

	item := assets[0].(map[string]any)
	require.Equal(t, claimID.String(), item["claim_id"])
	require.Equal(t, "content", item["producer_name"])
	require.Equal(t, "committed", item["state"])
	require.Equal(t, "durable", item["lifetime"])
	require.Equal(t, "v-001", item["version_id"])
	require.Equal(t, "producer.dataset", item["alias"])
	scope, _ := item["scope"].(map[string]any)
	require.Equal(t, "north", scope["area"])
	require.NotContains(t, item, "address")
}

func TestAssetEndpoints_GetSingleAsset(t *testing.T) {
	t.Parallel()
	ah, teardown := newAssetHarness(t, nil)
	t.Cleanup(teardown)

	instID, claimID, _, _ := ah.seedAsset(t, "asset-get")

	status, out := ah.harness.httpJSON(t, "GET", "/v1/instances/"+instID.String()+"/assets/producer.dataset", nil)
	require.Equal(t, http.StatusOK, status, out)
	require.Equal(t, claimID.String(), out["claim_id"])
	require.Equal(t, "producer.dataset", out["alias"])

	status, _ = ah.harness.httpJSON(t, "GET", "/v1/instances/"+instID.String()+"/assets/producer.ghost", nil)
	require.Equal(t, http.StatusNotFound, status)

	status, _ = ah.harness.httpJSON(t, "GET", "/v1/instances/"+instID.String()+"/assets/nodot", nil)
	require.Equal(t, http.StatusBadRequest, status)
}

func TestAssetEndpoints_VersionsProxiesDataProcessor(t *testing.T) {
	t.Parallel()
	now := time.Now().Unix()
	versions := []runtime.DataProcessingVersion{
		{VersionID: "v-001", CommittedAtUnixS: now - 100, ProducerMetadata: []byte(`{"rows":10}`)},
		{VersionID: "v-002", CommittedAtUnixS: now, ProducerMetadata: []byte(`{"rows":20}`)},
	}
	ah, teardown := newAssetHarness(t, versions)
	t.Cleanup(teardown)

	instID, claimID, _, _ := ah.seedAsset(t, "asset-ver")

	status, out := ah.harness.httpJSON(t, "GET", "/v1/instances/"+instID.String()+"/assets/producer.dataset/versions", nil)
	require.Equal(t, http.StatusOK, status, out)
	got, _ := out["versions"].([]any)
	require.Len(t, got, 2)
	v0 := got[0].(map[string]any)
	require.Equal(t, "v-001", v0["version_id"])
	v1 := got[1].(map[string]any)
	require.Equal(t, "v-002", v1["version_id"])

	require.Len(t, ah.dp.listVersions, 1)
	require.Equal(t, "content", ah.dp.listVersions[0].ProducerName)
	require.Equal(t, claimID.String(), ah.dp.listVersions[0].ClaimHandleID)
}

func TestAssetEndpoints_MaterializationHistoryJoinsLineage(t *testing.T) {
	t.Parallel()
	ah, teardown := newAssetHarness(t, nil)
	t.Cleanup(teardown)
	ctx := context.Background()

	instID, claimID, _, frameID := ah.seedAsset(t, "asset-hist")

	insertClaimTerminal := func(versionID string, observedAt time.Time) {
		rec, err := json.Marshal(map[string]any{
			"claim_handle_id": claimID.String(),
			"producer_name":   "content",
			"version_id":      versionID,
		})
		require.NoError(t, err)
		require.NoError(t, ah.harness.persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			return ah.harness.persist.Lineage().Insert(ctx, tx, persistence.LineageRow{
				ID:         shared.UUID(uuid.New()),
				RecordKind: persistence.LineageRecordKindClaimTerminal,
				InstanceID: shared.UUID(instID),
				FrameID:    shared.UUID(frameID),
				ObservedAt: observedAt,
				Record:     rec,
				Outcome:    persistence.LineageOutcomeCommitted,
			})
		}))
	}
	base := time.Now().UTC()
	insertClaimTerminal("v-001", base)
	insertClaimTerminal("v-002", base.Add(time.Second))

	status, out := ah.harness.httpJSON(t, "GET", "/v1/instances/"+instID.String()+"/assets/producer.dataset/materialization-history", nil)
	require.Equal(t, http.StatusOK, status, out)
	hist, _ := out["materialization_history"].([]any)
	require.Len(t, hist, 2, "both claim_terminal rows for this claim handle must join in")
	first := hist[0].(map[string]any)
	require.Equal(t, "claim_terminal", first["record_kind"])
}

func TestAssetEndpoints_DeleteReleasesAndDeletes(t *testing.T) {
	t.Parallel()
	ah, teardown := newAssetHarness(t, nil)
	t.Cleanup(teardown)
	ctx := context.Background()

	instID, claimID, _, _ := ah.seedAsset(t, "asset-del")

	status, out := ah.harness.httpJSON(t, "DELETE", "/v1/instances/"+instID.String()+"/assets/producer.dataset", nil)
	require.Equal(t, http.StatusOK, status, out)
	require.Equal(t, true, out["deleted"])

	var remaining int
	pgtest.QueryRowForTest(ctx, t, ah.harness.driver,
		`SELECT count(*) FROM rimsky_claim_handles WHERE id = $1`,
		[]any{claimID}, &remaining)
	require.Equal(t, 0, remaining, "asset row must be deleted")

	var releaseFired bool
	for _, c := range ah.content.Calls() {
		if c.Verb == "release" && c.ClaimID == claimproducer.ClaimID(claimID.String()) {
			releaseFired = true
		}
	}
	require.True(t, releaseFired, "Release must fire on the producer before row delete")
}

func TestAssetEndpoints_DeleteRefusesInFlightHolder(t *testing.T) {
	t.Parallel()
	ah, teardown := newAssetHarness(t, nil)
	t.Cleanup(teardown)
	ctx := context.Background()

	instID, claimID, producerNodeID, frameID := ah.seedAsset(t, "asset-del-busy")

	holderRunID := uuid.New()
	var mainScopeID shared.UUID
	pgtest.QueryRowForTest(ctx, t, ah.harness.driver,
		`SELECT main_run_scope_id FROM rimsky_instances WHERE id = $1`,
		[]any{instID}, &mainScopeID)
	pgtest.ExecForTest(ctx, t, ah.harness.driver, `
		INSERT INTO rimsky_node_runs
			(id, node_id, executor_name, required_stores, enqueued_at, state, frame_id, run_scope_id, sequence)
		VALUES ($1, $2, 'worker', ARRAY[]::text[], now(), 'running', $3, $4, 0)
	`, holderRunID, producerNodeID, frameID, mainScopeID)
	pgtest.ExecForTest(ctx, t, ah.harness.driver, `
		INSERT INTO rimsky_claim_holders (id, claim_handle_id, holder_run_id, state, frame_id)
		VALUES ($1, $2, $3, 'active', $4)
	`, uuid.New(), claimID, holderRunID, frameID)

	status, out := ah.harness.httpJSON(t, "DELETE", "/v1/instances/"+instID.String()+"/assets/producer.dataset", nil)
	require.Equal(t, http.StatusConflict, status, out)
	require.EqualValues(t, 1, out["active_count"])

	var remaining int
	pgtest.QueryRowForTest(ctx, t, ah.harness.driver,
		`SELECT count(*) FROM rimsky_claim_handles WHERE id = $1`,
		[]any{claimID}, &remaining)
	require.Equal(t, 1, remaining, "asset row must survive a refused delete")

	for _, c := range ah.content.Calls() {
		require.NotEqual(t, "release", c.Verb, "Release must not fire when delete is refused")
	}
}
