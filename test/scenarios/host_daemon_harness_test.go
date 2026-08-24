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
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/enroll"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/hostdaemon"
	"github.com/rimsky-ai/rimsky-core/test/support/awaited"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

const proxyExecutorName = "codegen-proxy"

const lateBindServiceName = "codegen"

type hostDaemonFixture struct {
	h                     *scenario.Harness
	proxyAddr             string
	proxyServiceAddr      string
	proxyCAPath           string
	stubBinary            string
	adminKey              string
	targetRoutingIdentity string
	cancelDaemon          context.CancelFunc
	daemonDone            chan struct{}
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

func waitDialable(t *testing.T, addr string) {
	t.Helper()
	awaited.Until(t, "a TCP listener to accept connections on "+addr, func() bool {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err != nil {
			return false
		}
		_ = conn.Close()
		return true
	})
}

type daemonStartOptions struct {
	APIKey       string
	RoutingLabel string
}

// @decision: host-daemon-proxy-tls
func startDaemon(t *testing.T, proxyAddr, proxyCAPath string, opts daemonStartOptions) (context.CancelFunc, chan struct{}, string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	cfg, err := hostdaemon.LoadConfigFromEnv()
	if err != nil {
		t.Fatalf("LoadConfigFromEnv: %v", err)
	}
	cfg.ProxyURL = proxyAddr
	cfg.APIKey = opts.APIKey
	cfg.TLSCAPath = proxyCAPath
	cfg.DaemonLabel = "scenario-daemon"
	cfg.RoutingLabel = opts.RoutingLabel
	cfg.IdentityFile = filepath.Join(t.TempDir(), "identity.json")
	cfg.StatusFile = filepath.Join(t.TempDir(), "daemon-status.json")
	go func() {
		defer close(done)
		_ = hostdaemon.Run(ctx, cfg)
	}()
	return cancel, done, cfg.StatusFile
}

func waitDaemonConnected(t *testing.T, statusFile string) {
	t.Helper()
	awaited.Until(t, "the daemon to report connected via status file "+statusFile, func() bool {
		body, err := os.ReadFile(statusFile)
		if err != nil {
			return false
		}
		var snap hostdaemon.StatusSnapshot
		return json.Unmarshal(body, &snap) == nil && snap.Connected
	})
}

type fixtureOpts struct {
	withDaemon bool
	blindProxy bool
	producers  config.RemoteClaimProducersConfig
	anonymous  bool
}

func newHostDaemonFixture(t *testing.T, opts fixtureOpts) *hostDaemonFixture {
	t.Helper()

	proxyPort := freePort(t)
	proxyAddr := fmt.Sprintf("127.0.0.1:%d", proxyPort)
	proxyServicePort := freePort(t)
	proxyServiceAddr := fmt.Sprintf("127.0.0.1:%d", proxyServicePort)

	execProtocols := []string{"executor", "lifecycle_subscriber"}
	if opts.blindProxy {
		execProtocols = []string{"executor"}
	}
	h := scenario.Start(t, scenario.HarnessOpts{
		ExtraExecutors: map[string]executor.Endpoint{
			proxyExecutorName: {Transport: "grpc", URL: proxyServiceAddr},
		},
		LateBindServiceProxies: map[string]string{"executor": proxyExecutorName},
		ExecutorProtocols:      map[string][]string{proxyExecutorName: execProtocols},
		ClaimProducers:         opts.producers,
	})

	var adminKey, daemonAPIKey, routingLabel string
	if !opts.anonymous {
		adminKey, _ = h.MintAdminKey("scenario-admin")
		daemonAPIKey = adminKey
	} else {
		routingLabel = "scenario-badger"
	}

	controlURL, controlToken := h.ControlBase, adminKey
	if opts.blindProxy {
		controlURL, controlToken = "", ""
	}
	proxyCAPath := startProxyOnPort(t, proxyPort, proxyServicePort, controlURL, controlToken)

	stub := buildBinary(t, "lib/runtime/hostdaemon/testdata/stubchild")

	fx := &hostDaemonFixture{
		h:                h,
		proxyAddr:        proxyAddr,
		proxyServiceAddr: proxyServiceAddr,
		proxyCAPath:      proxyCAPath,
		stubBinary:       stub,
		adminKey:         adminKey,
	}
	if opts.anonymous {
		fx.targetRoutingIdentity = routingLabel
	}
	if opts.withDaemon {
		var statusFile string
		fx.cancelDaemon, fx.daemonDone, statusFile = startDaemon(t, proxyAddr, proxyCAPath,
			daemonStartOptions{APIKey: daemonAPIKey, RoutingLabel: routingLabel})
		t.Cleanup(func() {
			fx.cancelDaemon()
			<-fx.daemonDone
		})
		if !opts.blindProxy {
			waitDaemonConnected(t, statusFile)
		}
	}
	return fx
}

// @decision: host-daemon-proxy-tls
// @concept: host-daemon-proxy
func startProxyOnPort(t *testing.T, daemonPort, servicePort int, controlBase, adminKey string) string {
	t.Helper()
	bin := buildBinary(t, "cmd/rimsky-host-daemon-proxy")
	addr := fmt.Sprintf("127.0.0.1:%d", daemonPort)
	serviceAddr := fmt.Sprintf("127.0.0.1:%d", servicePort)
	caPath := filepath.Join(t.TempDir(), "proxy-ca.pem")
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("RIMSKY_PROXY_GRPC_PORT=%d", daemonPort),
		fmt.Sprintf("RIMSKY_PROXY_SERVICE_GRPC_PORT=%d", servicePort),
		"RIMSKY_CONTROL_API_URL="+controlBase,
		"RIMSKY_CONTROL_API_TOKEN="+adminKey,
		"RIMSKY_PROXY_LOCAL_CA_FILE="+caPath,
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
		//nolint:testwallclock-pacing a teardown grace before SIGKILL; the arm kills the child and reaches no verdict
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
		}
	})
	waitDialable(t, addr)
	waitDialable(t, serviceAddr)
	awaited.Until(t, "the proxy to publish its daemon-facing CA root at "+caPath, func() bool {
		_, err := os.Stat(caPath)
		return err == nil
	})
	return caPath
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

func (fx *hostDaemonFixture) deployLateBindTemplate(t *testing.T, name string) string {
	t.Helper()
	return fx.h.DeployTemplateSpecMap(lateBindTemplateSpec(name), fx.adminKey)
}

func (fx *hostDaemonFixture) createLateBindInstance(t *testing.T, templateHash, instanceKey, binaryPath string) shared.UUID {
	t.Helper()
	bindings := map[string]any{
		lateBindServiceName: map[string]any{"path": binaryPath},
	}
	return fx.h.CreateInstanceWithServiceBindingsAndTarget(templateHash, instanceKey, fx.adminKey, map[string]any{}, bindings, fx.targetRoutingIdentity)
}

func (fx *hostDaemonFixture) nodeEventPayload(t *testing.T, instanceID shared.UUID, kind string) map[string]any {
	t.Helper()
	worker := fx.h.FindNode(instanceID, "worker")
	if worker == nil {
		t.Fatalf("instance %s has no worker node", instanceID)
	}
	nid := worker.ID
	var payload map[string]any
	if err := fx.h.InTx(func(tx persistence.Tx) error {
		r, err := fx.h.Persist.Events().List(fx.h.Ctx,
			persistence.EventListFilter{NodeID: &nid},
			persistence.ListPagination{Limit: 500}, tx)
		if err != nil {
			return err
		}
		for _, e := range r.Events {
			if e.KindRaw == kind {
				payload = e.Payload.Map()
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("list events for node %s: %v", nid, err)
	}
	if payload == nil {
		t.Fatalf("node %s logged no %s event", nid, kind)
	}
	return payload
}

func (fx *hostDaemonFixture) waitForNodeEventKind(t *testing.T, instanceID shared.UUID, kinds ...string) string {
	t.Helper()
	var found string
	awaited.Until(t, fmt.Sprintf("the worker node of instance %s to log one of the event kinds %v", instanceID, kinds), func() bool {
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
				found = seen
				return true
			}
		}
		return false
	})
	return found
}

// @decision: host-daemon-proxy-tls
func dialDaemonFacing(t *testing.T, proxyAddr, caPath string) *grpc.ClientConn {
	t.Helper()
	pool, err := enroll.CAPoolFromFile(caPath)
	require.NoError(t, err, "read the CA root the proxy published")
	conn, err := grpc.NewClient(proxyAddr, grpc.WithTransportCredentials(credentials.NewTLS(enroll.PinnedTLSConfig(pool))))
	require.NoError(t, err, "dial the proxy's daemon-facing listener")
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}
