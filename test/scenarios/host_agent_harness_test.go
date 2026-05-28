// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// host_agent_harness_test.go — shared end-to-end scaffolding for the
// host-agent + host-agent-proxy scenario tests. Stands up the real wiring:
//   - the rimsky-host-agent-proxy binary (built once, exec'd as a real gRPC
//     server on a free port, pointed at the harness control-api for the
//     GET /instances binding-cache fallback);
//   - the host-agent daemon in-process via hostagent.Run, dialing the proxy
//     and registering under the owner api-key id;
//   - the stubchild test binary (Executor + ClaimProducer over
//     RIMSKY_AGENT_PORT) as the late-bound service binary the agent exec()s.
//
// The supervisor resolves the late-bound `codegen` executor name through the
// proxy (registered as the `codegen-proxy` executor + lifecycle peer); the
// proxy fetches the instance's service_bindings + owner via control-api,
// finds the connected agent, spawns the stub, and tunnels Execute through to
// it. This is the real dispatch path, not a fake.
package scenarios

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/hostagent"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

// proxyExecutorName is the static executor name the proxy is registered
// under; late_bind_service_proxies maps the executor protocol to it.
const proxyExecutorName = "codegen-proxy"

// lateBindServiceName is the late-bound service the codegen node references
// and the key in the instance's service_bindings catalog.
const lateBindServiceName = "codegen"

// hostAgentFixture bundles the running proxy + agent + stub binary path for
// one scenario.
type hostAgentFixture struct {
	h           *scenario.Harness
	proxyAddr   string // host:port the supervisor dials as the executor endpoint
	stubBinary  string
	adminKey    string // plaintext bearer for authenticated control-api calls
	ownerKeyID  string // the created_by_api_key_id the agent registers under
	cancelAgent context.CancelFunc
	agentDone   chan struct{}
}

// repoRoot finds the module root (the directory containing go.work) by
// walking up from the test's working directory.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.work")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("repoRoot: go.work not found walking up from working dir")
		}
		dir = parent
	}
}

// buildBinary go-builds pkgPath into a temp file and returns its path.
func buildBinary(t *testing.T, pkgPath string) string {
	t.Helper()
	root := repoRoot(t)
	out := filepath.Join(t.TempDir(), filepath.Base(pkgPath))
	cmd := exec.Command("go", "build", "-o", out, "./"+pkgPath)
	cmd.Dir = root
	cmd.Env = os.Environ()
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", pkgPath, err, combined)
	}
	return out
}

// freePort grabs an OS-assigned TCP port and returns it. The brief
// close-then-reuse race is acceptable for an in-process test fixture.
func freePort(t *testing.T) int {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := lis.Addr().(*net.TCPAddr).Port
	require.NoError(t, lis.Close())
	return port
}

// waitDialable poll-dials addr until a TCP connection succeeds or timeout.
func waitDialable(addr string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// startAgent runs hostagent.Run in a background goroutine, dialing the proxy
// and registering under ownerKeyID. Returns a cancel func + done channel so
// the caller can drop the agent mid-test (disconnect scenarios).
func startAgent(t *testing.T, proxyAddr, ownerKeyID string) (context.CancelFunc, chan struct{}) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	cfg := hostagent.LoadConfigFromEnv()
	cfg.RimskyURL = proxyAddr
	cfg.APIKey = ownerKeyID
	cfg.AgentLabel = "scenario-agent"
	go func() {
		defer close(done)
		_ = hostagent.Run(ctx, cfg)
	}()
	return cancel, done
}

// fixtureOpts tweaks the host-agent fixture wiring.
type fixtureOpts struct {
	// withAgent connects a host-agent under the owner key.
	withAgent bool
	// blindProxy starts the proxy with NO control-api fallback URL and does
	// NOT wire it as a lifecycle peer, so its binding cache stays empty even
	// though the resolver (reading the instance row directly) still routes to
	// it. Used to exercise the proxy's binding_not_found guard: the resolver
	// routes the dispatch, but the proxy can't find the binding in its empty
	// cache.
	blindProxy bool
}

// newHostAgentFixture wires the host-agent stack: an authenticated
// control-api with the proxy as a late-bind executor (and, unless blind, a
// lifecycle peer + GET fallback), a minted owner key, the running proxy, the
// stub binary, and (when withAgent) a connected host-agent.
func newHostAgentFixture(t *testing.T, opts fixtureOpts) *hostAgentFixture {
	t.Helper()

	// proxyAddr is allocated up-front so the static resolver can point at it
	// before the proxy process is actually listening (gRPC dials lazily).
	proxyPort := freePort(t)
	proxyAddr := fmt.Sprintf("127.0.0.1:%d", proxyPort)

	execProtocols := []string{"executor", "lifecycle_subscriber"}
	if opts.blindProxy {
		// No lifecycle subscription → the proxy never sees OnInstanceCreated.
		execProtocols = []string{"executor"}
	}
	h := scenario.Start(t, scenario.HarnessOpts{
		ExtraExecutors: map[string]executor.Endpoint{
			proxyExecutorName: {Transport: "grpc", URL: proxyAddr},
		},
		LateBindServiceProxies: map[string]string{"executor": proxyExecutorName},
		ExecutorProtocols:      map[string][]string{proxyExecutorName: execProtocols},
	})

	// Mint the owner key (also flips the deployment to authenticated mode).
	adminKey, ownerKeyID := h.MintAdminKey("scenario-admin")

	// Start the proxy. A blind proxy gets no control-api URL so its
	// GET-fallback can't populate the cache either.
	controlURL, controlToken := h.ControlBase, adminKey
	if opts.blindProxy {
		controlURL, controlToken = "", ""
	}
	startProxyOnPort(t, proxyPort, controlURL, controlToken)

	stub := buildBinary(t, "lib/runtime/hostagent/testdata/stubchild")

	fx := &hostAgentFixture{
		h:          h,
		proxyAddr:  proxyAddr,
		stubBinary: stub,
		adminKey:   adminKey,
		ownerKeyID: ownerKeyID,
	}
	if opts.withAgent {
		fx.cancelAgent, fx.agentDone = startAgent(t, proxyAddr, ownerKeyID)
		// Give the agent a moment to register with the proxy.
		time.Sleep(300 * time.Millisecond)
	}
	return fx
}

// startProxyOnPort execs the proxy on a caller-allocated port (so the
// resolver endpoint is known before the process binds). An empty controlBase
// leaves the proxy without a GET-fallback (blind cache).
func startProxyOnPort(t *testing.T, port int, controlBase, adminKey string) {
	t.Helper()
	bin := buildBinary(t, "cmd/rimsky-host-agent-proxy")
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("RIMSKY_PROXY_GRPC_PORT=%d", port),
		"RIMSKY_CONTROL_API_URL="+controlBase,
		"RIMSKY_CONTROL_API_TOKEN="+adminKey,
		"RIMSKY_LOG_LEVEL=warn",
	)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		_ = cmd.Process.Signal(os.Interrupt)
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
		}
	})
	require.True(t, waitDialable(addr, 10*time.Second), "proxy did not come up on %s", addr)
}

// lateBindTemplateSpec builds the raw spec map for a single-node template
// whose node references the late-bound executor and declares
// late_bind_services so registration bypasses the existence/schema checks.
// The node deliberately carries no attributes block so dispatch skips the
// executor_schema_unavailable gate (the spawned binary's Capabilities are
// the authority for late-bound nodes).
func lateBindTemplateSpec(name string) map[string]any {
	return map[string]any{
		"name":                  name,
		"version":               "1",
		"frame_resolution_mode": "serial_queue",
		"late_bind_services":    []string{lateBindServiceName},
		"nodes": []map[string]any{
			{"type": "worker", "executor": lateBindServiceName},
		},
	}
}

// deployLateBindTemplate registers + deploys the late-bind template under
// the admin key and returns its hash.
func (fx *hostAgentFixture) deployLateBindTemplate(t *testing.T, name string) string {
	t.Helper()
	return fx.h.DeployTemplateSpecMap(lateBindTemplateSpec(name), fx.adminKey)
}

// createLateBindInstance creates an instance binding the late-bound service
// to the stub binary, under the owner key.
func (fx *hostAgentFixture) createLateBindInstance(t *testing.T, templateHash, instanceKey, binaryPath string) shared.UUID {
	t.Helper()
	bindings := map[string]any{
		lateBindServiceName: map[string]any{"path": binaryPath},
	}
	return fx.h.CreateInstanceWithServiceBindings(templateHash, instanceKey, fx.adminKey, map[string]any{}, bindings)
}

// waitForNodeEventKind polls rimsky_events for an event of the given kind on
// the instance's node. Returns true if seen before timeout. Used to assert
// terminal/error/<class> (failure modes) and terminal/success (happy path).
func (fx *hostAgentFixture) waitForNodeEventKind(t *testing.T, instanceID shared.UUID, kind string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		worker := fx.h.FindNode(instanceID, "worker")
		if worker != nil {
			nid := worker.ID
			var seen bool
			_ = fx.h.InTx(func(tx persistence.Tx) error {
				r, err := fx.h.Persist.Events().List(fx.h.Ctx,
					persistence.EventListFilter{NodeID: &nid},
					persistence.ListPagination{Limit: 500}, tx)
				if err != nil {
					return err
				}
				for _, e := range r.Events {
					if e.Kind == kind {
						seen = true
						return nil
					}
				}
				return nil
			})
			if seen {
				return true
			}
		}
		time.Sleep(75 * time.Millisecond)
	}
	return false
}
