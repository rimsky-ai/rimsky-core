// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package compose

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli"
)

type fakeInstanceClient struct {
	mu     sync.Mutex
	frames map[string][]fakeFrame
	idx    map[string]int
}

type fakeFrame struct {
	inst  cli.Instance
	nodes []cli.Node
}

func newFakeClient() *fakeInstanceClient {
	return &fakeInstanceClient{
		frames: map[string][]fakeFrame{},
		idx:    map[string]int{},
	}
}

func (f *fakeInstanceClient) script(id string, frame fakeFrame) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.frames[id] = append(f.frames[id], frame)
}

func (f *fakeInstanceClient) GetInstance(ctx context.Context, id string) (*cli.Instance, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	frames, ok := f.frames[id]
	if !ok || len(frames) == 0 {
		return nil, errors.New("no frames scripted for id " + id)
	}
	i := f.idx[id]
	if i >= len(frames) {
		i = len(frames) - 1
	}
	frame := frames[i]
	if i < len(frames)-1 {
		f.idx[id] = i + 1
	} else {
		f.idx[id] = i
	}
	inst := frame.inst
	return &inst, nil
}

func (f *fakeInstanceClient) ListInstanceNodes(ctx context.Context, id string) (*cli.ListInstanceNodesResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	frames, ok := f.frames[id]
	if !ok || len(frames) == 0 {
		return &cli.ListInstanceNodesResponse{Nodes: nil}, nil
	}
	i := f.idx[id]
	if i >= len(frames) {
		i = len(frames) - 1
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
	i := f.idx[id]
	if i >= len(frames) {
		i = len(frames) - 1
	}
	if allNodesSettled(frames[i].nodes) {
		return &cli.ListFramesResponse{}, nil
	}
	return &cli.ListFramesResponse{Frames: []cli.FrameItem{{FrameID: "fake-frame", State: "running"}}}, nil
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
func (nopPrinter) FrameTick(project, name string, frameNo int)                   {}
func (nopPrinter) Finalize()                                                     {}

func TestWaitForInstancesTerminal_ReturnsOnAllTerminal(t *testing.T) {
	client := newFakeClient()
	client.script("a", fakeFrame{inst: cli.Instance{ID: "a"}, nodes: []cli.Node{{ID: "a-n1", RunSummary: &cli.NodeRunSummary{ActiveCount: 1}}}})
	client.script("a", fakeFrame{inst: cli.Instance{ID: "a", TerminatedAt: termTime()}, nodes: []cli.Node{{ID: "a-n1", RunSummary: &cli.NodeRunSummary{FreshCount: 1}}}})
	client.script("b", fakeFrame{inst: cli.Instance{ID: "b"}, nodes: []cli.Node{{ID: "b-n1", RunSummary: &cli.NodeRunSummary{ActiveCount: 1}}}})
	client.script("b", fakeFrame{inst: cli.Instance{ID: "b", TerminatedAt: termTime()}, nodes: []cli.Node{{ID: "b-n1", RunSummary: &cli.NodeRunSummary{FreshCount: 1}}}})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
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
	printer := newDefaultPrinter(&buf)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
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
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	done := make(chan error, 1)
	go func() {
		_, err := WaitForInstancesTerminal(ctx, client, []string{"a"}, "proj", nil, nopPrinter{}, 10*time.Millisecond)
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("WaitForInstancesTerminal err = %v, want context.Canceled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("WaitForInstancesTerminal did not exit on context cancel")
	}
}

func TestWaitForInstancesTerminal_EmptyRosterReturnsImmediately(t *testing.T) {
	client := newFakeClient()
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	outcomes, err := WaitForInstancesTerminal(ctx, client, nil, "proj", nil, nopPrinter{}, 10*time.Millisecond)
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

func (c *transientNodesErrorClient) ListInstanceNodes(ctx context.Context, id string) (*cli.ListInstanceNodesResponse, error) {
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
	return c.fakeInstanceClient.ListInstanceNodes(ctx, id)
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

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
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

func (c *zeroRunNodeClient) GetInstance(_ context.Context, id string) (*cli.Instance, error) {
	return &cli.Instance{ID: id}, nil
}

func (c *zeroRunNodeClient) ListInstanceNodes(_ context.Context, _ string) (*cli.ListInstanceNodesResponse, error) {
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

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
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

func contains(haystack, needle string) bool {
	return bytes.Contains([]byte(haystack), []byte(needle))
}
