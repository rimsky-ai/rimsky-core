// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// assets_test.go — F5 characterization tests for the asset handlers
// against the real chi router + real Postgres (the app_test.go::newHarness
// pattern, extended with a DataProcessing-advertising store + a stub
// DataProcessing registry). The asset surface works today; these tests
// pin the observable response bodies/status so a future regression in the
// alias resolution, the ListByInstanceAndState(committed, durable) row
// discovery, the ListVersions proxy, the lineage join, or the
// delete/materialize 409-if-in-flight gate surfaces as a red test rather
// than a silent behavior change.
//
// Why a bespoke harness (assetHarness) rather than newHarness: the asset
// endpoints filter to producers that advertise `data_processing` and the
// versions endpoint dials a DataProcessing client. newHarness wires
// neither, so an asset row would be silently dropped by
// buildDataProcessingPredicate. assetHarness registers a content store
// that advertises data_processing and a stub DataProcessors registry.

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

// stubDataProcessor is a minimal runtime.DataProcessingClient that records
// the ListVersions call and returns a canned version list. Only ListVersions
// is exercised by the asset surface; the other verbs return empty.
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

// stubDataProcessorRegistry is a single-entry DataProcessingRegistry.
type stubDataProcessorRegistry struct {
	clients map[string]runtime.DataProcessingClient
}

func (r *stubDataProcessorRegistry) Get(name string) (runtime.DataProcessingClient, bool) {
	c, ok := r.clients[name]
	return c, ok
}

// assetHarness is newHarness plus a data_processing-advertising "content"
// store and a stub DataProcessors registry, so the asset endpoints surface
// rows and the versions proxy resolves.
type assetHarness struct {
	*harness
	dp *stubDataProcessor
	// release captures whether ClaimProducer.Release fired (asserted by the
	// delete test). The content fake records all calls.
	content *storetest.Fake
}

func newAssetHarness(t *testing.T, versions []runtime.DataProcessingVersion) (*assetHarness, func()) {
	t.Helper()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)

	reg := locks.NewRegistry()
	// `content` advertises data_processing so handleListAssets's predicate
	// includes the seeded durable claim and handleDeleteAsset can resolve a
	// producer to Release.
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
		Stores:         reg,
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

// assetTemplateBody builds a template whose `producer` node declares a
// `content` store aliased `dataset`. The alias resolution
// (lookupClaimAliasForProducer / lookupProducerForAlias) walks this
// template to map node_type↔producer↔alias; the durable claim handle is
// seeded directly in the DB (the asset row state lives there, not on the
// template store entry).
func assetTemplateBody(name string) map[string]any {
	return map[string]any{
		"spec": map[string]any{
			"name":                  name,
			"version":               "v1",
			"frame_resolution_mode": "serial_queue",
			"nodes": []map[string]any{
				{
					"type":     "producer",
					"executor": "worker",
					"stores": []map[string]any{
						{"name": "content", "selector": "items/x", "intent": "rw", "alias": "dataset"},
					},
				},
				{"type": "downstream", "executor": "worker", "subscribes": []map[string]any{{"node": "producer", "type": "terminal/*"}}},
			},
		},
	}
}

// seedAsset deploys assetTemplateBody, creates an instance, and inserts a
// committed/durable claim handle row owned by the `producer` node + a
// node-run that owns it (so the in-flight gate has a run to look at). The
// claim handle is seeded via raw SQL because the Insert path always lands
// state='active'; assets are state='committed' rows by construction
// (post-Promote), so a direct insert is the faithful seed. Returns the
// instance id and the seeded claim handle id.
func (ah *assetHarness) seedAsset(t *testing.T, namePrefix string) (instID uuid.UUID, claimID uuid.UUID, producerNodeID uuid.UUID, frameID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	h := ah.harness

	_, out := h.httpJSON(t, "POST", "/templates", assetTemplateBody(namePrefix+"-"+uuid.NewString()))
	tplID, _ := out["template_id"].(string)
	require.NotEmpty(t, tplID)
	deployStatus, _ := h.httpJSON(t, "POST", "/templates/"+tplID+"/deploy", map[string]any{})
	require.Equal(t, http.StatusOK, deployStatus)
	status, out := h.httpJSON(t, "POST", "/instances", map[string]any{
		"template":     tplID,
		"instance_key": namePrefix + "-ck-" + uuid.NewString(),
	})
	require.Equal(t, http.StatusCreated, status, out)
	instStr, _ := out["instance_id"].(string)
	instID, err := uuid.Parse(instStr)
	require.NoError(t, err)

	// Resolve the `producer` node id + an existing frame for the FK chain.
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
		// Re-use a seeded frame so the node-run + lineage FKs are satisfiable.
		fid, err := h.persist.Frames().EnqueueSerialFrame(ctx, shared.UUID(instID), prodNodeUUID, 600000, tx)
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

	// A node-run for the producer node so a claim_holder can FK against it.
	nodeRunID := uuid.New()
	pgtest.ExecForTest(ctx, t, h.driver, `
		INSERT INTO rimsky_node_runs
			(id, node_id, executor_name, required_stores, enqueued_at, phase, state, frame_id, run_scope_id)
		VALUES ($1, $2, 'worker', ARRAY[]::text[], now(), 'completed', 'fresh', $3, $4)
	`, nodeRunID, producerNodeID, frameID, mainScopeID)

	// The durable, committed asset row. holder_supervisor_id is NULL per the
	// inactive-has-no-holder CHECK; lock_kind='claim_scope' with producer +
	// scope + intent set per the claim_handle_kind_fields CHECK.
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

	status, out := ah.harness.httpJSON(t, "GET", "/instances/"+instID.String()+"/assets", nil)
	require.Equal(t, http.StatusOK, status, out)
	assets, _ := out["assets"].([]any)
	require.Len(t, assets, 1, "exactly the seeded durable/committed asset must surface")

	item := assets[0].(map[string]any)
	require.Equal(t, claimID.String(), item["claim_id"])
	require.Equal(t, "content", item["producer_name"])
	require.Equal(t, "committed", item["state"])
	require.Equal(t, "durable", item["lifetime"])
	require.Equal(t, "v-001", item["version_id"])
	// Alias resolves through the template: {node_type}.{claim_alias}.
	require.Equal(t, "producer.dataset", item["alias"])
	// Scope is surfaced; address is never leaked (blessed-invariant 20).
	scope, _ := item["scope"].(map[string]any)
	require.Equal(t, "north", scope["area"])
	require.NotContains(t, item, "address")
}

func TestAssetEndpoints_GetSingleAsset(t *testing.T) {
	t.Parallel()
	ah, teardown := newAssetHarness(t, nil)
	t.Cleanup(teardown)

	instID, claimID, _, _ := ah.seedAsset(t, "asset-get")

	status, out := ah.harness.httpJSON(t, "GET", "/instances/"+instID.String()+"/assets/producer.dataset", nil)
	require.Equal(t, http.StatusOK, status, out)
	require.Equal(t, claimID.String(), out["claim_id"])
	require.Equal(t, "producer.dataset", out["alias"])

	// A well-formed but unknown alias resolves to no row → 404.
	status, _ = ah.harness.httpJSON(t, "GET", "/instances/"+instID.String()+"/assets/producer.ghost", nil)
	require.Equal(t, http.StatusNotFound, status)

	// A malformed alias (no dot) → 400.
	status, _ = ah.harness.httpJSON(t, "GET", "/instances/"+instID.String()+"/assets/nodot", nil)
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

	status, out := ah.harness.httpJSON(t, "GET", "/instances/"+instID.String()+"/assets/producer.dataset/versions", nil)
	require.Equal(t, http.StatusOK, status, out)
	got, _ := out["versions"].([]any)
	require.Len(t, got, 2)
	v0 := got[0].(map[string]any)
	require.Equal(t, "v-001", v0["version_id"])
	v1 := got[1].(map[string]any)
	require.Equal(t, "v-002", v1["version_id"])

	// The proxy dialed the stub with the resolved claim handle id.
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

	// Two claim_terminal lineage rows keyed to the seeded claim handle.
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

	status, out := ah.harness.httpJSON(t, "GET", "/instances/"+instID.String()+"/assets/producer.dataset/materialization-history", nil)
	require.Equal(t, http.StatusOK, status, out)
	hist, _ := out["materialization_history"].([]any)
	require.Len(t, hist, 2, "both claim_terminal rows for this claim handle must join in")
	// GetByClaimHandleID is observed_at ASC.
	first := hist[0].(map[string]any)
	require.Equal(t, "claim_terminal", first["record_kind"])
}

// TestAssetEndpoints_DeleteReleasesAndDeletes drives the happy-path delete:
// no in-flight holder → ClaimProducer.Release fires, the row is deleted.
func TestAssetEndpoints_DeleteReleasesAndDeletes(t *testing.T) {
	t.Parallel()
	ah, teardown := newAssetHarness(t, nil)
	t.Cleanup(teardown)
	ctx := context.Background()

	instID, claimID, _, _ := ah.seedAsset(t, "asset-del")

	status, out := ah.harness.httpJSON(t, "DELETE", "/instances/"+instID.String()+"/assets/producer.dataset", nil)
	require.Equal(t, http.StatusOK, status, out)
	require.Equal(t, true, out["deleted"])

	// The claim handle row is gone.
	var remaining int
	pgtest.QueryRowForTest(ctx, t, ah.harness.driver,
		`SELECT count(*) FROM rimsky_claim_handles WHERE id = $1`,
		[]any{claimID}, &remaining)
	require.Equal(t, 0, remaining, "asset row must be deleted")

	// ClaimProducer.Release fired against the producer.
	var releaseFired bool
	for _, c := range ah.content.Calls() {
		if c.Verb == "release" && c.ClaimID == claimproducer.ClaimID(claimID.String()) {
			releaseFired = true
		}
	}
	require.True(t, releaseFired, "Release must fire on the producer before row delete")
}

// TestAssetEndpoints_DeleteAndMaterializeRefuseInFlight pins the
// 409-if-in-flight gate: an active claim_holder row makes both DELETE
// (in-flight holder) and POST /materialize (instance still active uses a
// different gate, so we assert the holder-driven delete refusal) return 409.
func TestAssetEndpoints_DeleteRefusesInFlightHolder(t *testing.T) {
	t.Parallel()
	ah, teardown := newAssetHarness(t, nil)
	t.Cleanup(teardown)
	ctx := context.Background()

	instID, claimID, producerNodeID, frameID := ah.seedAsset(t, "asset-del-busy")

	// An active node-run + an active claim_holder row → the delete gate
	// must refuse (409) and NOT call Release or delete the row.
	holderRunID := uuid.New()
	var mainScopeID shared.UUID
	pgtest.QueryRowForTest(ctx, t, ah.harness.driver,
		`SELECT main_run_scope_id FROM rimsky_instances WHERE id = $1`,
		[]any{instID}, &mainScopeID)
	pgtest.ExecForTest(ctx, t, ah.harness.driver, `
		INSERT INTO rimsky_node_runs
			(id, node_id, executor_name, required_stores, enqueued_at, phase, state, frame_id, run_scope_id)
		VALUES ($1, $2, 'worker', ARRAY[]::text[], now(), 'active', 'running', $3, $4)
	`, holderRunID, producerNodeID, frameID, mainScopeID)
	pgtest.ExecForTest(ctx, t, ah.harness.driver, `
		INSERT INTO rimsky_claim_holders (id, claim_handle_id, holder_run_id, state, frame_id)
		VALUES ($1, $2, $3, 'active', $4)
	`, uuid.New(), claimID, holderRunID, frameID)

	status, out := ah.harness.httpJSON(t, "DELETE", "/instances/"+instID.String()+"/assets/producer.dataset", nil)
	require.Equal(t, http.StatusConflict, status, out)
	require.EqualValues(t, 1, out["active_count"])

	// The row survives the refused delete.
	var remaining int
	pgtest.QueryRowForTest(ctx, t, ah.harness.driver,
		`SELECT count(*) FROM rimsky_claim_handles WHERE id = $1`,
		[]any{claimID}, &remaining)
	require.Equal(t, 1, remaining, "asset row must survive a refused delete")

	// Release must NOT have fired.
	for _, c := range ah.content.Calls() {
		require.NotEqual(t, "release", c.Verb, "Release must not fire when delete is refused")
	}
}

// TestAssetEndpoints_MaterializeEnqueuesInvalidate exercises the
// materialize endpoint (POST /materialize) which is an alias for an
// operator invalidate message, and its 409-if-terminated gate.
func TestAssetEndpoints_MaterializeEnqueuesInvalidate(t *testing.T) {
	t.Parallel()
	ah, teardown := newAssetHarness(t, nil)
	t.Cleanup(teardown)
	ctx := context.Background()

	instID, _, _, _ := ah.seedAsset(t, "asset-mat")

	status, out := ah.harness.httpJSON(t, "POST", "/instances/"+instID.String()+"/assets/producer.dataset/materialize",
		map[string]any{"reason": "operator-poke"})
	require.Equal(t, http.StatusCreated, status, out)
	msgID, _ := out["message_id"].(string)
	require.NotEmpty(t, msgID)

	// A message row landed targeting the producer node_type with kind=invalidate.
	var msgCount int
	pgtest.QueryRowForTest(ctx, t, ah.harness.driver,
		`SELECT count(*) FROM rimsky_messages WHERE instance_id = $1 AND kind = 'invalidate' AND target = 'producer'`,
		[]any{instID}, &msgCount)
	require.Equal(t, 1, msgCount)

	// Drive the instance terminal — materialize must then refuse with 409.
	pgtest.ExecForTest(ctx, t, ah.harness.driver,
		`UPDATE rimsky_instances SET terminated_at = now() WHERE id = $1`, instID)
	status, _ = ah.harness.httpJSON(t, "POST", "/instances/"+instID.String()+"/assets/producer.dataset/materialize",
		map[string]any{"reason": "too-late"})
	require.Equal(t, http.StatusConflict, status)
	_ = ctx
}
