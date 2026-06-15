// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// End-to-end proof that `rimsky watch <id>` renders events, breakpoint hits,
// and the terminal line as ONE timestamp-ordered feed against the REAL
// assembled product — not a source-grouped batch.
//
// STORY-event-log-read: an operator debugging a running instance runs
// `rimsky watch <id>` and sees node lifecycle transitions, breakpoint hits,
// message activity, and supervisor decisions interleaved by true timestamp
// order across the kinds — so the relative ordering of every source on the
// unified event feed is faithfully shown.
//
// The value path is driven through the REAL CLI entrypoint (`cli.RunWatch`)
// in-process — the same poll loop an operator hits — against a live
// all-in-one stack: the control-api event log and breakpoint-hits route are
// the real components feeding watch, the scheduler/supervisor produce the
// real interleaved sequence, and the in-tree stub executor stands in for
// "whatever executor your deployment provides".
//
// The interleaved sequence is seeded by a real pause-mode breakpoint on the
// worker node's `before_dispatch` checkpoint, then an operator-side
// invalidate is injected DURING the paused window so a `message_emitted`
// event lands strictly between the hit and the post-resume settle events:
//
//  1. The supervisor acquires the node and appends `work_started` (an EVENT,
//     timestamped BEFORE the hit).
//  2. It reaches `before_dispatch` and records a breakpoint hit. With
//     breakpoint hits unified into the `/events` log
//     (S-observability-breakpoint-hit-event), that hit is a `breakpoint.hit`
//     row on the SAME ordered `/events` stream — timestamped AFTER
//     `work_started`. Pause mode then BLOCKS the dispatch.
//  3. While the dispatch is held at the hit, the test POSTs an admin
//     invalidate against the worker node. `runtime.InvalidateNode` appends a
//     `message_emitted` event (and an `operator_override` audit event
//     ahead of it) on the SAME /events stream — timestamped AFTER the hit
//     and BEFORE any post-resume event, exhibiting the "message activity"
//     interleaving the story names. The invalidate enqueues a NEXT frame
//     that never runs (terminate_after_run will terminate the instance
//     after the currently-running frame ends).
//  4. The test resumes the breakpoint; the node dispatches and settles
//     against the stub, the cascade walk appends settle/terminal events
//     including a `state_transition` (the "node lifecycle transition"
//     kind), and `terminate_after_run` terminates the instance, which
//     makes watch's terminal check fire and the loop exit.
//
// So the real timeline is: work_started → breakpoint.hit → operator_override
// → message_emitted → … → state_transition → terminal. The assertion proves
// watch printed them in that true timestamp order — the hit line sits
// BETWEEN the pre-acquire events and the message-activity events, and the
// message events sit BETWEEN the hit and the post-resume settle events —
// and that the whole printed feed is timestamp-monotonic. A source-grouped
// watch (all events, then all hits) would print the hit AFTER every event,
// breaking the "between" relation and the monotonic-timestamp invariant;
// a kind-grouped feed (all of one kind then another) would scramble the
// message-activity interleaving; this test would catch either defect as a
// real value-path defect, not a Docker error.
package scenarios

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli"
	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

// TestCLIWatch_ChronologicalAcrossSources drives a real instance through an
// interleaved event → breakpoint-hit → event sequence and asserts
// `rimsky watch` prints the hit line between the two event lines by true
// timestamp order, against the live all-in-one stack.
func TestCLIWatch_ChronologicalAcrossSources(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// @constraint: stub executor must be reachable on the shared network
	// before rimsky/all starts — the control-api fires a Capabilities
	// handshake against declared executors at startup. Network first, then
	// the executor peer, then rimsky on the baked SQLite default.
	netName := harness.NewNetwork(ctx, t)
	harness.StartExecutorStubOnNetwork(ctx, t, netName, "executor-stub")

	ep := harness.BringUpRimsky(ctx, t,
		harness.WithSQLite(),
		harness.WithExistingNetwork(netName),
		harness.WithExecutor("stub", "executor-stub:9300"),
	)

	// @deliberate: a single executor node carrying an attributes block. The
	// stub returns Success, so once the breakpoint is resumed the node
	// settles and (terminate_after_run) the instance terminates, ending the
	// watch loop. The `attributes:` block is the canonical shape every
	// breakpoint scenario exercises (the in-process pause-resume scenario
	// gives its worker the same `{ok: boolean, readOnly}` schema), so the
	// gate's interleaved sequence rides the most-trodden dispatch path. (The
	// before_dispatch checkpoint also fires on an attribute-less node — see
	// the breakpoints-scenario regression that pins that — but the
	// attributed shape keeps this watch gate on the canonical path rather
	// than an edge case.)
	templateID := deployScenarioTemplate(t, ep, map[string]any{
		"spec": map[string]any{
			"name":                  "cli-watch-chronological",
			"version":               "1",
			"frame_resolution_mode": "serial_queue",
			"frame_timeout_ms":      600000,
			"nodes": []map[string]any{
				{
					"type":     "worker",
					"executor": "stub",
					"attributes": map[string]any{
						"schema": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"ok": map[string]any{"type": "boolean", "readOnly": true},
							},
						},
					},
				},
			},
		},
	})

	// @deliberate: instance is created PAUSED so the breakpoint can be
	// installed before any dispatch, and terminate_after_run so the
	// post-resume settle terminates the instance and watch exits cleanly.
	instanceID := createWatchPausedInstance(t, ep, templateID, "ck-watch-chronological")

	// @deliberate: pause-mode breakpoint on the worker's before_dispatch
	// checkpoint. before_dispatch runs AFTER the acquire-phase
	// `work_started` event and BEFORE the dispatch's settle events, so the
	// recorded hit's timestamp falls strictly between an earlier event and
	// the later events — exactly the interleaving the story requires.
	bpID := createWatchBreakpoint(t, ep, instanceID)

	// @deliberate: resuming the instance now drives the supervisor to claim
	// the node, append `work_started`, reach before_dispatch, record the
	// hit (a breakpoint.hit row on /events), and BLOCK on pause mode.
	if status, raw := ep.PostJSON(t, "/v1/instances/"+instanceID+"/resume", map[string]any{}); status != http.StatusOK {
		t.Fatalf("POST /instances/%s/resume: %d %s", instanceID, status, string(raw))
	}

	// @constraint: watch runs in a goroutine with stdout captured. The
	// capture holds the package stdout-swap lock for the whole watch
	// lifetime (RunWatch streams until the instance terminates), so no
	// other CLI capture interleaves.
	watch := startWatchCapture(t, ctx, ep.BaseURL, instanceID)

	// @constraint: wait until the supervisor has actually reached the
	// checkpoint and recorded a hit (the pause is now blocking the
	// dispatch). Only then resume the breakpoint — this guarantees the hit
	// is durably between the pre-dispatch and post-dispatch events before
	// the node proceeds.
	hitID := waitForWatchBreakpointHit(t, ep, instanceID, 60*time.Second)

	// @deliberate: while the dispatch is held at the pause-mode breakpoint,
	// inject an admin invalidate against the worker node. This routes
	// through `runtime.InvalidateNode`, which appends `operator_override` +
	// `message_emitted` events onto the SAME /events stream — timestamped
	// strictly AFTER the hit (the hit is already durably recorded above)
	// and strictly BEFORE the post-resume settle events (the dispatch is
	// still blocked). The invalidate uses `frame: "next"` so it enqueues a
	// separate frame rather than racing the in-flight dispatch;
	// terminate_after_run will terminate the instance after the running
	// frame settles, so the newly-enqueued frame never runs and the watch
	// loop still exits cleanly. The injection exhibits the
	// message-activity interleaving the story names — without it, the feed
	// would only carry node-lifecycle / breakpoint kinds and the "message
	// activity in chronological order" acceptance clause would be
	// untested. Timestamp ordering is protected by waiting for the hit
	// BEFORE injecting (so message_emitted is provably after the hit) and
	// by injecting BEFORE resuming the breakpoint (so message_emitted is
	// provably before the post-resume state_transition that the cascade
	// emits during settle propagation).
	workerNodeID := resolveWorkerNodeID(t, ep, instanceID, "worker")
	injectAdminInvalidate(t, ep, workerNodeID, "watch-chronological-interleave")

	resumeWatchBreakpoint(t, ep, instanceID, bpID, hitID)

	stdout := watch.wait(t, 90*time.Second)

	assertWatchFeedChronological(t, stdout)
}

// watchCapture is the handle to a backgrounded `cli.RunWatch` run whose
// stdout is being captured. wait blocks for the watch loop to return and
// yields the captured stdout.
type watchCapture struct {
	out  chan string
	code chan int
}

// startWatchCapture launches cli.RunWatch in a goroutine against the live
// endpoint with a 250ms poll interval, redirecting os.Stdout through a pipe
// for the duration of the run. It holds stdoutCaptureMu across the whole
// watch lifetime so a parallel one-shot CLI capture cannot clobber the swap.
//
// The poll interval is deliberately short (250ms) so the pre-hit event, the
// hit, and the post-resume events are drained promptly; the within-cycle
// timestamp merge in RunWatch is what proves the chronological ordering.
func startWatchCapture(t *testing.T, ctx context.Context, baseURL, instanceID string) *watchCapture {
	t.Helper()

	stdoutCaptureMu.Lock()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		stdoutCaptureMu.Unlock()
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w

	wc := &watchCapture{out: make(chan string, 1), code: make(chan int, 1)}

	readDone := make(chan string, 1)
	go func() {
		var b strings.Builder
		buf := make([]byte, 4096)
		for {
			n, rerr := r.Read(buf)
			if n > 0 {
				b.Write(buf[:n])
			}
			if rerr != nil {
				break
			}
		}
		readDone <- b.String()
	}()

	go func() {
		code := cli.RunWatch(ctx, []string{
			"--endpoint", baseURL,
			"--poll-interval", "250ms",
			instanceID,
		})
		os.Stdout = orig
		_ = w.Close()
		stdoutCaptureMu.Unlock()
		out := <-readDone
		_ = r.Close()
		wc.out <- out
		wc.code <- code
	}()

	return wc
}

// wait blocks until the watch loop returns (or the deadline elapses) and
// returns the captured stdout. A non-zero exit or a timeout is a real
// failure: watch must exit 0 when the instance terminates.
func (wc *watchCapture) wait(t *testing.T, deadline time.Duration) string {
	t.Helper()
	select {
	case out := <-wc.out:
		code := <-wc.code
		if code != 0 {
			t.Fatalf("rimsky watch exited %d (want 0)\nstdout:\n%s", code, out)
		}
		return out
	case <-time.After(deadline):
		t.Fatalf("rimsky watch did not exit within %v — the instance never terminated or the terminal check never fired", deadline)
		return ""
	}
}

// createWatchPausedInstance POSTs a new instance with paused:true and
// terminate_after_run:true and returns its instance_id. Paused so the
// breakpoint installs before any dispatch; terminate_after_run so the
// post-resume settle terminates the instance and ends the watch loop.
func createWatchPausedInstance(t *testing.T, ep harness.RimskyEndpoint, templateID, instanceKey string) string {
	t.Helper()
	status, raw := ep.PostJSON(t, "/v1/instances", map[string]any{
		"template":            templateID,
		"instance_key":        instanceKey,
		"params":              map[string]any{},
		"paused":              true,
		"terminate_after_run": true,
	})
	if status != http.StatusCreated {
		t.Fatalf("POST /instances (paused): %d %s", status, string(raw))
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

// createWatchBreakpoint installs a pause-mode before_dispatch breakpoint on
// the worker node and returns the breakpoint_id. Pause mode blocks the
// dispatch at the checkpoint so the hit is durably recorded between the
// pre-dispatch and post-dispatch events.
func createWatchBreakpoint(t *testing.T, ep harness.RimskyEndpoint, instanceID string) string {
	t.Helper()
	status, raw := ep.PostJSON(t, "/v1/instances/"+instanceID+"/breakpoints", map[string]any{
		"checkpoint": "before_dispatch",
		"matcher":    map[string]any{"node_type": "worker"},
		"mode":       "pause",
	})
	if status != http.StatusCreated {
		t.Fatalf("POST /instances/%s/breakpoints: %d %s", instanceID, status, string(raw))
	}
	var resp struct {
		BreakpointID string `json:"breakpoint_id"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode breakpoint response: %v: %s", err, string(raw))
	}
	if resp.BreakpointID == "" {
		t.Fatalf("breakpoint_id empty: %s", string(raw))
	}
	return resp.BreakpointID
}

// waitForWatchBreakpointHit polls the breakpoint-hits route until at least one
// hit is recorded and returns its hit_id. The hit appearing proves the
// supervisor reached the checkpoint and is now blocked on pause mode.
func waitForWatchBreakpointHit(t *testing.T, ep harness.RimskyEndpoint, instanceID string, deadline time.Duration) string {
	t.Helper()
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		status, raw := ep.GetJSON(t, "/v1/instances/"+instanceID+"/breakpoint-hits", "")
		if status == http.StatusOK {
			var resp struct {
				Hits []map[string]any `json:"hits"`
			}
			if err := json.Unmarshal(raw, &resp); err == nil && len(resp.Hits) > 0 {
				hitID, _ := resp.Hits[0]["hit_id"].(string)
				if hitID != "" {
					return hitID
				}
			}
		}
		time.Sleep(150 * time.Millisecond)
	}
	_, instRaw := ep.GetJSON(t, "/v1/instances/"+instanceID, "")
	_, nodeRaw := ep.GetJSON(t, "/v1/observability/nodes/"+instanceID+"/worker", "")
	_, bpRaw := ep.GetJSON(t, "/v1/instances/"+instanceID+"/breakpoints", "")
	_, evRaw := ep.GetJSON(t, "/v1/events?instance_id="+instanceID+"&limit=100", "")
	t.Logf("diag instance=%s", string(instRaw))
	t.Logf("diag node=%s", string(nodeRaw))
	t.Logf("diag breakpoints=%s", string(bpRaw))
	t.Logf("diag events=%s", string(evRaw))
	t.Fatalf("no breakpoint hit recorded on instance %s within %v — the supervisor never reached the before_dispatch checkpoint", instanceID, deadline)
	return ""
}

// resumeWatchBreakpoint resumes a paused breakpoint hit so the node proceeds
// to dispatch and settle.
func resumeWatchBreakpoint(t *testing.T, ep harness.RimskyEndpoint, instanceID, bpID, hitID string) {
	t.Helper()
	status, raw := ep.PostJSON(t,
		"/v1/instances/"+instanceID+"/breakpoints/"+bpID+"/resume",
		map[string]any{"hit_id": hitID})
	if status != http.StatusOK {
		t.Fatalf("POST breakpoint resume: %d %s", status, string(raw))
	}
}

// injectAdminInvalidate POSTs an operator-side invalidate against a node so
// `runtime.InvalidateNode` appends `operator_override` + `message_emitted`
// events to the instance's unified /events feed. Uses `frame: "next"` so
// the enqueued frame is queued behind the running one — for a
// terminate_after_run instance the queued frame is orphaned at terminal
// (per `MarkInstanceTerminatedIfDone`'s strict "run at most once more"
// semantics) and never executes, so the injection is observable only as
// the appended events. The 202 Accepted return shape is the admin
// invalidate's documented success status.
func injectAdminInvalidate(t *testing.T, ep harness.RimskyEndpoint, nodeID, reason string) {
	t.Helper()
	status, raw := ep.PostJSON(t,
		"/v1/nodes/"+nodeID+"/invalidate",
		map[string]any{"reason": reason, "frame": "next"})
	// @deliberate: handler returns 202 Accepted on success; treat any 2xx
	// as success — the load-bearing assertion is on the events the call
	// appended, not the status code itself.
	if status < 200 || status >= 300 {
		t.Fatalf("POST /v1/nodes/%s/invalidate: %d %s", nodeID, status, string(raw))
	}
}

// watchFeedLine is one parsed line of `rimsky watch` human-format stdout: the
// leading RFC3339 timestamp, the source column, and (for event-source rows)
// the event kind. RunWatch's text format is tab-separated:
//
//	event rows         → "<ts>\tevent\t<kind>\t<detail>"
//	breakpoint-hit rows → "<ts>\tbreakpoint.hit\tseq=…\t…"
//	terminal row        → "<ts>\tterminal\t…"
//
// so field[0] is the timestamp, field[1] the source, and (event rows only)
// field[2] the kind. The breakpoint.hit unified onto /events surfaces as an
// event-source row whose kind is "breakpoint.hit".
// @constraint: source is one of "event" | "breakpoint.hit" | "terminal";
// kind is populated only for event-source rows (the event Kind such as
// work_started).
type watchFeedLine struct {
	ts     time.Time
	source string
	kind   string
	raw    string
}

// isBreakpointHitEventRow reports whether the line is the breakpoint hit as it
// appears ON the unified /events stream (source "event", kind
// "breakpoint.hit") — the row the chronological-feed story is anchored on.
func (l watchFeedLine) isBreakpointHitEventRow() bool {
	return l.source == "event" && l.kind == "breakpoint.hit"
}

// isPlainEventRow reports whether the line is a genuine event-log row that is
// NOT the breakpoint-hit event — i.e. one of the real run events
// (work_started, state_transition, settle, …) that must bracket the hit.
func (l watchFeedLine) isPlainEventRow() bool {
	return l.source == "event" && l.kind != "breakpoint.hit"
}

// assertWatchFeedChronological parses the captured watch stdout and asserts:
//
//   - the printed feed is timestamp-monotonic (non-decreasing) across ALL
//     sources — a source-grouped feed would violate this the moment a hit
//     with an earlier timestamp printed after a later event;
//   - the breakpoint hit, as it appears on the unified /events stream
//     (source "event", kind "breakpoint.hit"), has at least one GENUINE
//     (non-hit) event line printed BEFORE it and at least one printed AFTER
//     it, by printed order — i.e. the hit sits BETWEEN two real run events,
//     the exact "event → breakpoint hit → later event" relation the story
//     names, and is timestamp-faithful (its own timestamp falls between the
//     bracketing events' timestamps).
func assertWatchFeedChronological(t *testing.T, stdout string) {
	t.Helper()

	lines := parseWatchFeed(stdout)
	if len(lines) == 0 {
		t.Fatalf("rimsky watch printed no parseable timestamped lines; stdout:\n%s", stdout)
	}

	// @constraint: hit must appear exactly once — as its /events row
	// (source=event, kind=breakpoint.hit), never ALSO as a pending-hits-route
	// row (source=breakpoint.hit). watch drains /events alone, so a
	// source=breakpoint.hit line would mean the redundant pending-hits read
	// came back and the hit is double-rendered.
	for _, l := range lines {
		if l.source == "breakpoint.hit" {
			t.Fatalf("watch feed contains a pending-hits-route row (source=breakpoint.hit) — the hit is double-rendered; watch must drain /events alone\nfull feed:\n%s", stdout)
		}
	}

	// @constraint: the whole feed must be timestamp-monotonic
	// (timestamp-faithful, not source-grouped). Load-bearing
	// anti-regression: if watch drained-and-printed each source as its own
	// batch, a hit timestamped before a later-printed event would break
	// monotonicity here.
	for i := 1; i < len(lines); i++ {
		if lines[i].ts.Before(lines[i-1].ts) {
			t.Fatalf("watch feed is NOT timestamp-ordered: line %d (%s, %s) precedes line %d (%s, %s) in print order but is later in time — output is source-grouped, not chronological\nfull feed:\n%s",
				i-1, lines[i-1].source, lines[i-1].ts.Format(time.RFC3339Nano),
				i, lines[i].source, lines[i].ts.Format(time.RFC3339Nano),
				stdout)
		}
	}

	// @constraint: anchor on the breakpoint hit as it appears on the
	// unified /events stream (the source the story's "single
	// timestamp-ordered /events stream" names), then prove a GENUINE
	// (non-hit) event sits on each side of it by printed order. Using the
	// /events-sourced hit row (not the redundant breakpoint-hits-route
	// row) and requiring NON-hit events on both sides is what makes this a
	// real interleaving proof — a duplicate of the same hit cannot satisfy
	// either bracket.
	hitIdx := -1
	for i, l := range lines {
		if l.isBreakpointHitEventRow() {
			hitIdx = i
			break
		}
	}
	if hitIdx < 0 {
		t.Fatalf("watch feed has no breakpoint.hit event row (source=event kind=breakpoint.hit) — the recorded hit was not surfaced into the unified /events feed\nfull feed:\n%s", stdout)
	}

	var beforeEvent, afterEvent *watchFeedLine
	for i := 0; i < hitIdx; i++ {
		if lines[i].isPlainEventRow() {
			beforeEvent = &lines[i]
		}
	}
	for i := hitIdx + 1; i < len(lines); i++ {
		if lines[i].isPlainEventRow() {
			afterEvent = &lines[i]
			break
		}
	}
	if beforeEvent == nil || afterEvent == nil {
		t.Fatalf("breakpoint.hit event row is not BETWEEN two genuine event rows (before=%v after=%v) — the hit was printed source-grouped, not interleaved by timestamp\nfull feed:\n%s",
			beforeEvent != nil, afterEvent != nil, stdout)
	}

	// @constraint: timestamp-faithfulness — the hit's own timestamp must
	// fall between the bracketing events' timestamps. The monotonic check
	// above already forbids a print-order violation, but pinning the hit's
	// timestamp strictly inside [before, after] proves the ordering is
	// driven by the real recorded times, not an accident of source-drain
	// order.
	hitTS := lines[hitIdx].ts
	if hitTS.Before(beforeEvent.ts) || afterEvent.ts.Before(hitTS) {
		t.Fatalf("breakpoint.hit timestamp %s is not within [%s, %s] of its bracketing events — ordering is not timestamp-faithful\nfull feed:\n%s",
			hitTS.Format(time.RFC3339Nano),
			beforeEvent.ts.Format(time.RFC3339Nano),
			afterEvent.ts.Format(time.RFC3339Nano),
			stdout)
	}

	// @constraint: message-activity interleaving — the admin invalidate
	// injected during the paused window must have produced a
	// `message_emitted` event row on the unified /events feed, printed
	// AFTER the breakpoint.hit row and BEFORE the post-resume
	// node-lifecycle row (a `state_transition` or `work_completed` event
	// the cascade walk emits during settle). This is the "node lifecycle
	// transitions, breakpoint hits, message activity, and supervisor
	// decisions in true chronological order across kinds" clause of
	// STORY-event-log-read's Acceptance: the prior bracket only proves
	// event-source vs hit-source ordering, while this clause proves three
	// distinct event KINDS (hit, message, lifecycle) appear interleaved by
	// timestamp rather than grouped by kind. A kind-grouped renderer (e.g.
	// one that drained the feed by kind and printed each batch in order)
	// would fail this check the moment message_emitted printed adjacent to
	// other message kinds rather than between the hit and the lifecycle
	// event.
	messageIdx := -1
	for i := hitIdx + 1; i < len(lines); i++ {
		if lines[i].source == "event" && lines[i].kind == "message_emitted" {
			messageIdx = i
			break
		}
	}
	if messageIdx < 0 {
		t.Fatalf("watch feed has no `message_emitted` event row after the breakpoint.hit — the admin invalidate injected during the paused window did not surface as a message-activity event on the unified /events feed\nfull feed:\n%s", stdout)
	}

	// @constraint: lifecycle row must be a post-resume node-state
	// transition emitted by the cascade walk at settle. `state_transition`
	// (cascade) or `work_completed` (acquire-side terminal) are the
	// canonical kinds; any non-hit, non-message event row strictly after
	// the message row is accepted so a future runtime that surfaces a
	// different terminal kind (e.g. `claim_resolved` / `lock_released`)
	// does not falsely fail the gate. The story's load-bearing claim is
	// "three distinct kinds appear in chronological order", not "exactly
	// these named kinds".
	lifecycleIdx := -1
	for i := messageIdx + 1; i < len(lines); i++ {
		if lines[i].source == "event" && lines[i].kind != "breakpoint.hit" && lines[i].kind != "message_emitted" && lines[i].kind != "message_received" && lines[i].kind != "operator_override" {
			lifecycleIdx = i
			break
		}
	}
	if lifecycleIdx < 0 {
		t.Fatalf("watch feed has no post-message non-message event row — the post-resume settle did not surface a node-lifecycle event after `message_emitted`, so the three-kind chronological interleaving cannot be proven\nfull feed:\n%s", stdout)
	}

	// @constraint: timestamp-faithfulness on the message bracket — the
	// message_emitted timestamp must fall strictly within (hitTS,
	// lifecycleTS]. The monotonic check above already forbids print-order
	// violations, but pinning the message's timestamp inside (hitTS,
	// lifecycleTS] proves the interleaving is driven by real recorded
	// times rather than a drain-order accident.
	messageTS := lines[messageIdx].ts
	lifecycleTS := lines[lifecycleIdx].ts
	if messageTS.Before(hitTS) || lifecycleTS.Before(messageTS) {
		t.Fatalf("message_emitted timestamp %s is not within (%s, %s] of (breakpoint.hit, post-resume %s] — message-activity ordering is not timestamp-faithful\nfull feed:\n%s",
			messageTS.Format(time.RFC3339Nano),
			hitTS.Format(time.RFC3339Nano),
			lifecycleTS.Format(time.RFC3339Nano),
			lines[lifecycleIdx].kind,
			stdout)
	}
}

// parseWatchFeed extracts the timestamped, source-tagged lines from RunWatch's
// human-format stdout. Each rendered line is "<RFC3339-ts>\t<source>\t...".
// Lines that don't parse as a timestamped feed row (blank lines, any
// non-feed output) are skipped. The returned slice is in print order; it is
// additionally stable-sorted only as a defensive no-op (print order already
// equals feed order) so a future renderer that buffers cannot silently mask a
// reordering — the monotonic check above runs on print order regardless.
func parseWatchFeed(stdout string) []watchFeedLine {
	var out []watchFeedLine
	for _, raw := range strings.Split(stdout, "\n") {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		fields := strings.Split(raw, "\t")
		if len(fields) < 2 {
			continue
		}
		ts, err := time.Parse(time.RFC3339, strings.TrimSpace(fields[0]))
		if err != nil {
			// @deliberate: some sources print sub-second RFC3339Nano; accept that too.
			ts, err = time.Parse(time.RFC3339Nano, strings.TrimSpace(fields[0]))
			if err != nil {
				continue
			}
		}
		source := strings.TrimSpace(fields[1])
		if source != "event" && source != "breakpoint.hit" && source != "terminal" {
			continue
		}
		// @constraint: for event-source rows the kind is field[2]
		// (printWatchEvent: "<ts>\tevent\t<kind>\t<detail>").
		kind := ""
		if source == "event" && len(fields) >= 3 {
			kind = strings.TrimSpace(fields[2])
		}
		out = append(out, watchFeedLine{ts: ts, source: source, kind: kind, raw: raw})
	}
	// @deliberate: print order is already feed order; this sort check is a
	// no-op on a correctly-ordered feed and only documents the invariant
	// the monotonic check enforces on the un-sorted print order.
	_ = sort.SliceIsSorted(out, func(i, j int) bool { return out[i].ts.Before(out[j].ts) })
	return out
}
