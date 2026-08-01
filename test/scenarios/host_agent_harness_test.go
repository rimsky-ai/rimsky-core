// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package scenarios

import (
	"context"
	"encoding/json"
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

type hostAgentFixture struct {
	h                     *scenario.Harness
	proxyAddr             string
	stubBinary            string
	adminKey              string
	targetRoutingIdentity string
	cancelAgent           context.CancelFunc
	agentDone             chan struct{}
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

func waitDialable(addr string) {
	for {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

type agentStartOptions struct {
	APIKey       string
	RoutingLabel string
}

func startAgent(t *testing.T, proxyAddr string, opts agentStartOptions) (context.CancelFunc, chan struct{}, string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	cfg, err := hostagent.LoadConfigFromEnv()
	if err != nil {
		t.Fatalf("LoadConfigFromEnv: %v", err)
	}
	cfg.ProxyURL = proxyAddr
	cfg.APIKey = opts.APIKey
	cfg.AgentLabel = "scenario-agent"
	cfg.RoutingLabel = opts.RoutingLabel
	cfg.IdentityFile = filepath.Join(t.TempDir(), "identity.json")
	cfg.StatusFile = filepath.Join(t.TempDir(), "agent-status.json")
	go func() {
		defer close(done)
		_ = hostagent.Run(ctx, cfg)
	}()
	return cancel, done, cfg.StatusFile
}

func waitAgentConnected(t *testing.T, statusFile string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		body, err := os.ReadFile(statusFile)
		if err == nil {
			var snap hostagent.StatusSnapshot
			if jerr := json.Unmarshal(body, &snap); jerr == nil && snap.Connected {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("agent did not report connected via status file %s within deadline", statusFile)
}

type fixtureOpts struct {
	withAgent  bool
	blindProxy bool
	producers  config.RemoteClaimProducersConfig
	anonymous  bool
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
		ClaimProducers:         opts.producers,
	})

	var adminKey, agentAPIKey, routingLabel string
	if !opts.anonymous {
		adminKey, _ = h.MintAdminKey("scenario-admin")
		agentAPIKey = adminKey
	} else {
		routingLabel = "scenario-badger"
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
	}
	if opts.anonymous {
		fx.targetRoutingIdentity = routingLabel
	}
	if opts.withAgent {
		var statusFile string
		fx.cancelAgent, fx.agentDone, statusFile = startAgent(t, proxyAddr, agentStartOptions{APIKey: agentAPIKey, RoutingLabel: routingLabel})
		t.Cleanup(func() {
			fx.cancelAgent()
			<-fx.agentDone
		})
		if !opts.blindProxy {
			waitAgentConnected(t, statusFile)
		}
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
	waitDialable(addr)
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
	return fx.h.CreateInstanceWithServiceBindingsAndTarget(templateHash, instanceKey, fx.adminKey, map[string]any{}, bindings, fx.targetRoutingIdentity)
}

func (fx *hostAgentFixture) waitForNodeEventKind(t *testing.T, instanceID shared.UUID, kinds ...string) string {
	t.Helper()
	for {
		worker := fx.h.FindNode(instanceID, "worker")
		if worker != nil {
			nid := worker.ID
			var seen string
			_ = fx.h.InTx(func(tx persistence.Tx) error {
				r, err := fx.h.Persist.Events().List(fx.h.Ctx,
					persistence.EventListFilter{NodeID: &nid},
					persistence.ListPagination{Limit: 500}, tx)
				if err != nil {
					return err
				}
				for _, e := range r.Events {
					for _, kind := range kinds {
						if e.KindRaw == kind {
							seen = kind
							return nil
						}
					}
				}
				return nil
			})
			if seen != "" {
				return seen
			}
		}
		time.Sleep(75 * time.Millisecond)
	}
}
