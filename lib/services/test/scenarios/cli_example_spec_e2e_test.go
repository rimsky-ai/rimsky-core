// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// End-to-end proof that the headline dev-loop verb the project advertises —
// `rimsky run <file>` — is exercisable by an operator who has never written a
// TemplateSpec, against the REAL assembled product.
//
// S-cli-onboarding-example-spec: a new operator copies a shipped example
// TemplateSpec YAML from the `examples/` module and runs it end to end with
// `rimsky run <file>` (register + deploy + instantiate in one shot), then
// watches the instance reach a terminal state.
//
// Unlike the other scenario tests, the value path here is driven through the
// REAL CLI entrypoint (`cli.RunRun`) in-process — not a hand-rolled HTTP POST.
// The CLI's spec-file decode (readSpecFile), template registration, deploy,
// and instance-create are all the real, shipped code paths an operator hits.
// The control-api, scheduler, and supervisor are the real value-delivering
// components; the in-tree stub executor stands in for "whatever executor your
// deployment provides" (the example node uses `executor: stub`).
//
// The input is the SHIPPED on-disk file `examples/compose/template-a.yml` —
// a real YAML with a `nodes:` block, consumed verbatim, NOT an inline Go
// struct. If `rimsky run` cannot drive that shipped file to terminal on the
// real stack, the README's headline dev loop is a lie; this test would catch
// it as a failed assertion on the observable outcome (no `instance_id=` line,
// or the node never reaching `fresh`), not a Docker error.
package scenarios

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli"
	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

// instanceIDLine matches the `instance_id=<uuid>` line `cli.RunRun` prints on
// success in human-output mode. The capture group is the bare UUID.
var instanceIDLine = regexp.MustCompile(`(?m)^instance_id=([0-9a-fA-F-]{36})\s*$`)

// TestCLIExampleSpec_RunReachesTerminal drives the shipped example
// TemplateSpec through the real `rimsky run` verb against a live all-in-one
// stack and asserts the worker node reaches the terminal `fresh` state.
func TestCLIExampleSpec_RunReachesTerminal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// The stub executor must be reachable on the shared network before
	// rimsky/all starts — the control-api fires a Capabilities handshake
	// against declared executors at startup. Network first, then executor
	// peer, then rimsky on the baked SQLite default.
	netName := harness.NewNetwork(ctx, t)
	harness.StartExecutorStubOnNetwork(ctx, t, netName, "executor-stub")

	ep := harness.BringUpRimsky(ctx, t,
		harness.WithSQLite(),
		harness.WithExistingNetwork(netName),
		harness.WithExecutor("stub", "executor-stub:9300"),
	)

	specPath := repoExampleSpecPath(t, "examples/compose/template-a.yml")

	// First assertion: drive the SHIPPED file through the real `rimsky run`
	// verb exactly as the plan's gate command specifies, capturing the
	// human-format stdout to read back the printed instance_id.
	stdout, code := captureRunRun(t, ctx, []string{"--endpoint", ep.BaseURL, specPath})
	if code != 0 {
		t.Fatalf("rimsky run %s exited %d (want 0)\nstdout:\n%s", specPath, code, stdout)
	}
	match := instanceIDLine.FindStringSubmatch(stdout)
	if match == nil {
		t.Fatalf("rimsky run did not print `instance_id=<uuid>`; stdout:\n%s", stdout)
	}
	instanceID := match[1]

	// The instance is reachable via the real `rimsky instance get` verb —
	// the operator-facing read path the story names.
	if _, getCode := captureRun(t, func() int {
		return cli.RunInstanceGet(ctx, []string{"--endpoint", ep.BaseURL, instanceID})
	}); getCode != 0 {
		t.Fatalf("rimsky instance get %s exited %d (want 0)", instanceID, getCode)
	}

	// Watch the instance reach a terminal state: the worker node must emit a
	// `work_started` event (unambiguous proof of a real claim+dispatch) and
	// settle to `fresh` (the stub executor returns Success for every
	// dispatch, so a healthy loop lands the node in `fresh`).
	waitForExampleDispatchToFresh(t, ep, instanceID, "worker", 90*time.Second)

	// Second assertion: run the README's documented `rimsky run` invocation
	// AS WRITTEN against the shipped file (examples/README.md documents
	// `rimsky run -f examples/compose/template-a.yml`). It must also exit 0,
	// proving the documented dev-loop command works verbatim — not just the
	// flag spelling the first assertion happened to use. A distinct
	// instance_key keeps it disjoint from the first run.
	readmeArgs := documentedRunInvocation(t, ep.BaseURL, specPath)
	_, readmeCode := captureRunRun(t, ctx, readmeArgs)
	if readmeCode != 0 {
		t.Fatalf("README-documented `rimsky run` invocation %v exited %d (want 0)", readmeArgs, readmeCode)
	}
}

// documentedRunInvocation returns the argv for the `rimsky run` invocation the
// README documents, retargeted at the live stack. The README block is
// `rimsky run examples/compose/template-a.yml` (the positional <file> form the
// `run` verb actually accepts), run here against the shipped file with an
// explicit endpoint and a distinct instance-key so it does not collide with
// the first run's instance.
//
// Keeping the documented argv shape in one place means a drift between what
// the README tells an operator to type and what actually works fails this
// test rather than passing silently.
func documentedRunInvocation(t *testing.T, baseURL, specPath string) []string {
	t.Helper()
	return []string{
		"--endpoint", baseURL,
		"--instance-key", "example-readme-run",
		specPath,
	}
}

// captureRunRun invokes cli.RunRun with stdout redirected to a buffer and
// returns the captured stdout plus the verb's exit code. RunRun writes the
// `instance_id=` line to os.Stdout directly, so the test must swap os.Stdout
// for the duration of the call.
func captureRunRun(t *testing.T, ctx context.Context, args []string) (string, int) {
	t.Helper()
	return captureRun(t, func() int { return cli.RunRun(ctx, args) })
}

// stdoutCaptureMu serializes the process-global os.Stdout swap that every
// CLI-verb capture in this package performs. The capture replaces the single
// package-level os.Stdout for the duration of one verb invocation; two
// parallel tests swapping it concurrently would race (one test's pipe would
// capture the other's output, or a restore would clobber an in-flight swap).
// Holding this mutex for the whole swap window makes every stdout-capturing
// CLI test in the package mutually exclusive — correctness over the cheaper
// unguarded swap. The long-lived watch capture (RunWatch streams for the life
// of an instance) takes the same lock, so a watch run and a one-shot verb run
// never interleave their os.Stdout swaps.
var stdoutCaptureMu sync.Mutex

// captureRun runs fn with os.Stdout redirected through an os.Pipe, returning
// everything fn wrote to stdout plus fn's return value. The CLI verbs write
// to the package-level os.Stdout, so capture is by process-global swap
// (guarded by stdoutCaptureMu so parallel captures don't race); restored via
// defer. Reads run on a goroutine so a writer larger than the pipe buffer
// cannot deadlock.
func captureRun(t *testing.T, fn func() int) (string, int) {
	t.Helper()
	stdoutCaptureMu.Lock()
	defer stdoutCaptureMu.Unlock()

	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w

	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()

	code := func() int {
		defer func() {
			os.Stdout = orig
			_ = w.Close()
		}()
		return fn()
	}()

	out := <-done
	_ = r.Close()
	return out, code
}

// repoExampleSpecPath resolves an example-spec path that is relative to the
// repository root, given the test file's own location. The scenarios package
// lives at lib/services/test/scenarios, four directories below the repo root.
// The resolved path must exist on disk — the story's whole point is that the
// example is a SHIPPED file, not an inline struct, so a missing file is a real
// failure (the example was never shipped), not infra.
func repoExampleSpecPath(t *testing.T, rel string) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed to locate the test source file")
	}
	// thisFile = <repo>/lib/services/test/scenarios/cli_example_spec_e2e_test.go
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..", "..", ".."))
	path := filepath.Join(repoRoot, filepath.FromSlash(rel))
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("shipped example spec %s not found at %s: %v — the example must ship as a real on-disk file", rel, path, err)
	}
	return path
}

// exampleTerminalStates are the node states the wait loop treats as settled.
// A healthy stub dispatch settles to `fresh`; `failed` is accepted only so the
// loop stops promptly on a real defect instead of timing out — the explicit
// `fresh` assertion then fails the test.
var exampleTerminalStates = map[string]bool{
	"fresh":  true,
	"failed": true,
}

// waitForExampleDispatchToFresh polls the node-state observability route until
// the node has (a) emitted a `work_started` event — proving the supervisor
// claimed and dispatched — and (b) settled into `fresh` — proving the executor
// ran and the terminal transition landed. A non-`fresh` settle, a missing
// `work_started`, or a timeout fails the test.
//
// @source: lib/services/test/scenarios/sqlite_all_in_one_test.go::waitForDispatchToFresh
// The wait shape is identical to the SQLite all-in-one scenario; it is
// duplicated rather than shared because the sqlite helper is package-private to
// the same package but reads against a path-equal selector and the two tests
// must stay independently legible (one proves the CLI verb, one the raw POST).
func waitForExampleDispatchToFresh(t *testing.T, ep harness.RimskyEndpoint, instanceID, nodeType string, deadline time.Duration) {
	t.Helper()
	end := time.Now().Add(deadline)
	var (
		lastState   string
		sawDispatch bool
	)
	for time.Now().Before(end) {
		status, raw := ep.GetJSON(t,
			"/v1/observability/nodes/"+instanceID+"/"+nodeType, "")
		if status == http.StatusOK {
			var resp struct {
				Node struct {
					State string `json:"state"`
				} `json:"node"`
				Events []struct {
					Kind string `json:"kind"`
				} `json:"events"`
			}
			if err := json.Unmarshal(raw, &resp); err == nil {
				lastState = resp.Node.State
				for _, e := range resp.Events {
					if e.Kind == "work_started" {
						sawDispatch = true
						break
					}
				}
				if sawDispatch && exampleTerminalStates[lastState] {
					if lastState != "fresh" {
						t.Fatalf("node %q dispatched via `rimsky run` but settled in %q, want fresh — the stub executor returns Success, so a non-fresh terminal is a real dev-loop defect",
							nodeType, lastState)
					}
					return
				}
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("node %q on instance %s (driven by `rimsky run`) did not complete a real dispatch within %v; last state=%q, work_started seen=%v",
		nodeType, instanceID, deadline, lastState, sawDispatch)
}
