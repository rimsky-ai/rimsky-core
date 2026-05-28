// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// resources_test.go — JSON-RPC dispatch tests for the resources/list
// and resources/read methods added in Pass 6 per spec
// .ok-planner/specs/2026-05-24-instance-debugger-design.md §6.
//
// The dispatcher (mcp.Server.ServeHTTP) lives in this package; the
// breakpoint-hits URI scheme + persistence reads live in
// controlapi/mcp_resources.go. Tests here focus on the dispatch +
// shape contract (capability advertising at initialize, list returns
// {"resources":[...]}, read returns {"contents":[...]}, polling-cursor
// pagination flows through unmodified). Permission-gated catalog
// behavior (filtering per identity) is exercised against a fakeResources
// stub that mirrors what the controlapi breakpointResourceCatalog does.

package mcp_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/control/controlapi/mcp"
)

// fakeResources is a stub ResourceCatalog that lets tests assert
// dispatch + shape without standing up persistence.
type fakeResources struct {
	listFn func(r *http.Request) ([]mcp.Resource, error)
	readFn func(r *http.Request, uri string) (*mcp.ResourceContents, *mcp.Error)

	listCalls int
	readCalls int
	readURI   string
}

func (f *fakeResources) List(r *http.Request) ([]mcp.Resource, error) {
	f.listCalls++
	if f.listFn != nil {
		return f.listFn(r)
	}
	return nil, nil
}

func (f *fakeResources) Read(r *http.Request, uri string) (*mcp.ResourceContents, *mcp.Error) {
	f.readCalls++
	f.readURI = uri
	if f.readFn != nil {
		return f.readFn(r, uri)
	}
	return nil, nil
}

// TestMCPInitializeAdvertisesResources verifies the resources
// capability is advertised at initialize per spec §6.1.
func TestMCPInitializeAdvertisesResources(t *testing.T) {
	server := &mcp.Server{Tools: &fakeCatalog{}, Resources: &fakeResources{}}
	resp := serveRPC(t, server, `{"jsonrpc":"2.0","id":1,"method":"initialize"}`)
	if resp.Error != nil {
		t.Fatalf("initialize: %v", resp.Error)
	}
	m := resp.Result.(map[string]any)
	caps := m["capabilities"].(map[string]any)
	res, ok := caps["resources"].(map[string]any)
	if !ok {
		t.Fatalf("missing resources capability: %+v", caps)
	}
	if sub, _ := res["subscribe"].(bool); sub {
		t.Errorf("resources.subscribe should be false in v1 (push deferred)")
	}
	if lc, _ := res["listChanged"].(bool); lc {
		t.Errorf("resources.listChanged should be false in v1")
	}
}

// TestMCPResourcesList_ShapesResponse verifies the dispatcher
// envelopes the catalog's []Resource into {"resources": [...]}.
func TestMCPResourcesList_ShapesResponse(t *testing.T) {
	fr := &fakeResources{
		listFn: func(r *http.Request) ([]mcp.Resource, error) {
			return []mcp.Resource{
				{URI: "rimsky://instances/abc/breakpoint-hits", Name: "a", MimeType: "application/x-rimsky-breakpoint-hits+json"},
				{URI: "rimsky://instances/def/breakpoint-hits", Name: "d", MimeType: "application/x-rimsky-breakpoint-hits+json"},
			}, nil
		},
	}
	server := &mcp.Server{Tools: &fakeCatalog{}, Resources: fr}
	resp := serveRPC(t, server, `{"jsonrpc":"2.0","id":2,"method":"resources/list"}`)
	if resp.Error != nil {
		t.Fatalf("resources/list: %v", resp.Error)
	}
	m := resp.Result.(map[string]any)
	list, ok := m["resources"].([]any)
	if !ok {
		t.Fatalf("expected resources array: %+v", m)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 resources; got %d", len(list))
	}
	first := list[0].(map[string]any)
	if first["uri"] != "rimsky://instances/abc/breakpoint-hits" {
		t.Errorf("uri mismatch: %v", first["uri"])
	}
	if fr.listCalls != 1 {
		t.Errorf("expected 1 list call; got %d", fr.listCalls)
	}
}

// TestMCPResourcesList_PermissionGatedReturnsEmpty verifies that a
// catalog that returns no resources (the permission-denied case) is
// surfaced as `{"resources":[]}` rather than an error or null. This
// matches the controlapi breakpointResourceCatalog.List behavior when
// the identity has no breakpoint:read grant.
func TestMCPResourcesList_PermissionGatedReturnsEmpty(t *testing.T) {
	fr := &fakeResources{
		listFn: func(r *http.Request) ([]mcp.Resource, error) {
			// Mirror the production catalog: no `breakpoint:read` → empty.
			return []mcp.Resource{}, nil
		},
	}
	server := &mcp.Server{Tools: &fakeCatalog{}, Resources: fr}
	resp := serveRPC(t, server, `{"jsonrpc":"2.0","id":3,"method":"resources/list"}`)
	if resp.Error != nil {
		t.Fatalf("resources/list: %v", resp.Error)
	}
	m := resp.Result.(map[string]any)
	list, ok := m["resources"].([]any)
	if !ok {
		t.Fatalf("expected resources array; got %T", m["resources"])
	}
	if len(list) != 0 {
		t.Fatalf("expected empty resources array; got %d entries: %+v", len(list), list)
	}
}

// TestMCPResourcesRead_ShapesResponse verifies the dispatcher wraps a
// single ResourceContents in `{"contents":[{...}]}`.
func TestMCPResourcesRead_ShapesResponse(t *testing.T) {
	fr := &fakeResources{
		readFn: func(r *http.Request, uri string) (*mcp.ResourceContents, *mcp.Error) {
			return &mcp.ResourceContents{
				URI:      uri,
				MimeType: "application/x-rimsky-breakpoint-hits+json",
				Text:     `{"hits":[],"next_since":0,"truncated":false}`,
			}, nil
		},
	}
	server := &mcp.Server{Tools: &fakeCatalog{}, Resources: fr}
	resp := serveRPC(t, server, `{"jsonrpc":"2.0","id":4,"method":"resources/read","params":{"uri":"rimsky://instances/00000000-0000-0000-0000-000000000001/breakpoint-hits"}}`)
	if resp.Error != nil {
		t.Fatalf("resources/read: %v", resp.Error)
	}
	m := resp.Result.(map[string]any)
	contents, ok := m["contents"].([]any)
	if !ok {
		t.Fatalf("expected contents array: %+v", m)
	}
	if len(contents) != 1 {
		t.Fatalf("expected 1 contents entry; got %d", len(contents))
	}
	c := contents[0].(map[string]any)
	if c["mimeType"] != "application/x-rimsky-breakpoint-hits+json" {
		t.Errorf("mimeType mismatch: %v", c["mimeType"])
	}
	if !strings.Contains(c["text"].(string), `"hits"`) {
		t.Errorf("text body missing hits: %v", c["text"])
	}
	if fr.readURI != "rimsky://instances/00000000-0000-0000-0000-000000000001/breakpoint-hits" {
		t.Errorf("read uri propagation: %q", fr.readURI)
	}
}

// TestMCPResourcesRead_PassesCursorQueryParamsToCatalog confirms the
// dispatcher forwards the raw URI (with ?since=…&limit=…) unchanged to
// the catalog. Cursor parsing lives in the catalog implementation
// (controlapi/mcp_resources.go::parseBreakpointHitsURI); this test
// guards the wire-level contract.
func TestMCPResourcesRead_PassesCursorQueryParamsToCatalog(t *testing.T) {
	var capturedURI string
	fr := &fakeResources{
		readFn: func(r *http.Request, uri string) (*mcp.ResourceContents, *mcp.Error) {
			capturedURI = uri
			return &mcp.ResourceContents{URI: uri, MimeType: "application/x-rimsky-breakpoint-hits+json", Text: "{}"}, nil
		},
	}
	server := &mcp.Server{Tools: &fakeCatalog{}, Resources: fr}
	requestURI := "rimsky://instances/00000000-0000-0000-0000-000000000001/breakpoint-hits?since=42&limit=10"
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      5,
		"method":  "resources/read",
		"params":  map[string]any{"uri": requestURI},
	})
	resp := serveRPC(t, server, string(body))
	if resp.Error != nil {
		t.Fatalf("resources/read: %v", resp.Error)
	}
	if capturedURI != requestURI {
		t.Fatalf("uri not forwarded verbatim: got %q want %q", capturedURI, requestURI)
	}
}

// TestMCPResourcesRead_RejectsMissingURI verifies the dispatcher
// returns CodeInvalidParams when the params object lacks `uri`.
func TestMCPResourcesRead_RejectsMissingURI(t *testing.T) {
	server := &mcp.Server{Tools: &fakeCatalog{}, Resources: &fakeResources{}}
	resp := serveRPC(t, server, `{"jsonrpc":"2.0","id":6,"method":"resources/read","params":{}}`)
	if resp.Error == nil || resp.Error.Code != mcp.CodeInvalidParams {
		t.Fatalf("expected invalid-params; got %+v", resp.Error)
	}
}

// TestMCPResourcesRead_NoCatalogReturnsMethodNotFound covers the
// tools-only deployment fallback: a Server without a Resources field
// surfaces resources/read as method-not-found rather than a misleading
// success.
func TestMCPResourcesRead_NoCatalogReturnsMethodNotFound(t *testing.T) {
	server := &mcp.Server{Tools: &fakeCatalog{}}
	resp := serveRPC(t, server, `{"jsonrpc":"2.0","id":7,"method":"resources/read","params":{"uri":"rimsky://instances/x/breakpoint-hits"}}`)
	if resp.Error == nil || resp.Error.Code != mcp.CodeMethodNotFound {
		t.Fatalf("expected method-not-found; got %+v", resp.Error)
	}
}

// TestMCPResourcesList_NoCatalogReturnsEmpty covers the tools-only
// deployment fallback for the list variant — a Server without a
// Resources field returns an empty `{"resources":[]}` so a client
// probing the capability sees a clean answer rather than an error.
func TestMCPResourcesList_NoCatalogReturnsEmpty(t *testing.T) {
	server := &mcp.Server{Tools: &fakeCatalog{}}
	resp := serveRPC(t, server, `{"jsonrpc":"2.0","id":8,"method":"resources/list"}`)
	if resp.Error != nil {
		t.Fatalf("resources/list: %v", resp.Error)
	}
	m := resp.Result.(map[string]any)
	list, _ := m["resources"].([]any)
	if len(list) != 0 {
		t.Fatalf("expected empty resources; got %+v", list)
	}
}

// TestMCPResourcesRead_PollingCursorPagination simulates a polling
// agent draining a queue: read returns truncated=true with a next
// cursor, the next read with that cursor returns the next page, and
// the final read returns an empty page with truncated=false. The
// catalog stub holds the entire dataset; this test is the wire-level
// version of the "agent records next_since and re-polls" loop from
// spec §6.4.
func TestMCPResourcesRead_PollingCursorPagination(t *testing.T) {
	// Synthetic data: 5 hits with seq 1..5. Limit 2 per page → 3 pages
	// (2+2+1, with the last page un-truncated since len < limit).
	allHits := []map[string]any{
		{"seq": 1, "hit_id": "h1"},
		{"seq": 2, "hit_id": "h2"},
		{"seq": 3, "hit_id": "h3"},
		{"seq": 4, "hit_id": "h4"},
		{"seq": 5, "hit_id": "h5"},
	}
	fr := &fakeResources{
		readFn: func(r *http.Request, uri string) (*mcp.ResourceContents, *mcp.Error) {
			// Parse since= and limit= out of the URI the same way the
			// production catalog does — minimal stdlib parse so the
			// test doesn't depend on the controlapi package.
			since, limit := parseFakeCursor(uri, t)
			page := []map[string]any{}
			for _, h := range allHits {
				if int64(h["seq"].(int)) > since {
					page = append(page, h)
					if len(page) >= limit {
						break
					}
				}
			}
			next := since
			if len(page) > 0 {
				next = int64(page[len(page)-1]["seq"].(int))
			}
			body, _ := json.Marshal(map[string]any{
				"hits":       page,
				"next_since": next,
				"truncated":  len(page) >= limit,
			})
			return &mcp.ResourceContents{
				URI:      uri,
				MimeType: "application/x-rimsky-breakpoint-hits+json",
				Text:     string(body),
			}, nil
		},
	}
	server := &mcp.Server{Tools: &fakeCatalog{}, Resources: fr}

	// Drain via repeated reads. Start at since=0 (no cursor).
	cursor := int64(0)
	collected := []int64{}
	for i := 0; i < 10; i++ { // safety bound; expect 3 iterations
		uri := buildCursorURI(cursor, 2)
		body, _ := json.Marshal(map[string]any{
			"jsonrpc": "2.0",
			"id":      100 + i,
			"method":  "resources/read",
			"params":  map[string]any{"uri": uri},
		})
		resp := serveRPC(t, server, string(body))
		if resp.Error != nil {
			t.Fatalf("page %d: %v", i, resp.Error)
		}
		m := resp.Result.(map[string]any)
		contents := m["contents"].([]any)
		if len(contents) != 1 {
			t.Fatalf("page %d: expected 1 contents entry; got %d", i, len(contents))
		}
		c := contents[0].(map[string]any)
		// Decode the inner JSON text per spec §6.4.
		var inner struct {
			Hits      []map[string]any `json:"hits"`
			NextSince int64            `json:"next_since"`
			Truncated bool             `json:"truncated"`
		}
		if err := json.Unmarshal([]byte(c["text"].(string)), &inner); err != nil {
			t.Fatalf("page %d inner decode: %v", i, err)
		}
		for _, h := range inner.Hits {
			seq := int64(h["seq"].(float64))
			collected = append(collected, seq)
		}
		cursor = inner.NextSince
		if !inner.Truncated {
			break
		}
	}
	if len(collected) != 5 {
		t.Fatalf("expected to drain all 5 hits; got %d (%v)", len(collected), collected)
	}
	for i, seq := range collected {
		if int(seq) != i+1 {
			t.Errorf("collected[%d] = %d; want %d", i, seq, i+1)
		}
	}

	// One more poll past the end returns an empty page with
	// truncated=false — confirming the cursor advances and the polling
	// agent can wait safely.
	uri := buildCursorURI(cursor, 2)
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      999,
		"method":  "resources/read",
		"params":  map[string]any{"uri": uri},
	})
	resp := serveRPC(t, server, string(body))
	if resp.Error != nil {
		t.Fatalf("tail poll: %v", resp.Error)
	}
	c := resp.Result.(map[string]any)["contents"].([]any)[0].(map[string]any)
	var inner struct {
		Hits      []map[string]any `json:"hits"`
		NextSince int64            `json:"next_since"`
		Truncated bool             `json:"truncated"`
	}
	if err := json.Unmarshal([]byte(c["text"].(string)), &inner); err != nil {
		t.Fatalf("tail decode: %v", err)
	}
	if len(inner.Hits) != 0 {
		t.Fatalf("tail poll should be empty; got %d hits", len(inner.Hits))
	}
	if inner.Truncated {
		t.Errorf("tail poll truncated should be false")
	}
}

// parseFakeCursor extracts ?since= and ?limit= from a URI string.
// Test-internal mirror of the production parser; kept here so the mcp
// package's tests don't have to import controlapi.
func parseFakeCursor(uri string, t *testing.T) (int64, int) {
	t.Helper()
	qIdx := strings.Index(uri, "?")
	if qIdx < 0 {
		return 0, 100
	}
	since := int64(0)
	limit := 100
	for _, kv := range strings.Split(uri[qIdx+1:], "&") {
		eq := strings.Index(kv, "=")
		if eq < 0 {
			continue
		}
		k, v := kv[:eq], kv[eq+1:]
		switch k {
		case "since":
			var n int64
			for _, c := range v {
				if c < '0' || c > '9' {
					t.Fatalf("bad since digit: %q", v)
				}
				n = n*10 + int64(c-'0')
			}
			since = n
		case "limit":
			var n int
			for _, c := range v {
				if c < '0' || c > '9' {
					t.Fatalf("bad limit digit: %q", v)
				}
				n = n*10 + int(c-'0')
			}
			limit = n
		}
	}
	return since, limit
}

// buildCursorURI is the test-side complement to parseFakeCursor.
func buildCursorURI(since int64, limit int) string {
	return "rimsky://instances/00000000-0000-0000-0000-000000000001/breakpoint-hits?since=" +
		itoa(since) + "&limit=" + itoa(int64(limit))
}

// itoa is a tiny dependency-free integer formatter so this test file
// doesn't pull strconv just for two cursors.
func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
