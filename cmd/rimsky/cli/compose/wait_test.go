// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package compose

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli"
	"github.com/rimsky-ai/rimsky-core/test/support/awaited"
)

type fakeInstanceClient struct {
	mu     sync.Mutex
	frames map[string][]fakeFrame
	idx    map[string]int
	served map[string]int
	polls  int
}

func (f *fakeInstanceClient) pollCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.polls
}

type fakeFrame struct {
	inst    cli.Instance
	nodes   []cli.Node
	frameID string
}

func newFakeClient() *fakeInstanceClient {
	return &fakeInstanceClient{
		frames: map[string][]fakeFrame{},
		idx:    map[string]int{},
		served: map[string]int{},
	}
}

func (f *fakeInstanceClient) script(id string, frame fakeFrame) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.frames[id] = append(f.frames[id], frame)
}

func (f *fakeInstanceClient) ListInstanceNodes(ctx context.Context, id string, q cli.ListNodesQuery) (*cli.ListInstanceNodesResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.polls++
	frames, ok := f.frames[id]
	if !ok || len(frames) == 0 {
		return &cli.ListInstanceNodesResponse{Nodes: nil}, nil
	}
	i := f.idx[id]
	if i >= len(frames) {
		i = len(frames) - 1
	}
	f.served[id] = i
	if i < len(frames)-1 {
		f.idx[id] = i + 1
	}
	return &cli.ListInstanceNodesResponse{Nodes: append([]cli.Node(nil), frames[i].nodes...)}, nil
}

func (f *fakeInstanceClient) ListInstanceFrames(ctx context.Context, id, state string) (*cli.ListFramesResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	frames, ok := f.frames[id]
	if !ok || len(frames) == 0 {
		return &cli.ListFramesResponse{}, nil
	}
	i := f.served[id]
	if i >= len(frames) {
		i = len(frames) - 1
	}
	if allNodesSettled(frames[i].nodes) {
		return &cli.ListFramesResponse{}, nil
	}
	frameID := frames[i].frameID
	if frameID == "" {
		frameID = "fake-frame"
	}
	return &cli.ListFramesResponse{Frames: []cli.FrameItem{{FrameID: frameID, State: "running"}}}, nil
}

func (f *fakeInstanceClient) ListInstanceMessages(ctx context.Context, id string, q cli.ListMessagesQuery) (*cli.ListMessagesResponse, error) {
	return &cli.ListMessagesResponse{}, nil
}

func (f *fakeInstanceClient) TerminateInstance(_ context.Context, id string, _ string) (*cli.Instance, error) {
	return &cli.Instance{ID: id}, nil
}

func termTime() *string {
	s := time.Now().UTC().Format(time.RFC3339Nano)
	return &s
}

type nopPrinter struct{}

func (nopPrinter) InstanceStarting(project, name string)                         {}
func (nopPrinter) NodeRunTerminal(project, name, nodeID, outcome, reason string) {}
func (nopPrinter) InstanceTerminal(project, name, outcome string, nodeCount int) {}
func (nopPrinter) FrameTick(project, name, frameID string, frameNo int)          {}
func (nopPrinter) Summary(verb, reason string, instanceCount int)                {}
func (nopPrinter) Finalize()                                                     {}

func TestWaitForInstancesTerminal_ReturnsOnAllTerminal(t *testing.T) {
	client := newFakeClient()
	client.script("a", fakeFrame{inst: cli.Instance{ID: "a"}, nodes: []cli.Node{{ID: "a-n1", RunSummary: &cli.NodeRunSummary{ActiveCount: 1}}}})
	client.script("a", fakeFrame{inst: cli.Instance{ID: "a", TerminatedAt: termTime()}, nodes: []cli.Node{{ID: "a-n1", RunSummary: &cli.NodeRunSummary{FreshCount: 1}}}})
	client.script("b", fakeFrame{inst: cli.Instance{ID: "b"}, nodes: []cli.Node{{ID: "b-n1", RunSummary: &cli.NodeRunSummary{ActiveCount: 1}}}})
	client.script("b", fakeFrame{inst: cli.Instance{ID: "b", TerminatedAt: termTime()}, nodes: []cli.Node{{ID: "b-n1", RunSummary: &cli.NodeRunSummary{FreshCount: 1}}}})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	keys := map[string]string{"a": "first", "b": "second"}
	outcomes, err := WaitForInstancesTerminal(ctx, client, []string{"a", "b"}, "proj", keys, nopPrinter{}, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("WaitForInstancesTerminal: %v", err)
	}
	if got, want := outcomes["a"], OutcomeSuccess; got != want {
		t.Errorf("outcomes[a] = %q, want %q", got, want)
	}
	if got, want := outcomes["b"], OutcomeSuccess; got != want {
		t.Errorf("outcomes[b] = %q, want %q", got, want)
	}
}

func TestWaitForInstancesTerminal_CallsPrinter(t *testing.T) {
	client := newFakeClient()
	client.script("a", fakeFrame{inst: cli.Instance{ID: "a"}, nodes: []cli.Node{{ID: "a-n1", RunSummary: &cli.NodeRunSummary{ActiveCount: 1}}}})
	client.script("a", fakeFrame{inst: cli.Instance{ID: "a", TerminatedAt: termTime()}, nodes: []cli.Node{{ID: "a-n1", RunSummary: &cli.NodeRunSummary{FailedCount: 1}, SettlingSignalType: "boom"}}})

	var buf bytes.Buffer
	printer := newProgressPrinter(&buf, false, false, false)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	outcomes, err := WaitForInstancesTerminal(ctx, client, []string{"a"}, "proj", map[string]string{"a": "alpha"}, printer, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("WaitForInstancesTerminal: %v", err)
	}
	if outcomes["a"] != OutcomeFailure {
		t.Errorf("outcomes[a] = %q, want %q", outcomes["a"], OutcomeFailure)
	}
	out := buf.String()
	if !contains(out, "instance proj/alpha: tracking") {
		t.Errorf("missing InstanceStarting line; output = %q", out)
	}
	if !contains(out, "instance proj/alpha node a-n1: failure (boom)") {
		t.Errorf("missing NodeRunTerminal line with reason; output = %q", out)
	}
	if !contains(out, "instance proj/alpha: failure (nodes=1)") {
		t.Errorf("missing InstanceTerminal line; output = %q", out)
	}
}

func TestWaitForInstancesTerminal_ContextCancelExits(t *testing.T) {
	client := newFakeClient()
	client.script("a", fakeFrame{inst: cli.Instance{ID: "a"}, nodes: []cli.Node{{ID: "a-n1", RunSummary: &cli.NodeRunSummary{ActiveCount: 1}}}})

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		_, err := WaitForInstancesTerminal(ctx, client, []string{"a"}, "proj", nil, nopPrinter{}, 10*time.Millisecond)
		done <- err
	}()
	awaited.Until(t, "the wait loop to poll the instance at least once, so the cancel lands mid-wait",
		func() bool { return client.pollCount() > 0 })
	cancel()

	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("WaitForInstancesTerminal err = %v, want context.Canceled", err)
	}
}

func TestWaitForInstancesTerminal_EmptyRosterReturnsImmediately(t *testing.T) {
	client := newFakeClient()
	outcomes, err := WaitForInstancesTerminal(context.Background(), client, nil, "proj", nil, nopPrinter{}, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("WaitForInstancesTerminal on empty roster: %v", err)
	}
	if len(outcomes) != 0 {
		t.Errorf("outcomes len = %d, want 0", len(outcomes))
	}
}

type transientNodesErrorClient struct {
	*fakeInstanceClient
	mu      sync.Mutex
	failed  map[string]bool
	errOnce error
}

func (c *transientNodesErrorClient) ListInstanceNodes(ctx context.Context, id string, q cli.ListNodesQuery) (*cli.ListInstanceNodesResponse, error) {
	c.mu.Lock()
	frames := c.fakeInstanceClient.frames[id]
	cursor := c.fakeInstanceClient.idx[id]
	terminalFrame := cursor == len(frames)-1 && frames[cursor].inst.TerminatedAt != nil
	if terminalFrame && !c.failed[id] {
		c.failed[id] = true
		c.mu.Unlock()
		return nil, c.errOnce
	}
	c.mu.Unlock()
	return c.fakeInstanceClient.ListInstanceNodes(ctx, id, q)
}

func TestWaitForInstancesTerminal_TransientNodesErrorPreservesOutcome(t *testing.T) {
	base := newFakeClient()
	base.script("a", fakeFrame{inst: cli.Instance{ID: "a"}, nodes: []cli.Node{{ID: "a-n1", RunSummary: &cli.NodeRunSummary{ActiveCount: 1}}}})
	base.script("a", fakeFrame{inst: cli.Instance{ID: "a", TerminatedAt: termTime()}, nodes: []cli.Node{{ID: "a-n1", RunSummary: &cli.NodeRunSummary{FailedCount: 1}}}})

	client := &transientNodesErrorClient{
		fakeInstanceClient: base,
		failed:             map[string]bool{},
		errOnce:            errors.New("simulated transient list-nodes error"),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	outcomes, err := WaitForInstancesTerminal(ctx, client, []string{"a"}, "proj", map[string]string{"a": "alpha"}, nopPrinter{}, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("WaitForInstancesTerminal: %v", err)
	}
	if got, want := outcomes["a"], OutcomeFailure; got != want {
		t.Errorf("outcomes[a] = %q, want %q (transient nodes-error must not silently promote failure to success)", got, want)
	}
}

type zeroRunNodeClient struct {
	mu   sync.Mutex
	poll int
}

func (c *zeroRunNodeClient) ListInstanceNodes(_ context.Context, _ string, _ cli.ListNodesQuery) (*cli.ListInstanceNodesResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.poll++
	main := cli.Node{ID: "main", RunSummary: &cli.NodeRunSummary{ActiveCount: 1}}
	if c.poll >= 2 {
		main = cli.Node{ID: "main", RunSummary: &cli.NodeRunSummary{FreshCount: 1}}
	}
	receiver := cli.Node{ID: "receiver", RunSummary: &cli.NodeRunSummary{}}
	return &cli.ListInstanceNodesResponse{Nodes: []cli.Node{main, receiver}}, nil
}

func (c *zeroRunNodeClient) ListInstanceFrames(_ context.Context, _, _ string) (*cli.ListFramesResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.poll < 2 {
		return &cli.ListFramesResponse{Frames: []cli.FrameItem{{FrameID: "f1", State: "running"}}}, nil
	}
	return &cli.ListFramesResponse{}, nil
}

func (c *zeroRunNodeClient) ListInstanceMessages(_ context.Context, _ string, _ cli.ListMessagesQuery) (*cli.ListMessagesResponse, error) {
	return &cli.ListMessagesResponse{}, nil
}

func (c *zeroRunNodeClient) TerminateInstance(_ context.Context, id, _ string) (*cli.Instance, error) {
	return &cli.Instance{ID: id}, nil
}

// @story: one-shot-to-terminal
func TestWaitForInstancesTerminal_ZeroRunNodeDoesNotBlockSettlement(t *testing.T) {
	client := &zeroRunNodeClient{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	outcomes, err := WaitForInstancesTerminal(ctx, client, []string{"a"}, "proj", nil, nopPrinter{}, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("WaitForInstancesTerminal: %v (a permanently-zero-run declared node must not block settlement once the instance has no open frame and no pending message)", err)
	}
	if got, want := outcomes["a"], OutcomeSuccess; got != want {
		t.Errorf("outcomes[a] = %q, want %q", got, want)
	}
}

func TestAnyOutcomeFailed(t *testing.T) {
	if AnyOutcomeFailed(map[string]string{"a": OutcomeSuccess}) {
		t.Error("all-success map should not be failed")
	}
	if !AnyOutcomeFailed(map[string]string{"a": OutcomeSuccess, "b": OutcomeFailure}) {
		t.Error("any-failure map should be failed")
	}
}

func TestNextWaitPollInterval(t *testing.T) {
	base := DefaultWaitPollInterval
	interval := base
	for tick := 1; tick < waitPollBackoffAfter; tick++ {
		interval = nextWaitPollInterval(tick, interval)
		if interval != base {
			t.Fatalf("tick %d: interval = %v, want unchanged %v before the backoff threshold", tick, interval, base)
		}
	}

	interval = nextWaitPollInterval(waitPollBackoffAfter, interval)
	if interval != base*2 {
		t.Fatalf("tick %d: interval = %v, want %v (first doubling at the threshold)", waitPollBackoffAfter, interval, base*2)
	}

	for interval < maxWaitPollInterval {
		prev := interval
		interval = nextWaitPollInterval(waitPollBackoffAfter+1, interval)
		if interval <= prev {
			t.Fatalf("interval did not grow: prev=%v got=%v", prev, interval)
		}
	}
	if interval != maxWaitPollInterval {
		t.Fatalf("interval = %v, want it to converge to the cap %v", interval, maxWaitPollInterval)
	}

	capped := nextWaitPollInterval(waitPollBackoffAfter+10, interval)
	if capped != maxWaitPollInterval {
		t.Fatalf("interval past the cap = %v, want it to stay at %v", capped, maxWaitPollInterval)
	}
}

func TestAllNodesSettled(t *testing.T) {
	if !allNodesSettled(nil) {
		t.Error("allNodesSettled(nil) should be vacuously true (no nodes means none are unsettled)")
	}
	if !allNodesSettled([]cli.Node{}) {
		t.Error("allNodesSettled(empty slice) should be vacuously true")
	}
	settled := []cli.Node{{ID: "a", RunSummary: &cli.NodeRunSummary{FreshCount: 1}}}
	if !allNodesSettled(settled) {
		t.Error("allNodesSettled should be true when every node is settled")
	}
	unsettled := []cli.Node{
		{ID: "a", RunSummary: &cli.NodeRunSummary{FreshCount: 1}},
		{ID: "b", RunSummary: &cli.NodeRunSummary{ActiveCount: 1}},
	}
	if allNodesSettled(unsettled) {
		t.Error("allNodesSettled should be false when any node is still active")
	}
}

func TestClassifyWaitErr(t *testing.T) {
	if got := classifyWaitErr(nil); got != ReasonAllSuccess {
		t.Errorf("classifyWaitErr(nil) = %v, want ReasonAllSuccess", got)
	}
	if got := classifyWaitErr(context.Canceled); got != ReasonSignal {
		t.Errorf("classifyWaitErr(Canceled) = %v, want ReasonSignal", got)
	}
	if got := classifyWaitErr(context.DeadlineExceeded); got != ReasonTimeout {
		t.Errorf("classifyWaitErr(DeadlineExceeded) = %v, want ReasonTimeout", got)
	}
	if got := classifyWaitErr(errors.New("other")); got != ReasonAnyFailure {
		t.Errorf("classifyWaitErr(other) = %v, want ReasonAnyFailure", got)
	}
}

func TestBootFailureReason(t *testing.T) {
	live, cancel := context.WithCancel(context.Background())
	defer cancel()
	if got := bootFailureReason(live); got != ReasonAnyFailure {
		t.Errorf("bootFailureReason(live ctx) = %v, want ReasonAnyFailure", got)
	}

	canceled, cancelNow := context.WithCancel(context.Background())
	cancelNow()
	if got := bootFailureReason(canceled); got != ReasonSignal {
		t.Errorf("bootFailureReason(canceled ctx) = %v, want ReasonSignal (a client-call error during a signaled boot window must be reported as an interrupt, not a generic failure)", got)
	}
}

func contains(haystack, needle string) bool {
	return bytes.Contains([]byte(haystack), []byte(needle))
}

// @decision: progress-flags
// @story: live-progress
func TestWaitReportsEachNewlyObservedFrameAsATick(t *testing.T) {
	client := newFakeClient()
	running := []cli.Node{{ID: "a-n1", RunSummary: &cli.NodeRunSummary{ActiveCount: 1}}}
	settled := []cli.Node{{ID: "a-n1", RunSummary: &cli.NodeRunSummary{FreshCount: 1}}}
	client.script("a", fakeFrame{inst: cli.Instance{ID: "a"}, nodes: running, frameID: "frame-one"})
	client.script("a", fakeFrame{inst: cli.Instance{ID: "a"}, nodes: running, frameID: "frame-two"})
	client.script("a", fakeFrame{inst: cli.Instance{ID: "a", TerminatedAt: termTime()}, nodes: settled})

	var verboseBuf, defaultBuf bytes.Buffer
	keys := map[string]string{"a": "alpha"}

	if _, err := WaitForInstancesTerminal(context.Background(), client, []string{"a"}, "proj", keys,
		newProgressPrinter(&verboseBuf, false, true, false), time.Millisecond); err != nil {
		t.Fatalf("WaitForInstancesTerminal: %v", err)
	}

	out := verboseBuf.String()
	if !contains(out, "instance proj/alpha frame 1 (frame-one)") {
		t.Errorf("the first observed frame produced no tick naming its id; output = %q", out)
	}
	if !contains(out, "instance proj/alpha frame 2 (frame-two)") {
		t.Errorf("the second observed frame produced no tick naming its id; output = %q", out)
	}
	if got := strings.Count(out, "instance proj/alpha frame "); got != 2 {
		t.Errorf("two frames were observed but the loop emitted %d ticks; output = %q", got, out)
	}

	client2 := newFakeClient()
	client2.script("a", fakeFrame{inst: cli.Instance{ID: "a"}, nodes: running, frameID: "frame-one"})
	client2.script("a", fakeFrame{inst: cli.Instance{ID: "a"}, nodes: running, frameID: "frame-two"})
	client2.script("a", fakeFrame{inst: cli.Instance{ID: "a", TerminatedAt: termTime()}, nodes: settled})
	if _, err := WaitForInstancesTerminal(context.Background(), client2, []string{"a"}, "proj", keys,
		newProgressPrinter(&defaultBuf, false, false, false), time.Millisecond); err != nil {
		t.Fatalf("WaitForInstancesTerminal: %v", err)
	}
	if contains(defaultBuf.String(), "frame ") {
		t.Errorf("the default volume reported a frame tick; output = %q", defaultBuf.String())
	}
}
