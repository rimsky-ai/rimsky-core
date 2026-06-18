// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/hostagent"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

const proxyExecutorName = "codegen-proxy"

const lateBindServiceName = "codegen"

const anonRoutingIdentity = "anonymous"

type hostAgentFixture struct {
	h           *scenario.Harness
	proxyAddr   string
	stubBinary  string
	adminKey    string
	ownerKeyID  string
	cancelAgent context.CancelFunc
	agentDone   chan struct{}
}

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

func freePort(t *testing.T) int {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := lis.Addr().(*net.TCPAddr).Port
	require.NoError(t, lis.Close())
	return port
}

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

type fixtureOpts struct {
	withAgent bool
	blindProxy bool
	stores config.RemoteStoresConfig
	anonymous bool
}

func newHostAgentFixture(t *testing.T, opts fixtureOpts) *hostAgentFixture {
	t.Helper()

	proxyPort := freePort(t)
	proxyAddr := fmt.Sprintf("127.0.0.1:%d", proxyPort)

	execProtocols := []string{"executor", "lifecycle_subscriber"}
	if opts.blindProxy {
		execProtocols = []string{"executor"}
	}
	h := scenario.Start(t, scenario.HarnessOpts{
		ExtraExecutors: map[string]executor.Endpoint{
			proxyExecutorName: {Transport: "grpc", URL: proxyAddr},
		},
		LateBindServiceProxies: map[string]string{"executor": proxyExecutorName},
		ExecutorProtocols:      map[string][]string{proxyExecutorName: execProtocols},
		Stores:                 opts.stores,
	})

	var adminKey, ownerKeyID, agentRoutingKey string
	if opts.anonymous {
		agentRoutingKey = anonRoutingIdentity
	} else {
		adminKey, ownerKeyID = h.MintAdminKey("scenario-admin")
		agentRoutingKey = ownerKeyID
	}

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
		fx.cancelAgent, fx.agentDone = startAgent(t, proxyAddr, agentRoutingKey)
		time.Sleep(300 * time.Millisecond)
	}
	return fx
}

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

func lateBindTemplateSpec(name string) map[string]any {
	return map[string]any{
		"name":               name,
		"version":            "1",
		"late_bind_services": []string{lateBindServiceName},
		"nodes": []map[string]any{
			{"type": "worker", "executor": lateBindServiceName},
		},
	}
}

func (fx *hostAgentFixture) deployLateBindTemplate(t *testing.T, name string) string {
	t.Helper()
	return fx.h.DeployTemplateSpecMap(lateBindTemplateSpec(name), fx.adminKey)
}

func (fx *hostAgentFixture) createLateBindInstance(t *testing.T, templateHash, instanceKey, binaryPath string) shared.UUID {
	t.Helper()
	bindings := map[string]any{
		lateBindServiceName: map[string]any{"path": binaryPath},
	}
	return fx.h.CreateInstanceWithServiceBindings(templateHash, instanceKey, fx.adminKey, map[string]any{}, bindings)
}

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
					if e.KindRaw == kind {
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
