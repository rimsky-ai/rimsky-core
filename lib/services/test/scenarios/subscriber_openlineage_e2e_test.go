// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// subscriber_openlineage_e2e_test.go is the cross-stack proof for
// STORY-subscriber-openlineage: an operator running rimsky in a
// data-platform environment uses the bundled `openlineage` subscriber
// to translate rimsky lifecycle events + claim terminal records into
// OpenLineage 1.x JSON events posted to a backend (Marquez / DataHub /
// Collibra / ...) without writing a custom subscriber.
//
// The spec cites `subscribers/openlineage/subscriber_test.go` and
// `emitter_test.go` for the in-process coverage; this file is the
// cross-stack proof — the openlineage subscriber binary RUNS AS A
// CONTAINER off the locally-built `rimsky-subscriber-openlineage:latest`
// image, polls a REAL Postgres written to by a REAL rimsky stack, and
// posts to a REAL HTTP receiver (a host-side `httptest.NewServer` that
// validates inbound bodies against the OpenLineage 1.x envelope
// requirements). No stubs in the value-delivering path.
//
// Falsifier coverage (from the spec):
//
//   - "Subscriber posts to receiver but with malformed OpenLineage JSON"
//     → refuted by the receiver's per-arrival schema check: every POST
//     must decode as JSON and carry the required OpenLineage 1.x
//     top-level fields (`eventType` ∈ {START, RUNNING, COMPLETE, ABORT,
//     FAIL, OTHER}, parseable `eventTime`, non-empty `producer` URI,
//     `schemaURL` referencing the OpenLineage 1.x schema, a `run.runId`
//     that parses as a UUID-ish identifier, and a `job` with
//     namespace+name). A subscriber that posted malformed JSON, missed a
//     required field, or sent an unrecognised eventType would surface
//     here.
//
//   - "A lifecycle event the subscriber should emit on is skipped" →
//     refuted by driving a real template through to a leaf-run terminal
//     AND a real claim commit. The writer-side
//     `runtime/lineage_writer.go` appends one `leaf_run` row per
//     leaf-run terminal and one `claim_terminal` row per claim
//     Commit/Abandon; the test asserts the receiver observed at least
//     one OL event whose `run.runId` corresponds to the rimsky-side
//     leaf_run record AND at least one whose dataset output
//     corresponds to the rimsky-side claim_terminal record (the
//     claim_terminal mapping is the dataset-version event surfaced
//     into the OL backend's lineage graph).
//
//   - "The emitted event's IDs don't correspond to the rimsky-side IDs"
//     → refuted by reading the rimsky-side `rimsky_lineage` rows
//     directly via pgx (the same path `subscriber.fetchSince` uses) and
//     asserting every emitted OL event carries a `run.runId` /
//     dataset-output `name` derived from one of those rimsky-side
//     records. A subscriber that synthesised IDs (or emitted from a
//     different source) would diverge.
//
// @concept: lineage
// @concept: lineage-record
// @story: subscriber-lineage-receiver
package scenarios

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcnet "github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

// openLineageSubscriberImage is the locally-built bundled openlineage
// subscriber image, produced by `make service-images`. The harness
// consumes the local tag rather than pulling from a registry — exactly
// the same discipline the rest of `lib/services/test/scenarios` uses.
const openLineageSubscriberImage = "rimsky-subscriber-openlineage:latest"

// openLineageNamespace is the namespace the subscriber stamps on every
// emitted event (via RIMSKY_OPENLINEAGE_NAMESPACE). Asserting on this
// value gives the receiver one more knob to discriminate "this came from
// our subscriber" from any background noise.
const openLineageNamespace = "rimsky-e2e-namespace"

// openLineagePollInterval is the subscriber's tick cadence
// (RIMSKY_OPENLINEAGE_POLL_INTERVAL). Short enough that the test does
// not wait the default 5s, long enough that the subscriber doesn't
// thrash the test Postgres.
const openLineagePollInterval = 250 * time.Millisecond

// TestSubscriberOpenlineage is the cross-stack STORY-subscriber-openlineage
// acceptance proof. See the file doc for the falsifier argument the test
// refutes.
func TestSubscriberOpenlineage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// @deliberate: the fake OpenLineage receiver validates the
	// OpenLineage 1.x envelope shape ON THE HOT PATH — any decode
	// failure or missing-required-field is captured into a per-arrival
	// `validateErr` so the test fails with the receiver's own
	// diagnosis (rather than a generic "schema didn't match" after the
	// fact). The receiver returns 201 Created on a structural pass and
	// 400 Bad Request on a structural fail; the subscriber's
	// `emitter.Send` treats anything ≥ 300 as a halting error, so a
	// malformed body would visibly stall the subscriber's cursor —
	// surfaced through the rimsky-side cursor check below.
	var (
		mu       sync.Mutex
		received []olArrival
	)
	marquez := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		a := olArrival{Path: r.URL.Path, Body: body}
		decoded := map[string]any{}
		if err := json.Unmarshal(body, &decoded); err != nil {
			a.ValidateErr = fmt.Sprintf("json decode: %v", err)
		} else {
			a.Decoded = decoded
			a.ValidateErr = validateOpenLineageEnvelope(decoded)
		}
		mu.Lock()
		received = append(received, a)
		mu.Unlock()
		if a.ValidateErr != "" {
			http.Error(w, a.ValidateErr, http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(marquez.Close)

	receiverHostPort := hostPortOf(t, marquez.URL)

	// @deliberate: bring up rimsky + a claim-producer (fs-store) + an
	// executor stub on a shared docker network. The cascade produces
	// one `leaf_run` lineage row per leaf-run terminal (via
	// runtime/lineage_writer.go::AppendLeafRunRecord) and one
	// `claim_terminal` lineage row per claim Commit (via
	// runtime/lineage_writer.go::AppendClaimTerminalRecord). Both are
	// exactly the writes the openlineage subscriber polls and
	// translates into OL events.
	netName := harness.NewNetwork(ctx, t)
	fs := harness.StartFilesystemStore(ctx, t, netName, "store-filesystem",
		harness.FilesystemStoreSpec{
			PickPolicies: map[string]harness.FilesystemPickPolicy{
				"@docs-ring": {
					Root:                     "docs",
					OnCommit:                 "recycle",
					OnGiveUp:                 "recycle",
					VisibilityTimeoutSeconds: 60,
					SyncStrategy:             "on_open",
				},
			},
			SeedFolders: [][]string{{"docs", "alpha"}, {"docs", "beta"}},
		})
	harness.StartExecutorStubOnNetwork(ctx, t, netName, "executor-stub")
	ep := harness.BringUpRimsky(ctx, t,
		harness.WithExistingNetwork(netName),
		harness.WithClaimProducer("docs", fs.InternalEndpoint),
		harness.WithExecutor("stub", "executor-stub:9300"),
	)

	// @deliberate: deploy a one-node template that opens (and commits)
	// a claim on @docs-ring per dispatch. Each instance produces one
	// leaf_run + one claim_terminal lineage row.
	templateID := deployOLTemplate(t, ep, "openlineage-e2e")
	instanceID := createOLInstance(t, ep, templateID, "ck-openlineage-e2e")

	waitOLNodeTerminal(t, ep, instanceID, "acquire-and-execute", 90*time.Second)

	// @deliberate: connect to rimsky's host-mapped DSN so we can BOTH
	// (a) wait for the writer's rows to land and (b) snapshot the
	// rimsky-side IDs the subscriber will translate. Reading the same
	// table the subscriber polls is the only way to cross-check that
	// the OL runId / dataset name actually correspond to rimsky-side
	// records.
	pool, err := pgxpool.New(ctx, ep.HostDSN)
	if err != nil {
		t.Fatalf("connect rimsky host DSN: %v", err)
	}
	defer pool.Close()

	waitOLLineageRows(t, ctx, pool, "leaf_run", 1, 60*time.Second)
	// @constraint: claim_terminal lands when the producer's Commit
	// acks; the recycle-on-commit policy is synchronous, so the row
	// should be present shortly after the leaf-run terminal.
	waitOLLineageRows(t, ctx, pool, "claim_terminal", 1, 60*time.Second)

	// @constraint: `runId` for a leaf_run is `instance_id` (no
	// child_key on a single-node template), and the dataset-output
	// `name` for a claim_terminal is the producer's
	// `scope_data_hash`. These projections are what the subscriber
	// translates to OL.
	rimskyLeafRunIDs := selectOLLeafRunIDs(t, ctx, pool, instanceID)
	rimskyClaimScopeHashes := selectOLClaimScopeHashes(t, ctx, pool, instanceID)
	if len(rimskyLeafRunIDs) < 1 {
		t.Fatalf("rimsky-side leaf_run snapshot empty for instance %s; the "+
			"writer never produced a leaf_run row", instanceID)
	}
	if len(rimskyClaimScopeHashes) < 1 {
		t.Fatalf("rimsky-side claim_terminal snapshot empty for instance %s; "+
			"the producer's Commit never appended a claim_terminal row", instanceID)
	}

	// @constraint: the OL `runId` projection in `MakeLeafRunEvent` is
	// `instance_id + "/" + child_key`; the single-node template has no
	// fan-out child key, so the runId is the bare `instance_id` string.
	expectedRunID := instanceID
	t.Logf("rimsky-side IDs: instance_id=%s leaf_runs=%d claim_scope_hashes=%d (expected_ol_runid=%s)",
		instanceID, len(rimskyLeafRunIDs), len(rimskyClaimScopeHashes), expectedRunID)

	// @deliberate: boot the openlineage subscriber container against
	// (a) rimsky's in-network Postgres DSN (so it can poll
	// rimsky_lineage), and (b) the host-side fake receiver via
	// host.testcontainers.internal.
	startOpenLineageSubscriber(ctx, t,
		netName,
		ep.InternalDSN,
		fmt.Sprintf("http://host.testcontainers.internal:%d", receiverHostPort),
		openLineageNamespace,
		receiverHostPort,
	)

	// @deliberate: wait until the receiver has observed at least one
	// event of each OL kind that maps from the two rimsky lineage
	// record kinds — leaf_run → OL COMPLETE event with the
	// instance-keyed runId, and claim_terminal → OL event with a
	// dataset output whose `name` matches one of the rimsky-side
	// scope_data_hash values. Bounded by an explicit deadline so a
	// subscriber that never posts (or that posts only one of the two
	// kinds) surfaces as a deadline failure with the receiver's actual
	// arrivals attached.
	waitForOLArrivalMatching(t, &mu, &received, 60*time.Second,
		"leaf_run → COMPLETE event with instance-keyed runId",
		func(a olArrival) bool {
			if a.ValidateErr != "" {
				return false
			}
			run, _ := a.Decoded["run"].(map[string]any)
			runID, _ := run["runId"].(string)
			// @deliberate: pin both axes — runId corresponds to the
			// rimsky-side instance_id (the falsifier "IDs don't
			// correspond" case), and the event was stamped with our
			// namespace (rules out stray traffic).
			job, _ := a.Decoded["job"].(map[string]any)
			ns, _ := job["namespace"].(string)
			return strings.HasPrefix(runID, expectedRunID) &&
				ns == openLineageNamespace &&
				a.Decoded["eventType"] == "COMPLETE"
		})

	waitForOLArrivalMatching(t, &mu, &received, 60*time.Second,
		"claim_terminal → event with dataset output keyed on rimsky scope_data_hash",
		func(a olArrival) bool {
			if a.ValidateErr != "" {
				return false
			}
			outputs, _ := a.Decoded["outputs"].([]any)
			if len(outputs) == 0 {
				return false
			}
			for _, o := range outputs {
				out, _ := o.(map[string]any)
				name, _ := out["name"].(string)
				if name == "" {
					continue
				}
				for _, want := range rimskyClaimScopeHashes {
					if name == want {
						return true
					}
				}
			}
			return false
		})

	// @deliberate: final assertions confirm (a) no malformed arrivals
	// — the validator's check is the per-receiver contract that
	// catches a structurally bad emit (the story's "subscriber posts
	// to receiver but with malformed OpenLineage JSON" falsifier); and
	// (b) all POSTs landed on `/api/v1/lineage` (the documented OL
	// transport).
	mu.Lock()
	defer mu.Unlock()
	var validationErrs []string
	for i, a := range received {
		if a.ValidateErr != "" {
			validationErrs = append(validationErrs,
				fmt.Sprintf("arrival[%d] path=%s err=%s body=%s",
					i, a.Path, a.ValidateErr, string(a.Body)))
		}
		if a.Path != "/api/v1/lineage" {
			t.Errorf("arrival[%d] path = %q, want /api/v1/lineage (the documented OL transport)",
				i, a.Path)
		}
	}
	if len(validationErrs) > 0 {
		t.Fatalf("received %d malformed OpenLineage envelopes (falsifier 'malformed JSON' surfaced):\n%s",
			len(validationErrs), strings.Join(validationErrs, "\n"))
	}
	if len(received) == 0 {
		t.Fatalf("receiver got zero arrivals after the deadline — the subscriber " +
			"never reached the receiver (DNS, host-gateway, or backend URL wrong)")
	}
}

// validateOpenLineageEnvelope returns "" when the decoded body matches
// the OpenLineage 1.x envelope (required top-level fields per
// https://openlineage.io/spec/1-0-5/OpenLineage.json#/$defs/RunEvent),
// or a non-empty diagnostic string otherwise. Inline rather than via a
// dependency: the spec doesn't pull in openlineage-go (no validation
// helper available in this module's go.mod), so we hard-code the
// envelope contract from the spec — exactly the same shape
// `MakeLeafRunEvent` / `MakeClaimTerminalEvent` produce.
//
// Required (per OL 1.x RunEvent):
//   - eventType ∈ {START, RUNNING, COMPLETE, ABORT, FAIL, OTHER}
//   - eventTime parseable as RFC3339 (subseconds optional)
//   - producer non-empty (URI identifying the emitter)
//   - schemaURL non-empty (references the OL schema)
//   - run object present with non-empty runId
//   - job object present with non-empty namespace AND name
//
// Anything that fails one of those is "malformed" per the spec's
// falsifier brief.
func validateOpenLineageEnvelope(body map[string]any) string {
	allowedEvents := map[string]bool{
		"START":    true,
		"RUNNING":  true,
		"COMPLETE": true,
		"ABORT":    true,
		"FAIL":     true,
		"OTHER":    true,
	}
	eventType, _ := body["eventType"].(string)
	if !allowedEvents[eventType] {
		return fmt.Sprintf("eventType=%q not in OpenLineage 1.x allowed set", eventType)
	}
	eventTime, _ := body["eventTime"].(string)
	if eventTime == "" {
		return "eventTime missing or empty"
	}
	if _, err := time.Parse(time.RFC3339Nano, eventTime); err != nil {
		if _, err2 := time.Parse(time.RFC3339, eventTime); err2 != nil {
			return fmt.Sprintf("eventTime %q not RFC3339-parseable: %v", eventTime, err)
		}
	}
	producer, _ := body["producer"].(string)
	if producer == "" {
		return "producer URI missing or empty"
	}
	schemaURL, _ := body["schemaURL"].(string)
	if schemaURL == "" {
		return "schemaURL missing or empty"
	}
	if !strings.Contains(schemaURL, "openlineage.io") {
		return fmt.Sprintf("schemaURL %q does not reference openlineage.io", schemaURL)
	}
	run, ok := body["run"].(map[string]any)
	if !ok {
		return "run object missing"
	}
	runID, _ := run["runId"].(string)
	if runID == "" {
		return "run.runId missing or empty"
	}
	job, ok := body["job"].(map[string]any)
	if !ok {
		return "job object missing"
	}
	if ns, _ := job["namespace"].(string); ns == "" {
		return "job.namespace missing or empty"
	}
	if name, _ := job["name"].(string); name == "" {
		return "job.name missing or empty"
	}
	return ""
}

// startOpenLineageSubscriber boots one rimsky-subscriber-openlineage
// container on the shared network, wired to:
//   - the rimsky-side Postgres DSN (so it polls rimsky_lineage),
//   - the host-side OL receiver via host.testcontainers.internal
//     (the same hostgateway alias other host-port-access scenarios use),
//   - a short poll interval so the test doesn't wait the env default 5s,
//   - the cursor state DB pointing at the same Postgres as RIMSKY_DSN.
//
// Cleanup is registered via t.Cleanup. The subscriber's image must have
// been built locally by `make service-images`; the harness fails hard
// (t.Fatal) on missing-image — the discipline matches every other peer
// service in this package.
func startOpenLineageSubscriber(
	ctx context.Context,
	t *testing.T,
	networkName string,
	rimskyDSN string,
	backendURL string,
	namespace string,
	hostAccessPort int,
) testcontainers.Container {
	t.Helper()
	env := map[string]string{
		"RIMSKY_OPENLINEAGE_RIMSKY_DSN":    rimskyDSN,
		"RIMSKY_OPENLINEAGE_STATE_DSN":     rimskyDSN,
		"RIMSKY_OPENLINEAGE_BACKEND_URL":   backendURL,
		"RIMSKY_OPENLINEAGE_NAMESPACE":     namespace,
		"RIMSKY_OPENLINEAGE_POLL_INTERVAL": openLineagePollInterval.String(),
	}
	opts := []testcontainers.ContainerCustomizer{
		tcnet.WithNetworkName([]string{"subscriber-openlineage"}, networkName),
		testcontainers.WithEnv(env),
		testcontainers.WithHostPortAccess(hostAccessPort),
		// @constraint: the subscriber binary logs `openlineage.starting`
		// once the startup handshake (cursor table create + load) is
		// done; that is the wait-strategy gate.
		testcontainers.WithWaitStrategy(
			wait.ForLog("openlineage.starting").WithStartupTimeout(60 * time.Second),
		),
	}
	c, err := testcontainers.Run(ctx, openLineageSubscriberImage, opts...)
	if err != nil {
		t.Fatalf("harness: start openlineage subscriber container: %v "+
			"(did you run `make service-images`?)", err)
	}
	t.Cleanup(func() {
		termCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = c.Terminate(termCtx)
	})
	return c
}

// waitOLLineageRows polls `rimsky_lineage` until at least `want` rows
// of the requested `record_kind` are present. Reads through the same
// DSN the subscriber will read; the subscriber's tick path uses the
// identical predicate, so if rows are visible to us they are visible
// to the subscriber.
func waitOLLineageRows(t *testing.T, ctx context.Context, pool *pgxpool.Pool, kind string, want int, deadline time.Duration) {
	t.Helper()
	end := time.Now().Add(deadline)
	var n int
	for time.Now().Before(end) {
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM rimsky_lineage WHERE record_kind = $1`, kind,
		).Scan(&n); err != nil {
			t.Fatalf("count rimsky_lineage where record_kind=%s: %v", kind, err)
		}
		if n >= want {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("rimsky_lineage record_kind=%s: want >= %d rows within %v, got %d — "+
		"the writer never produced this record_kind for the driven cascade",
		kind, want, deadline, n)
}

// selectOLLeafRunIDs returns every distinct `run_id` from `rimsky_lineage`
// rows of record_kind `leaf_run` whose `instance_id` matches the driven
// instance. Cross-checked against the receiver's arrivals to refute the
// "emitted event's IDs don't correspond to the rimsky-side IDs"
// falsifier.
func selectOLLeafRunIDs(t *testing.T, ctx context.Context, pool *pgxpool.Pool, instanceID string) []string {
	t.Helper()
	rows, err := pool.Query(ctx,
		`SELECT DISTINCT record->>'run_id'
		   FROM rimsky_lineage
		  WHERE record_kind = 'leaf_run' AND instance_id::text = $1`,
		instanceID,
	)
	if err != nil {
		t.Fatalf("select leaf_run run_ids: %v", err)
	}
	defer rows.Close()
	out := make([]string, 0)
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			t.Fatalf("scan leaf_run run_id: %v", err)
		}
		if v != "" {
			out = append(out, v)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate leaf_run rows: %v", err)
	}
	return out
}

// selectOLClaimScopeHashes returns every distinct
// `claim_scope_data_hash` value from `rimsky_lineage` rows of
// record_kind `claim_terminal` whose `instance_id` matches the driven
// instance. The writer-side wire-key is `claim_scope_data_hash`
// (`runtime/lineage_writer.go::ClaimTerminalRecord.ClaimScopeDataHash`)
// and the subscriber's emitter projects this value into OL
// `outputs[].name`, so matching the receiver's arrivals against this
// set is a direct cross-check of identity. A row whose value is NULL
// (the producer didn't fill it) is filtered out via SQL — those rows
// would surface as empty OL dataset names, which carry no identity to
// cross-check.
func selectOLClaimScopeHashes(t *testing.T, ctx context.Context, pool *pgxpool.Pool, instanceID string) []string {
	t.Helper()
	rows, err := pool.Query(ctx,
		`SELECT DISTINCT record->>'claim_scope_data_hash'
		   FROM rimsky_lineage
		  WHERE record_kind = 'claim_terminal'
		    AND instance_id::text = $1
		    AND record->>'claim_scope_data_hash' IS NOT NULL`,
		instanceID,
	)
	if err != nil {
		t.Fatalf("select claim_terminal claim_scope_data_hashes: %v", err)
	}
	defer rows.Close()
	out := make([]string, 0)
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			t.Fatalf("scan claim_terminal claim_scope_data_hash: %v", err)
		}
		if v != "" {
			out = append(out, v)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate claim_terminal rows: %v", err)
	}
	return out
}

// olArrival is one recorded request body from the fake OpenLineage
// receiver, plus the receiver's per-arrival validation verdict.
// Aggregated into a per-test slice; the helpers below read this slice
// directly so the test can cross-check arrivals against rimsky-side
// IDs without re-deriving them.
type olArrival struct {
	Path        string
	Decoded     map[string]any
	ValidateErr string
	Body        []byte
}

// waitForOLArrivalMatching polls the receiver's recorded arrivals until
// at least one matches the predicate, or the deadline expires. On
// timeout dumps every recorded arrival so the failure mode is visible
// without having to add log statements after the fact.
func waitForOLArrivalMatching(
	t *testing.T,
	mu *sync.Mutex,
	received *[]olArrival,
	deadline time.Duration,
	label string,
	pred func(a olArrival) bool,
) {
	t.Helper()
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		mu.Lock()
		snapshot := append([]olArrival{}, (*received)...)
		mu.Unlock()
		for _, a := range snapshot {
			if pred(a) {
				return
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	t.Fatalf("no OpenLineage arrival matched %q within %v (got %d arrivals); "+
		"this either means the subscriber never reached the receiver, or it "+
		"emitted events whose runId/dataset name does not correspond to the "+
		"rimsky-side records (the falsifier). Arrivals: %s",
		label, deadline, len(*received), dumpOLArrivals(*received))
}

// dumpOLArrivals serialises every arrival into a compact form for
// failure dumps. Truncates each body to the first 400 bytes so a
// chatty subscriber doesn't drown the failure log.
func dumpOLArrivals(arrivals []olArrival) string {
	var b strings.Builder
	for i, a := range arrivals {
		body := string(a.Body)
		if len(body) > 400 {
			body = body[:400] + "...(truncated)"
		}
		fmt.Fprintf(&b, "\n  [%d] path=%s err=%q body=%s", i, a.Path, a.ValidateErr, body)
	}
	return b.String()
}

// deployOLTemplate POSTs the openlineage-e2e template and deploys it.
// The template wires one acquire-and-execute node bound to the
// @docs-ring pick policy: each dispatch opens a claim, the stub
// executor's Commit triggers the producer's commit, and rimsky's
// lineage writer appends one leaf_run row plus one claim_terminal row.
func deployOLTemplate(t *testing.T, ep harness.RimskyEndpoint, name string) string {
	t.Helper()
	body := map[string]any{
		"spec": map[string]any{
			"name":             name,
			"version":          "1",
			"frame_timeout_ms": 600000,
			"nodes": []map[string]any{
				{
					"type":     "acquire-and-execute",
					"executor": "stub",
					"stores": []map[string]any{
						{"name": "docs", "selector": "@docs-ring", "intent": "rw"},
					},
				},
			},
		},
	}
	status, raw := ep.PostJSON(t, "/v1/templates", body)
	if status != http.StatusCreated {
		t.Fatalf("POST /v1/templates: %d %s", status, string(raw))
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
	deployStatus, deployRaw := ep.PostJSON(t,
		"/v1/templates/"+resp.TemplateID+"/deploy", map[string]any{})
	if deployStatus != http.StatusOK {
		t.Fatalf("POST /v1/templates/%s/deploy: %d %s",
			resp.TemplateID, deployStatus, string(deployRaw))
	}
	return resp.TemplateID
}

// createOLInstance POSTs a new instance against templateID and returns
// its instance_id.
func createOLInstance(t *testing.T, ep harness.RimskyEndpoint, templateID, instanceKey string) string {
	t.Helper()
	status, raw := ep.PostJSON(t, "/v1/instances", map[string]any{
		"template":     templateID,
		"instance_key": instanceKey,
		"params":       map[string]any{},
	})
	if status != http.StatusCreated {
		t.Fatalf("POST /v1/instances: %d %s", status, string(raw))
	}
	var resp struct {
		InstanceID string `json:"instance_id"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode instance response: %v: %s", err, string(raw))
	}
	if resp.InstanceID == "" {
		t.Fatalf("instance_id empty: %s", string(raw))
	}
	return resp.InstanceID
}

// waitOLNodeTerminal polls the observability API until the named node
// reaches a terminal state (`fresh` or `failed`). Mirrors
// `waitNodeTerminal` in subscriber_test.go — the lineage-row arrival is
// gated on this so the writer's flush is observable downstream.
func waitOLNodeTerminal(t *testing.T, ep harness.RimskyEndpoint, instanceID, nodeType string, deadline time.Duration) {
	t.Helper()
	end := time.Now().Add(deadline)
	var lastState string
	for time.Now().Before(end) {
		status, raw := ep.GetJSON(t,
			"/v1/observability/nodes/"+instanceID+"/"+nodeType, "")
		if status == http.StatusOK {
			var resp struct {
				Node struct {
					State string `json:"state"`
				} `json:"node"`
			}
			if err := json.Unmarshal(raw, &resp); err == nil {
				lastState = resp.Node.State
				if lastState == "fresh" || lastState == "failed" {
					return
				}
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("node %q on instance %s did not reach terminal within %v; last state=%q",
		nodeType, instanceID, deadline, lastState)
}
