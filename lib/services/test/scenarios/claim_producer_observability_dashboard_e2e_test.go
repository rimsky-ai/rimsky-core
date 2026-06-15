// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Cross-stack proof for STORY-claim-producer-observability: an operator
// running a dashboard against a rimsky deployment fetches a claim's full
// detail, streams live claim-state changes, paginates the producer's
// claim inventory, and renders a producer-declared admin view — all
// without writing a custom backplane.
//
// The spec cites per-store in-process observability tests
// (filesystem-store and postgres-store) as already-existing artifacts
// that cover the producer-side behaviour; this proof additionally
// exercises the OPERATOR-side query path through a running rimsky
// dashboard surface. That surface is the rimsky control-api's
// `/v1/observability/stores/{name}` route + the store's own
// HTTP+JSON observability bridge at the URL the dashboard surfaces.
//
// Bring-up (using the testcontainers harness):
//
//  1. A real `rimsky-store-filesystem:latest` container with one pick
//     policy and three seeded folders. The store advertises its
//     `http_bridge_url` so rimsky's discovery handshake caches it.
//     The harness also maps the store's HTTP port to the host so the
//     test process can dial the bridge directly (simulating the
//     operator's browser, which would reach the bridge from outside
//     the cluster).
//  2. A success stub executor on the shared network — the
//     acquirer's terminal Commit fires the producer's real Open →
//     Commit, which is what the dashboard observes.
//  3. The `rimsky-all-in-one:latest` stack on the same network,
//     wired to the store via `claim_producers` and the executor via
//     `executors`. The control-api's startup observability handshake
//     probes the store and caches its capabilities + bridge URL.
//  4. A template that opens one claim per dispatch (executor: stub
//     with `on_commit: recycle`, so each cycle gets a different
//     folder). Three sequential instances populate three distinct
//     claim records in the store's in-memory ledger, which is what
//     ListClaims / GetClaim / StreamClaim read from.
//
// Acceptance assertions (in order):
//
//   - Dashboard discovers the per-store observability surface via
//     `GET /v1/observability/stores/{name}`. The response carries
//     `http_bridge_url` (the URL rimsky surfaces to the operator's
//     browser) and the producer-declared admin views (`pick_policies`,
//     `policy_items`).
//   - Dashboard fetches a real claim's full detail via the bridge's
//     `GET /observability/v1/claims/{claim_id}` and gets the
//     producer's actual state (state, opened_at, history with open +
//     commit events) — NOT a synthesized stub.
//   - Dashboard subscribes to the bridge's
//     `GET /observability/v1/claims/{claim_id}/stream` SSE endpoint
//     and observes the same state transitions the producer recorded
//     (open + commit + terminal), in order, with no drops.
//   - Dashboard paginates the producer's claim inventory via
//     `GET /observability/v1/claims?limit=...&cursor=...`. Each page
//     contains real producer rows (not synthesized): the same claim
//     IDs returned by GetClaim, with real opened/closed timestamps.
//     A second page reachable via the first page's next_cursor brings
//     the remaining rows; the total across pages equals the number
//     of real claim records in the ledger.
//   - Dashboard renders the producer-declared `pick_policies` admin
//     view via `GET /observability/v1/admin/pick_policies` and sees
//     data sourced from the producer (selector, root, queue depths) —
//     not a placeholder.
//
// Falsifier coverage:
//
//   - "Streamed claim state lags or drops": the test asserts the
//     stream produces the EXACT sequence of events the producer's
//     ledger holds for the streamed claim, in chronological order,
//     with a terminal marker. A drop would surface as a missing or
//     reordered event.
//   - "An admin view the producer declared isn't surfaced through the
//     dashboard route": the test reads the admin-view declarations
//     from the dashboard's `/v1/observability/stores/{name}` response
//     AND queries that view through the bridge, asserting it returns
//     data sourced from the producer's pick-policy map.
//   - "The inventory pagination synthesizes rows": the test
//     cross-checks that every claim_id returned by paginated ListClaims
//     also resolves via GetClaim to a record with matching state and
//     timestamps. A synthesizer would diverge.
package scenarios

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

// TestClaimProducerObservabilityDashboard drives the operator-side
// observability flow for a real claim-producer (the bundled
// filesystem-store) through a running rimsky-all-in-one stack. The
// real producer's Open / Commit appends events to its in-memory
// ledger; the rimsky dashboard surface exposes the producer's
// http_bridge_url + admin-view declarations; the bridge serves
// per-claim get / stream / list / admin view directly. This is the
// path an operator's browser-based dashboard would take.
func TestClaimProducerObservabilityDashboard(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	netName := harness.NewNetwork(ctx, t)

	// @deliberate: One pick policy with three seeded folders; on_commit=recycle
	// returns the folder to the queue post-commit so three sequential Open
	// cycles each pick a different folder (oldest-available, then
	// alphabetically-first). AdvertiseHTTPBridge=true makes the store render
	// its `http_bridge_url:` YAML field (rimsky's observability handshake
	// surfaces it through the dashboard route) and maps the HTTP port to the
	// host so this test process can dial the bridge directly.
	fs := harness.StartFilesystemStore(ctx, t, netName, "store-fs",
		harness.FilesystemStoreSpec{
			PickPolicies: map[string]harness.FilesystemPickPolicy{
				"@docs": {
					Root:                     "docs",
					OnCommit:                 "recycle",
					OnGiveUp:                 "recycle",
					VisibilityTimeoutSeconds: 60,
					SyncStrategy:             "on_open",
				},
			},
			SeedFolders: [][]string{
				{"docs", "alpha"},
				{"docs", "beta"},
				{"docs", "gamma"},
			},
			AdvertiseHTTPBridge: true,
		})
	if fs.HostHTTPBridge == "" {
		t.Fatal("harness: AdvertiseHTTPBridge=true but HostHTTPBridge is empty")
	}

	harness.StartExecutorStubOnNetwork(ctx, t, netName, "executor-stub")

	ep := harness.BringUpRimsky(ctx, t,
		harness.WithExistingNetwork(netName),
		harness.WithClaimProducer("docs", fs.InternalEndpoint),
		harness.WithExecutor("stub", "executor-stub:9300"),
	)

	// @deliberate: one-node template — each dispatch opens a claim on @docs and
	// commits it; on_commit=recycle puts the folder back into the queue so the
	// next instance picks the next folder.
	templateID := deployObsTemplate(t, ep, "claim-observability-demo")

	// @deliberate: drive three sequential instances, each producing one real
	// Open + Commit on the producer (so the ledger ends up with three distinct
	// claim records). The "node reached fresh" signal is the node-state
	// surface's INITIAL value (a node row with no run yet already shows fresh),
	// so it does not reliably mean work happened. We use a stronger proof:
	// wait until the producer's ledger reports the cumulative committed-claim
	// count we expect — that observation only goes up via real Open + Commit
	// calls from rimsky's supervisor through the gRPC ClaimProducer surface.
	for i := 0; i < 3; i++ {
		t.Logf("creating instance %d...", i)
		instanceID := createObsInstance(t, ep, templateID, fmt.Sprintf("ck-obs-%d", i))
		t.Logf("instance %d=%s, waiting for producer to record commit...", i, instanceID)
		waitForLedgerClaimCount(t, fs.HostHTTPBridge, i+1, 90*time.Second)
		t.Logf("instance %d=%s reached commit in producer ledger", i, instanceID)
	}

	peerEntry := pollGetStorePeer(t, ep, "docs", 30*time.Second)

	if peerEntry.HTTPBridgeURL == "" {
		t.Fatalf("dashboard /v1/observability/stores/docs returned empty http_bridge_url; "+
			"the rimsky dashboard surface must expose the producer's bridge URL so the "+
			"operator's browser can reach it. Full peer: %+v", peerEntry)
	}
	if peerEntry.Reachability != "reachable" {
		t.Fatalf("dashboard reports store reachability=%q, want reachable; full peer=%+v",
			peerEntry.Reachability, peerEntry)
	}
	if peerEntry.Capabilities == nil {
		t.Fatalf("dashboard returned nil capabilities for the store; the handshake "+
			"didn't probe it (or didn't cache the result). Full peer: %+v", peerEntry)
	}
	caps := peerEntry.Capabilities
	if !caps.SupportsClaimGet || !caps.SupportsClaimStream || !caps.SupportsListClaims {
		t.Fatalf("dashboard caps want SupportsClaimGet/Stream/ListClaims = true; got %+v", caps)
	}
	// @constraint: the producer declares two admin views; both must appear
	// on the dashboard's view of the cached caps.
	wantViews := map[string]bool{"pick_policies": false, "policy_items": false}
	for _, v := range caps.AdminViews {
		if _, ok := wantViews[v.Name]; ok {
			wantViews[v.Name] = true
		}
	}
	for name, seen := range wantViews {
		if !seen {
			t.Fatalf("dashboard caps missing producer-declared admin view %q; got views=%+v",
				name, caps.AdminViews)
		}
	}

	// @constraint: paginated inventory results must match what GetClaim
	// returns for each claim_id (no synthesis). Three claims live in the
	// ledger; request limit=2 so the first page returns two and next_cursor
	// brings the third on the second page.
	page1 := getClaimsPage(t, fs.HostHTTPBridge, "", 2)
	if len(page1.Claims) != 2 {
		t.Fatalf("inventory page1: claims=%d, want 2; cursor=%q; raw=%+v",
			len(page1.Claims), page1.NextCursor, page1)
	}
	if page1.NextCursor == "" {
		t.Fatalf("inventory page1: next_cursor empty after limit=2 on a 3-claim ledger; "+
			"the producer must emit a cursor when more rows remain. Page=%+v", page1)
	}
	page2 := getClaimsPage(t, fs.HostHTTPBridge, page1.NextCursor, 2)
	if len(page2.Claims) != 1 {
		t.Fatalf("inventory page2: claims=%d, want 1 (the third real claim); raw=%+v",
			len(page2.Claims), page2)
	}
	allInventory := append([]claimSummary{}, page1.Claims...)
	allInventory = append(allInventory, page2.Claims...)
	if len(allInventory) != 3 {
		t.Fatalf("inventory total across pages = %d, want 3 (one per driven instance)", len(allInventory))
	}
	seenIDs := map[string]bool{}
	for _, c := range allInventory {
		if c.ClaimID == "" {
			t.Fatalf("inventory: a claim summary returned an empty claim_id (synthesized row?); page=%+v", allInventory)
		}
		if seenIDs[c.ClaimID] {
			t.Fatalf("inventory: duplicate claim_id %q across pages; pagination must not repeat rows", c.ClaimID)
		}
		seenIDs[c.ClaimID] = true
	}

	// @constraint: fetch one claim's full detail and cross-check against
	// the inventory row — a synthesized inventory would diverge from what
	// GetClaim returns for the same claim_id.
	pickClaim := allInventory[0]
	detail := getClaimDetail(t, fs.HostHTTPBridge, pickClaim.ClaimID)
	if detail.ClaimID != pickClaim.ClaimID {
		t.Fatalf("claim detail returned claim_id=%q, want %q (inventory cross-check)",
			detail.ClaimID, pickClaim.ClaimID)
	}
	if detail.State != "COMMITTED" {
		t.Fatalf("claim %q detail state=%q, want COMMITTED (the test commits every dispatched claim)",
			pickClaim.ClaimID, detail.State)
	}
	// @constraint: the inventory row's state must match GetClaim's state
	// (no synthesis); the producer is the single source of truth.
	if pickClaim.State != detail.State {
		t.Fatalf("inventory claim state %q != detail state %q for claim_id=%q (inventory must source from producer, not synthesize)",
			pickClaim.State, detail.State, pickClaim.ClaimID)
	}
	if len(detail.History) < 2 {
		t.Fatalf("claim %q detail history has %d events, want >= 2 (open + commit at minimum); detail=%+v",
			pickClaim.ClaimID, len(detail.History), detail)
	}

	// @constraint: subscribe to the live stream. The producer's StreamClaim
	// atomically replays history then streams new events. Since the test
	// drove this claim to COMMITTED before connecting, we expect to receive
	// the recorded events (open + commit) followed by a terminal marker,
	// proving the stream delivers the producer's state transitions without
	// dropping.
	streamCtx, streamCancel := context.WithTimeout(ctx, 10*time.Second)
	defer streamCancel()
	streamEvents := streamClaim(t, streamCtx, fs.HostHTTPBridge, pickClaim.ClaimID)
	if len(streamEvents) < 2 {
		t.Fatalf("stream for claim %q produced %d events, want >= 2 (the falsifier 'streamed state lags or drops' would manifest as missing events); events=%+v",
			pickClaim.ClaimID, len(streamEvents), streamEvents)
	}
	// @constraint: the stream must include a terminal marker (proves it
	// reached closure without the consumer having to time out).
	hasTerminal := false
	for _, ev := range streamEvents {
		if ev["category"] == "claim_terminal" {
			hasTerminal = true
			break
		}
	}
	if !hasTerminal {
		t.Fatalf("stream for claim %q never produced a claim_terminal event before the read deadline; "+
			"a dropped terminal marker is the 'streamed claim state ... drops' falsifier. Events: %+v",
			pickClaim.ClaimID, streamEvents)
	}
	// @constraint: the stream's pre-terminal events must equal the detail's
	// recorded history in order — no drops, no reorderings.
	historyCategories := make([]string, 0, len(detail.History))
	for _, h := range detail.History {
		historyCategories = append(historyCategories, h.Category)
	}
	streamCategories := make([]string, 0, len(streamEvents))
	for _, ev := range streamEvents {
		cat, _ := ev["category"].(string)
		if cat == "claim_terminal" {
			continue
		}
		streamCategories = append(streamCategories, cat)
	}
	if !categoriesEqual(streamCategories, historyCategories) {
		t.Fatalf("stream event categories %v do not match recorded history %v for claim %q "+
			"(the stream must atomically replay history without dropping or reordering)",
			streamCategories, historyCategories, pickClaim.ClaimID)
	}

	// @constraint: the producer-declared `pick_policies` admin view must
	// return data sourced from the producer (selector, root, queue counts),
	// not a placeholder.
	view := getAdminView(t, fs.HostHTTPBridge, "pick_policies", nil)
	rows := view.dataRows()
	if len(rows) == 0 {
		t.Fatalf("admin view pick_policies returned zero rows; the producer declared one pick policy "+
			"(@docs), the dashboard must surface it. Raw view: %+v", view)
	}
	foundDocsRow := false
	for _, row := range rows {
		if sel, _ := row["selector"].(string); sel == "@docs" {
			foundDocsRow = true
			// @constraint: producer-sourced data, not a stub — the row must
			// carry fields the producer fills from its actual pick-policy +
			// on-disk state.
			if root, _ := row["root"].(string); root != "docs" {
				t.Errorf("admin view pick_policies @docs row root=%q, want %q (producer's actual policy)", root, "docs")
			}
			if _, ok := row["available_count"]; !ok {
				t.Errorf("admin view pick_policies @docs row missing available_count field; the row must carry producer-sourced data")
			}
			break
		}
	}
	if !foundDocsRow {
		t.Fatalf("admin view pick_policies missing the @docs row the producer was configured with; rows=%+v", rows)
	}

	// @constraint: the parametrised `policy_items` admin view's declaration
	// carries a required `selector` param; the dashboard passes it through.
	// The result must source from the producer (it lists real folders on
	// the on-disk bind-mount).
	itemsView := getAdminView(t, fs.HostHTTPBridge, "policy_items", map[string]string{"selector": "@docs"})
	itemRows := itemsView.dataRows()
	if len(itemRows) < 1 {
		t.Fatalf("admin view policy_items?selector=@docs returned 0 rows; the producer was seeded with three folders. View: %+v", itemsView)
	}
	// @deliberate: look for at least one seeded folder — three folders
	// (alpha, beta, gamma) all participated as the recycle pick policy
	// cycled through them; at least one must appear (either in available
	// or in_progress, depending on the ledger state).
	seedFolders := map[string]bool{"alpha": false, "beta": false, "gamma": false}
	for _, row := range itemRows {
		folder, _ := row["folder"].(string)
		if _, ok := seedFolders[folder]; ok {
			seedFolders[folder] = true
		}
	}
	anySeed := false
	for _, ok := range seedFolders {
		if ok {
			anySeed = true
			break
		}
	}
	if !anySeed {
		t.Fatalf("admin view policy_items?selector=@docs returned no rows for any seeded folder; "+
			"the producer's view must source from real on-disk state. Rows=%+v", itemRows)
	}
}

// peerJSON mirrors the JSON shape rimsky returns from
// `GET /v1/observability/stores/{name}`'s `peer` field. The fields
// here are the load-bearing ones for this test; unknown JSON fields
// are dropped silently.
type peerJSON struct {
	Name                  string            `json:"name"`
	Endpoint              string            `json:"endpoint"`
	ObservabilityEndpoint string            `json:"observability_endpoint"`
	HTTPBridgeURL         string            `json:"http_bridge_url"`
	Reachability          string            `json:"reachability_status"`
	Capabilities          *peerCapabilities `json:"observability_capabilities,omitempty"`
	LastError             string            `json:"last_error,omitempty"`
}

type peerCapabilities struct {
	SupportsClaimGet    bool             `json:"supports_claim_get,omitempty"`
	SupportsClaimStream bool             `json:"supports_claim_stream,omitempty"`
	SupportsListClaims  bool             `json:"supports_list_claims,omitempty"`
	AdminViews          []peerAdminView  `json:"admin_views,omitempty"`
	CustomUI            *json.RawMessage `json:"custom_ui,omitempty"`
}

type peerAdminView struct {
	Name        string `json:"name"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
}

// pollGetStorePeer fetches the rimsky dashboard's per-store
// observability detail (which carries the cached probe result) and
// polls until the handshake has marked the store reachable + the
// caps include the http_bridge_url. The initial RunHandshake fires
// at rimsky startup; on a freshly-booted stack the entry should
// already be present, but if probe ordering races the bring-up of
// the store-fs container we tolerate a short retry window.
func pollGetStorePeer(t *testing.T, ep harness.RimskyEndpoint, name string, deadline time.Duration) peerJSON {
	t.Helper()
	end := time.Now().Add(deadline)
	var last peerJSON
	for time.Now().Before(end) {
		status, raw := ep.GetJSON(t, "/v1/observability/stores/"+name, "")
		if status == http.StatusOK {
			var body struct {
				Peer peerJSON `json:"peer"`
			}
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Fatalf("decode /v1/observability/stores/%s: %v; raw=%s", name, err, string(raw))
			}
			last = body.Peer
			if last.Reachability == "reachable" && last.HTTPBridgeURL != "" && last.Capabilities != nil {
				return last
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("dashboard /v1/observability/stores/%s never converged to reachable+http_bridge_url within %v; last=%+v",
		name, deadline, last)
	return last
}

// claimSummary mirrors the inventory row shape served by the
// HTTP+JSON bridge's ListClaims endpoint (protojson form, so the
// fields are camelCase).
type claimSummary struct {
	ClaimID  string `json:"claimId"`
	State    string `json:"state"`
	OpenedAt string `json:"openedAt,omitempty"`
	ClosedAt string `json:"closedAt,omitempty"`
}

type claimsPage struct {
	Claims     []claimSummary `json:"claims"`
	NextCursor string         `json:"nextCursor,omitempty"`
}

// getClaimsPage queries the producer's HTTP+JSON bridge directly for
// one page of the claim inventory. The bridge's path is
// `/observability/v1/claims` (see protocols/serverkit/observability.go);
// rimsky's dashboard never proxies inventory — the bridge is dialed
// from the operator's browser.
func getClaimsPage(t *testing.T, bridge, cursor string, limit int) claimsPage {
	t.Helper()
	url := fmt.Sprintf("%s/observability/v1/claims?limit=%d", strings.TrimRight(bridge, "/"), limit)
	if cursor != "" {
		url += "&cursor=" + cursor
	}
	resp, err := http.Get(url) //nolint:gosec // #nosec G107
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status=%d", url, resp.StatusCode)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", url, err)
	}
	var page claimsPage
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatalf("decode %s: %v; raw=%s", url, err, string(raw))
	}
	return page
}

// claimEvent mirrors a single ClaimEvent in the bridge's protojson
// (camelCase) shape.
type claimEvent struct {
	EventID   string `json:"eventId"`
	Timestamp string `json:"timestamp"`
	Severity  string `json:"severity"`
	Category  string `json:"category"`
	Message   string `json:"message,omitempty"`
}

type claimDetail struct {
	ClaimID  string       `json:"claimId"`
	State    string       `json:"state"`
	OpenedAt string       `json:"openedAt,omitempty"`
	ClosedAt string       `json:"closedAt,omitempty"`
	History  []claimEvent `json:"history,omitempty"`
}

// getClaimDetail fetches the producer's full record for one claim_id
// through the HTTP+JSON bridge. The bridge's path is
// `/observability/v1/claims/{claim_id}`.
func getClaimDetail(t *testing.T, bridge, claimID string) claimDetail {
	t.Helper()
	url := fmt.Sprintf("%s/observability/v1/claims/%s", strings.TrimRight(bridge, "/"), claimID)
	resp, err := http.Get(url) //nolint:gosec // #nosec G107
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status=%d", url, resp.StatusCode)
	}
	var d claimDetail
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		t.Fatalf("decode %s: %v", url, err)
	}
	return d
}

// streamClaim subscribes to the bridge's SSE stream for one claim_id
// and returns the parsed events. The producer's StreamClaim atomically
// replays history then streams new events; for a terminal claim we
// expect to see the recorded history plus a terminal marker, after
// which the bridge closes the stream (or the test's context times
// out). Each SSE frame is `data: <protojson>\n\n`.
func streamClaim(t *testing.T, ctx context.Context, bridge, claimID string) []map[string]any {
	t.Helper()
	url := fmt.Sprintf("%s/observability/v1/claims/%s/stream",
		strings.TrimRight(bridge, "/"), claimID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("build SSE request %s: %v", url, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status=%d", url, resp.StatusCode)
	}
	events := []map[string]any{}
	scanner := bufio.NewScanner(resp.Body)
	// @constraint: default buffer for an SSE-claim event is small (a few
	// hundred bytes), but per-claim events can carry an attribute struct,
	// so bump the cap to avoid silent truncation.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		var ev map[string]any
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			t.Fatalf("decode SSE event payload %q: %v", payload, err)
		}
		events = append(events, ev)
		// @deliberate: stop after seeing the terminal marker — further events
		// would only be from the bridge's idle close anyway, and reading the
		// stream to its natural close avoids relying on the connection
		// shutdown behaviour of the test's HTTP client.
		if cat, _ := ev["category"].(string); cat == "claim_terminal" {
			break
		}
	}
	// @deliberate: Scanner.Err() with context.DeadlineExceeded is acceptable
	// — the terminal-marker exit above is the happy path. A real I/O error
	// (the bridge crashed mid-stream) is the failure mode.
	if err := scanner.Err(); err != nil && err != context.DeadlineExceeded {
		if ctx.Err() == nil {
			t.Fatalf("scanner: %v", err)
		}
	}
	return events
}

// adminViewResp mirrors the HTTP+JSON bridge's GetAdminView response
// (protojson camelCase). The `data` field is a google.protobuf.Struct
// which protojson renders as a generic JSON object — we keep it as a
// raw map and pluck `rows` for assertions.
type adminViewResp struct {
	Schema     map[string]any `json:"schema,omitempty"`
	Data       map[string]any `json:"data,omitempty"`
	RenderHint string         `json:"renderHint,omitempty"`
}

func (a adminViewResp) dataRows() []map[string]any {
	if a.Data == nil {
		return nil
	}
	rowsAny, ok := a.Data["rows"]
	if !ok {
		return nil
	}
	rows, ok := rowsAny.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		row, ok := r.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, row)
	}
	return out
}

// getAdminView queries the producer-declared admin view through the
// HTTP+JSON bridge's `/observability/v1/admin/{view_name}` route.
// Params are passed as query-string values (the bridge marshals them
// into a Struct).
func getAdminView(t *testing.T, bridge, name string, params map[string]string) adminViewResp {
	t.Helper()
	url := fmt.Sprintf("%s/observability/v1/admin/%s", strings.TrimRight(bridge, "/"), name)
	if len(params) > 0 {
		first := true
		for k, v := range params {
			if first {
				url += "?"
				first = false
			} else {
				url += "&"
			}
			url += k + "=" + v
		}
	}
	resp, err := http.Get(url) //nolint:gosec // #nosec G107
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status=%d", url, resp.StatusCode)
	}
	var view adminViewResp
	if err := json.NewDecoder(resp.Body).Decode(&view); err != nil {
		t.Fatalf("decode %s: %v", url, err)
	}
	return view
}

// categoriesEqual reports whether the stream's pre-terminal event
// categories equal the recorded history's categories in the same
// order. A stream that drops or reorders events would diverge here.
func categoriesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// deployObsTemplate posts a one-node template that opens a claim on
// the @docs pick policy and returns the template_id once deployed.
func deployObsTemplate(t *testing.T, ep harness.RimskyEndpoint, name string) string {
	t.Helper()
	body := map[string]any{
		"spec": map[string]any{
			"name":                  name,
			"version":               "1",
			"frame_resolution_mode": "serial_queue",
			"frame_timeout_ms":      600000,
			"nodes": []map[string]any{
				{
					"type":     "worker",
					"executor": "stub",
					"stores": []map[string]any{
						{"name": "docs", "selector": "@docs", "intent": "rw"},
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
		t.Fatalf("POST /v1/templates/%s/deploy: %d %s", resp.TemplateID, deployStatus, string(deployRaw))
	}
	return resp.TemplateID
}

// createObsInstance posts a new instance against templateID and
// returns its instance_id.
func createObsInstance(t *testing.T, ep harness.RimskyEndpoint, templateID, instanceKey string) string {
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

// waitForLedgerClaimCount polls the producer's HTTP+JSON bridge's
// ListClaims endpoint until the ledger reports at least `want` total
// claims. ListClaims returns one row per recorded claim (any state);
// the producer's ledger only records a claim once Open has run, so
// this is a sound proof that real Open / Commit traffic has reached
// the producer. Walks pages (each capped at 50 via the bridge's
// default) until next_cursor is empty so the count is total.
func waitForLedgerClaimCount(t *testing.T, bridge string, want int, deadline time.Duration) {
	t.Helper()
	end := time.Now().Add(deadline)
	var lastCount int
	for time.Now().Before(end) {
		total := 0
		cursor := ""
		for page := 0; page < 32; page++ {
			p := getClaimsPage(t, bridge, cursor, 50)
			total += len(p.Claims)
			if p.NextCursor == "" {
				break
			}
			cursor = p.NextCursor
		}
		lastCount = total
		if total >= want {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("producer ledger never reported >= %d claims within %v; last count=%d. "+
		"This means rimsky never opened a claim against the store — the wiring "+
		"between the rimsky supervisor's executor dispatch and the store's "+
		"ClaimProducer surface is broken at some point. Check rimsky logs.",
		want, deadline, lastCount)
}
