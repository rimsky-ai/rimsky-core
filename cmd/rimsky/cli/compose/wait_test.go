// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// wait_test.go — coverage for the terminal-wait poll loop. The fake
// instanceClient lets the tests script a per-poll state machine
// without spinning up a control-api stub: each GetInstance returns
// the next scripted response in order, ListInstanceNodes serves the
// node roster for the current step.
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

// fakeInstanceClient is a poll-step-driven fake. Scripts per id a
// list of (instance, nodes) responses; each call advances the cursor.
// The fake holds a mutex so the wait loop can poll multiple ids
// concurrently if a future implementation does, though today's
// implementation is sequential per tick.
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

func termTime() *string {
	s := time.Now().UTC().Format(time.RFC3339Nano)
	return &s
}

// nopPrinter is a silent ProgressPrinter for tests that only care
// about the wait loop's return value. The production WaitForInstances
// Terminal contract requires a non-nil printer; tests that do not
// inspect printer output use this stub to satisfy the contract.
type nopPrinter struct{}

func (nopPrinter) InstanceStarting(project, name string)                         {}
func (nopPrinter) NodeRunTerminal(project, name, nodeID, outcome, reason string) {}
func (nopPrinter) InstanceTerminal(project, name, outcome string, frames int)    {}
func (nopPrinter) FrameTick(project, name string, frameNo int)                   {}
func (nopPrinter) Finalize()                                                     {}

// TestWaitForInstancesTerminal_ReturnsOnAllTerminal exercises the
// happy path: two instances start as running, flip to terminal on
// the second poll, and the function returns with the success outcome
// for each. The test uses an aggressively short poll interval so the
// loop completes inside the test budget without depending on a real
// timer.
func TestWaitForInstancesTerminal_ReturnsOnAllTerminal(t *testing.T) {
	client := newFakeClient()
	client.script("a", fakeFrame{inst: cli.Instance{ID: "a"}, nodes: []cli.Node{{ID: "a-n1", State: "running"}}})
	client.script("a", fakeFrame{inst: cli.Instance{ID: "a", TerminatedAt: termTime()}, nodes: []cli.Node{{ID: "a-n1", State: "success"}}})
	client.script("b", fakeFrame{inst: cli.Instance{ID: "b"}, nodes: []cli.Node{{ID: "b-n1", State: "running"}}})
	client.script("b", fakeFrame{inst: cli.Instance{ID: "b", TerminatedAt: termTime()}, nodes: []cli.Node{{ID: "b-n1", State: "success"}}})

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

// TestWaitForInstancesTerminal_CallsPrinter checks the printer
// integration: every instance gets an InstanceStarting at the top
// and an InstanceTerminal on completion, and every terminal node
// emits one NodeRunTerminal call. The default printer's output is
// the verification surface — a bytes.Buffer captures each line so
// the test can assert on the prose shape directly.
func TestWaitForInstancesTerminal_CallsPrinter(t *testing.T) {
	client := newFakeClient()
	client.script("a", fakeFrame{inst: cli.Instance{ID: "a"}, nodes: []cli.Node{{ID: "a-n1", State: "running"}}})
	client.script("a", fakeFrame{inst: cli.Instance{ID: "a", TerminatedAt: termTime()}, nodes: []cli.Node{{ID: "a-n1", State: "failed", CurrentErrorClass: "boom"}}})

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
	if !contains(out, "instance proj/alpha: failure (frames=1)") {
		t.Errorf("missing InstanceTerminal line; output = %q", out)
	}
}

// TestWaitForInstancesTerminal_ContextCancelExits proves the loop
// honors ctx cancellation: a context cancelled after the wait
// starts (but before the instance reaches terminal) returns
// ctx.Err() rather than spinning. The falsifier this rules out is a
// poll-loop hang on a never-terminal instance.
func TestWaitForInstancesTerminal_ContextCancelExits(t *testing.T) {
	client := newFakeClient()
	client.script("a", fakeFrame{inst: cli.Instance{ID: "a"}, nodes: []cli.Node{{ID: "a-n1", State: "running"}}})

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

// TestWaitForInstancesTerminal_EmptyRosterReturnsImmediately covers
// the degenerate case: a manifest with zero instances. The verb
// should still complete (returning an empty outcomes map with nil
// error) so a script-friendly run on a "do nothing" manifest does
// not hang.
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

// transientNodesErrorClient wraps fakeInstanceClient and fails the
// FIRST ListInstanceNodes call after an instance flips to terminal;
// subsequent calls return the scripted node roster. Used to pin that
// the wait loop does not promote a transient-degraded read into
// OutcomeSuccess: a failed instance with a one-tick nodes-call blip
// must still surface as OutcomeFailure once the next tick succeeds.
type transientNodesErrorClient struct {
	*fakeInstanceClient
	mu      sync.Mutex
	failed  map[string]bool
	errOnce error
}

func (c *transientNodesErrorClient) ListInstanceNodes(ctx context.Context, id string) (*cli.ListInstanceNodesResponse, error) {
	c.mu.Lock()
	// @deliberate: fail-once-on-terminal-frame — the transient ListInstanceNodes
	// blip is injected only when the fake's cursor sits on the last scripted
	// frame (the terminal frame). This is the exact tick the wait loop would
	// otherwise use to read terminal nodes, so failing here pins the rule that
	// a one-tick nodes-call error must not silently promote OutcomeFailure to
	// OutcomeSuccess.
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

// TestWaitForInstancesTerminal_TransientNodesErrorPreservesOutcome
// pins the rule the @decision: exit-codes contract relies on: a
// transient ListInstanceNodes error on the same tick that observes
// terminated_at must NOT silently promote a failed instance to
// success. The loop should retry on the next tick and surface the
// real outcome — OutcomeFailure for an instance whose only node
// failed.
func TestWaitForInstancesTerminal_TransientNodesErrorPreservesOutcome(t *testing.T) {
	base := newFakeClient()
	base.script("a", fakeFrame{inst: cli.Instance{ID: "a"}, nodes: []cli.Node{{ID: "a-n1", State: "running"}}})
	base.script("a", fakeFrame{inst: cli.Instance{ID: "a", TerminatedAt: termTime()}, nodes: []cli.Node{{ID: "a-n1", State: "failed", CurrentErrorClass: "boom"}}})

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

// TestAnyOutcomeFailed pins the classifier the verb's main loop
// uses to map outcomes to ReasonAllSuccess vs ReasonAnyFailure.
// A failure or parked-timeout marks the run as failure for the
// exit-code table per @decision: exit-codes.
func TestAnyOutcomeFailed(t *testing.T) {
	if AnyOutcomeFailed(map[string]string{"a": OutcomeSuccess}) {
		t.Error("all-success map should not be failed")
	}
	if !AnyOutcomeFailed(map[string]string{"a": OutcomeSuccess, "b": OutcomeFailure}) {
		t.Error("any-failure map should be failed")
	}
	if !AnyOutcomeFailed(map[string]string{"a": OutcomeParkedTimeout}) {
		t.Error("parked-timeout should count as failed")
	}
}

// TestClassifyWaitErr verifies the wait-err → ShutdownReason map.
// Each branch lines up with one row of @decision: exit-codes; a
// regression that drops one branch would silently change exit
// behavior on signal or timeout.
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
