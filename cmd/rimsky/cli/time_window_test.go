// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package cli_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli"
)

// @story: audit-log-read
func TestAuditReadsTheAuthFeedNarrowedToItsWindow(t *testing.T) {
	srv := setupClitest(t)
	base := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	srv.State.AddEvent(cli.Event{
		Kind:       "auth.key_created",
		OccurredAt: base.Format(time.RFC3339),
		Payload:    map[string]any{"key_name": "deployer", "action": "auth:create"},
	})
	srv.State.AddEvent(cli.Event{
		Kind:       "auth.key_revoked",
		OccurredAt: base.Add(48 * time.Hour).Format(time.RFC3339),
		Payload:    map[string]any{"key_name": "retired", "action": "auth:revoke"},
	})

	var code int
	out := captureStdout(t, func() {
		code = cli.RunAudit(context.Background(), []string{
			"--since", base.Add(-time.Hour).Format(time.RFC3339),
			"--until", base.Add(time.Hour).Format(time.RFC3339),
		})
	})
	if code != 0 {
		t.Fatalf("rimsky audit: exit %d, want 0. Output:\n%s", code, out)
	}
	if !strings.Contains(out, "auth.key_created") || !strings.Contains(out, "deployer") {
		t.Errorf("rimsky audit: stdout %q, want the in-window row with its key name", out)
	}
	if strings.Contains(out, "auth.key_revoked") {
		t.Errorf("rimsky audit: stdout %q carried a row outside --since/--until", out)
	}
}

// @story: audit-log-read
func TestAuditWithoutAWindowReadsOneBoundedPageAndSaysSo(t *testing.T) {
	var requests int
	var gotLimit string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		gotLimit = r.URL.Query().Get("limit")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"audit": []map[string]any{
				{"id": 1, "kind": "auth.key_created", "occurred_at": "2026-08-01T12:00:00Z",
					"payload": map[string]any{"key_name": "deployer"}},
			},
			"next_cursor": "c1",
		})
	}))
	defer srv.Close()

	var code int
	notice := captureStderr(t, func() {
		code = cli.RunAudit(context.Background(), []string{"--endpoint", srv.URL})
	})
	if code != 0 {
		t.Fatalf("rimsky audit: exit %d, want 0", code)
	}
	if requests != 1 {
		t.Errorf("a bare audit read issued %d requests; it reads one bounded page", requests)
	}
	if gotLimit != "100" {
		t.Errorf("limit=%q, want the verb's page size", gotLimit)
	}
	if !strings.Contains(notice, "--since") {
		t.Errorf("rimsky audit truncated its read and named no window flags; stderr=%q", notice)
	}
}

// @story: audit-log-read
func TestAuditWalksTheWholeWindowAcrossPages(t *testing.T) {
	pages := map[string][]map[string]any{
		"":   {{"id": 3, "kind": "auth.key_created", "occurred_at": "2026-08-01T12:00:03Z"}},
		"c1": {{"id": 2, "kind": "auth.key_created", "occurred_at": "2026-08-01T12:00:02Z"}},
		"c2": {{"id": 1, "kind": "auth.key_created", "occurred_at": "2026-08-01T12:00:01Z"}},
	}
	next := map[string]string{"": "c1", "c1": "c2", "c2": ""}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cursor := r.URL.Query().Get("cursor")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"audit":       pages[cursor],
			"next_cursor": next[cursor],
		})
	}))
	defer srv.Close()

	var code int
	out := captureStdout(t, func() {
		code = cli.RunAudit(context.Background(), []string{
			"--endpoint", srv.URL, "--since", "2026-08-01T00:00:00Z", "-o", "json",
		})
	})
	if code != 0 {
		t.Fatalf("rimsky audit --since: exit %d, want 0", code)
	}
	var rows []cli.Event
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("decode audit rows: %v\nstdout: %s", err, out)
	}
	got := map[int64]bool{}
	for _, row := range rows {
		got[row.ID] = true
	}
	for _, id := range []int64{1, 2, 3} {
		if !got[id] {
			t.Errorf("a windowed read stopped at the first page: row %d is missing from %v", id, got)
		}
	}
}

// @story: audit-log-read
func TestAuditEmitsItsRowsAsJSONOnStdout(t *testing.T) {
	srv := setupClitest(t)
	srv.State.AddEvent(cli.Event{Kind: "auth.key_created", Payload: map[string]any{"key_name": "deployer"}})

	out := captureStdout(t, func() {
		if code := cli.RunAudit(context.Background(), []string{"-o", "json"}); code != 0 {
			t.Fatalf("rimsky audit -o json: exit %d", code)
		}
	})
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("rimsky audit -o json did not emit a JSON array on stdout: %v (%q)", err, out)
	}
	if len(rows) != 1 || rows[0]["kind"] != "auth.key_created" {
		t.Errorf("rimsky audit -o json: got %#v, want the one audit row", rows)
	}
}

func TestWatchNamesItsExitStateWithUntilState(t *testing.T) {
	srv := setupClitest(t)
	hash := deployedTemplate(t, srv, "v1")
	inst, _, _ := srv.State.CreateInstance(hash, nil, nil)

	var code int
	complaint := captureStderr(t, func() {
		code = cli.RunWatch(context.Background(), []string{"--until-state", "settled", inst.ID})
	})
	if code != 2 {
		t.Errorf("watch --until-state settled: exit %d, want 2", code)
	}
	if !strings.Contains(complaint, "--until-state") {
		t.Errorf("watch --until-state settled: stderr %q, want the flag named", complaint)
	}

	code = cli.RunWatch(context.Background(), []string{"--until", "terminated", inst.ID})
	if code != 2 {
		t.Errorf("watch --until terminated: exit %d, want 2. --until names a time window. Watch names "+
			"its exit condition with --until-state", code)
	}
}

func TestLineagePruneNamesItsCutoffWithUntil(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"deleted": 3})
	}))
	defer srv.Close()

	const cutoff = "2026-01-01T00:00:00Z"
	if code := cli.RunLineagePrune(context.Background(), []string{
		"--endpoint", srv.URL, "--yes", "--until", cutoff,
	}); code != 0 {
		t.Fatalf("lineage prune --until: exit %d, want 0", code)
	}
	if body["before"] != cutoff {
		t.Errorf("lineage prune --until sent %v, want the cutoff %q", body["before"], cutoff)
	}

	if code := cli.RunLineagePrune(context.Background(), []string{
		"--endpoint", srv.URL, "--yes", "--before", cutoff,
	}); code != 2 {
		t.Errorf("lineage prune --before: exit %d, want 2. The cutoff flag is --until across the CLI", code)
	}
}

// @story: audit-log-read
func TestAuditRefusesTwoFiltersOverTheSameField(t *testing.T) {
	srv := setupClitest(t)
	srv.State.AddEvent(cli.Event{
		Kind:       "auth.key_created",
		OccurredAt: time.Now().UTC().Format(time.RFC3339),
		Payload:    map[string]any{"key_name": "deployer", "action": "auth:create"},
	})

	var code int
	out := captureStdout(t, func() {
		code = cli.RunAudit(context.Background(), []string{"--action", "auth:create", "--action-prefix", "instance:"})
	})
	if code == 0 {
		t.Fatalf("rimsky audit --action with --action-prefix: exit 0. The route honors one and drops the other, "+
			"so the verb must refuse the pair. Output:\n%s", out)
	}
	if out != "" {
		t.Errorf("a refused verb prints no rows on stdout; got %q", out)
	}
}
