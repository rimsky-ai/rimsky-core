// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package hostagent

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/protobuf/proto"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/pki"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/enroll"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

type agentStack struct {
	trust        *localTrust
	enrollBase   string
	callbackBase string
	fp           *fakeProxy
	a            *agent
}

func startAgentStack(t *testing.T) *agentStack {
	t.Helper()
	trust, err := newLocalTrust(time.Now())
	if err != nil {
		t.Fatalf("newLocalTrust: %v", err)
	}
	enrollLis, enrollBase, err := bindPlaintextListener("")
	if err != nil {
		t.Fatalf("bind enroll: %v", err)
	}
	callbackLis, callbackBase, err := bindCallbackListener()
	if err != nil {
		t.Fatalf("bind callback: %v", err)
	}
	fp := startFakeProxy(t)

	var mu sync.Mutex
	var cur *agent
	get := func() *agent { mu.Lock(); defer mu.Unlock(); return cur }

	enrollSrv := &http.Server{Handler: localEnrollHandler(trust, get)}
	callbackSrv := &http.Server{Handler: localForwardHandler(get), TLSConfig: trust.callbackServerTLSConfig()}
	go func() { _ = enrollSrv.Serve(enrollLis) }()
	go func() { _ = callbackSrv.ServeTLS(callbackLis, "", "") }()

	ctx, cancel := context.WithCancel(context.Background())
	cfg := Config{ProxyURL: fp.addr, APIKey: "k", Insecure: true}.withDefaults()
	a, err := connectOnce(ctx, cfg, trust, enrollBase, callbackBase)
	if err != nil {
		cancel()
		t.Fatalf("connectOnce: %v", err)
	}
	mu.Lock()
	cur = a
	mu.Unlock()
	fp.waitConnected(t)
	go a.serve(ctx)

	t.Cleanup(func() {
		cancel()
		_ = enrollSrv.Close()
		_ = callbackSrv.Close()
		_ = enrollLis.Close()
		_ = callbackLis.Close()
	})
	return &agentStack{trust: trust, enrollBase: enrollBase, callbackBase: callbackBase, fp: fp, a: a}
}

func replyToForwards(fp *fakeProxy) {
	<-fp.connected
	for {
		frame := <-fp.clientFrame
		fwd := frame.GetHttpForward()
		if fwd == nil {
			continue
		}
		fp.mu.Lock()
		stream := fp.stream
		fp.mu.Unlock()
		_ = stream.Send(&genv1.ServerFrame{Body: &genv1.ServerFrame_HttpResponse{HttpResponse: &genv1.LocalHttpResponse{
			ForwardId: fwd.GetForwardId(),
			Status:    http.StatusCreated,
			Body:      append([]byte("echo:"), fwd.GetBody()...),
		}}})
	}
}

func readInjectedEnv(t *testing.T, execLog string) string {
	t.Helper()
	raw, err := os.ReadFile(execLog)
	if err != nil {
		t.Fatalf("read exec log: %v", err)
	}
	var rec struct {
		Env string `json:"env"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(raw))), &rec); err != nil {
		t.Fatalf("decode exec log: %v", err)
	}
	return rec.Env
}

func childHTTPClient(t *testing.T, trust *localTrust, principal string) *http.Client {
	t.Helper()
	issued, err := trust.issueChildLeaf(principal, time.Now())
	if err != nil {
		t.Fatalf("issueChildLeaf: %v", err)
	}
	cert, err := tls.X509KeyPair([]byte(issued.CertPEM), []byte(issued.KeyPEM))
	if err != nil {
		t.Fatalf("keypair: %v", err)
	}
	return &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{cert},
		RootCAs:      trust.caPool,
	}}}
}

func TestCallbackListenerAcceptsMutualChildRejectsOthers(t *testing.T) {
	st := startAgentStack(t)
	go replyToForwards(st.fp)
	url := st.callbackBase + "/v1/callback/ack-1"

	good := childHTTPClient(t, st.trust, "spawn-good")
	resp, err := good.Post(url, "application/json", strings.NewReader("hi"))
	if err != nil {
		t.Fatalf("mutually-authenticated child must be accepted: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}

	noCert := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		MinVersion: tls.VersionTLS12,
		RootCAs:    st.trust.caPool,
	}}}
	if _, err := noCert.Post(url, "application/json", strings.NewReader("hi")); err == nil {
		t.Fatal("a caller presenting no client cert must be rejected at the TLS handshake")
	}

	foreign, err := newLocalTrust(time.Now())
	if err != nil {
		t.Fatalf("foreign trust: %v", err)
	}
	foreignClient := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{foreign.agentCert},
		RootCAs:      st.trust.caPool,
	}}}
	if _, err := foreignClient.Post(url, "application/json", strings.NewReader("hi")); err == nil {
		t.Fatal("a caller presenting a cert from a different CA must be rejected at the TLS handshake")
	}
}

func TestEnrollIssuesLeafForLiveTokenAnd401sUnknown(t *testing.T) {
	st := startAgentStack(t)
	st.a.registerBootstrapToken("spawn-42", "live-token", time.Now())

	ctx := context.Background()
	resp, err := enroll.Enroll(ctx, &http.Client{}, st.enrollBase, "live-token", "child-label")
	if err != nil {
		t.Fatalf("enroll with a live bootstrap token must succeed: %v", err)
	}

	block, _ := pem.Decode([]byte(resp.CertPEM))
	if block == nil {
		t.Fatal("issued cert is not PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse issued cert: %v", err)
	}
	chains, err := cert.Verify(x509.VerifyOptions{Roots: st.trust.caPool})
	if err != nil {
		t.Fatalf("issued cert must chain to the local CA: %v", err)
	}
	principal, err := pki.PrincipalFromVerifiedChains(&tls.ConnectionState{VerifiedChains: chains})
	if err != nil || principal != "spawn-42" {
		t.Fatalf("issued cert principal = %q (err %v), want spawn-42", principal, err)
	}

	_, err = enroll.Enroll(ctx, &http.Client{}, st.enrollBase, "not-a-real-token", "child-label")
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("enroll with an unknown token must 401, got err=%v", err)
	}
}

func startTLSAcceptor(t *testing.T, cfg *tls.Config) int {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = lis.Close() })
	go func() {
		for {
			conn, acceptErr := lis.Accept()
			if acceptErr != nil {
				return
			}
			tc := tls.Server(conn, cfg)
			_ = tc.Handshake()
			_ = tc.Close()
		}
	}()
	return lis.Addr().(*net.TCPAddr).Port
}

func startPlaintextAcceptor(t *testing.T) int {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = lis.Close() })
	go func() {
		for {
			conn, acceptErr := lis.Accept()
			if acceptErr != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	return lis.Addr().(*net.TCPAddr).Port
}

func TestLocalReadinessRequiresMTLSHandshake(t *testing.T) {
	trust, err := newLocalTrust(time.Now())
	if err != nil {
		t.Fatalf("newLocalTrust: %v", err)
	}
	mtlsPort := startTLSAcceptor(t, trust.callbackServerTLSConfig())
	plainPort := startPlaintextAcceptor(t)

	if !probeReady(net.JoinHostPort("127.0.0.1", strconv.Itoa(mtlsPort)), trust.dialChildTLSConfig()) {
		t.Fatal("a peer completing the mutual-TLS handshake must be deemed ready")
	}
	if probeReady(net.JoinHostPort("127.0.0.1", strconv.Itoa(plainPort)), trust.dialChildTLSConfig()) {
		t.Fatal("a plaintext peer must never be deemed ready under mTLS readiness")
	}
}

func startEnrollServer(t *testing.T, trust *localTrust, principal, token string) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc(enroll.Path, func(w http.ResponseWriter, r *http.Request) {
		if bearerToken(r) != token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		resp, err := trust.issueChildLeaf(principal, time.Now())
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestSpawnRetriesPastPlaintextSquatterThenDispatchesOverMTLS(t *testing.T) {
	trust, err := newLocalTrust(time.Now())
	if err != nil {
		t.Fatalf("newLocalTrust: %v", err)
	}
	const token = "boot-tok"
	enrollBase := startEnrollServer(t, trust, "spawn-x", token)
	bin := buildStubChild(t)

	squatPort := startPlaintextAcceptor(t)
	calls := 0
	portSource := func() (int, error) {
		calls++
		if calls == 1 {
			return squatPort, nil
		}
		return FreeLocalPort()
	}

	env := os.Environ()
	env = setEnvVar(env, enroll.EnvPeerAuth, enroll.PeerAuthMTLS)
	env = setEnvVar(env, enroll.EnvAPIKey, token)
	env = setEnvVar(env, enroll.EnvControlAPIURL, enrollBase)

	ctx := context.Background()

	spawned, err := SpawnService(ctx, SpawnServiceParams{
		BinaryPath:   bin,
		Env:          env,
		DialTLS:      trust.dialChildTLSConfig(),
		ReadyTimeout: 5 * time.Second,
		portSource:   portSource,
	})
	if err != nil {
		t.Fatalf("SpawnService must retry past the plaintext squatter and land the mTLS child: %v", err)
	}
	if spawned.Port == squatPort {
		t.Fatalf("spawned on the plaintext squatter port %d", squatPort)
	}
	if calls < 2 {
		t.Fatalf("portSource called %d time(s), want >= 2 (squatter must force a re-pick)", calls)
	}

	conn, err := grpc.NewClient(fmt.Sprintf("127.0.0.1:%d", spawned.Port),
		grpc.WithTransportCredentials(credentials.NewTLS(trust.dialChildTLSConfig())))
	if err != nil {
		t.Fatalf("dial child over mTLS: %v", err)
	}
	defer func() { _ = conn.Close() }()
	out, err := genv1.NewExecutorClient(conn).Execute(ctx, &genv1.ExecuteRequest{NodeId: "node-1"})
	if err != nil {
		t.Fatalf("dispatch over mTLS to the real child must succeed: %v", err)
	}
	if out.GetSuccess() == nil {
		t.Fatalf("expected Outcome{Success}, got %T", out.GetOutcome())
	}

	_ = spawned.Cmd.Process.Signal(syscall.SIGTERM)
	<-spawned.Exited
}

func TestSpawnProvisionsMTLSEnvToChild(t *testing.T) {
	bin := buildStubChild(t)
	execLog := t.TempDir() + "/exec.log"
	t.Setenv("STUBCHILD_EXEC_LOG", execLog)
	t.Setenv("STUBCHILD_EXEC_ENV_KEY", enroll.EnvPeerAuth)

	fp := startFakeProxy(t)
	connectAgentToFakeProxy(t, fp, Config{})

	spawnID := uuid.NewString()
	ack := spawnVia(t, fp, &genv1.Spawn{
		SpawnId:             spawnID,
		Binding:             &genv1.Binding{Path: bin},
		ExpectedProtocols:   []string{protocolExecutor},
		ReadyTimeoutSeconds: 15,
	})
	if ack.GetStatus() != genv1.SpawnAck_SPAWN_STATUS_READY {
		t.Fatalf("spawn failed: %v", ack.GetError())
	}

	reqBytes, _ := proto.Marshal(&genv1.ExecuteRequest{NodeId: "node-1", InstanceId: "inst-1"})
	streamID := uuid.NewString()
	fp.sendToAgent(t, &genv1.ServerFrame{Body: &genv1.ServerFrame_DispatchFrame{DispatchFrame: &genv1.DispatchFrame{
		SpawnId:  spawnID,
		Protocol: protocolExecutor,
		Payload:  reqBytes,
		StreamId: streamID,
		Kind:     genv1.DispatchFrame_DISPATCH_FRAME_KIND_DATA,
	}}})
	_ = nextDispatch(t, fp, streamID)

	if got := readInjectedEnv(t, execLog); got != enroll.PeerAuthMTLS {
		t.Fatalf("child %s = %q, want %q", enroll.EnvPeerAuth, got, enroll.PeerAuthMTLS)
	}

	reapVia(t, fp, spawnID, 5)
}

func TestLocalHTTPForwardRoundTripOverMTLS(t *testing.T) {
	st := startAgentStack(t)
	go replyToForwards(st.fp)

	client := childHTTPClient(t, st.trust, "spawn-cb")
	resp, err := client.Post(st.callbackBase+"/v1/callback/ack-1", "application/json", strings.NewReader("hello"))
	if err != nil {
		t.Fatalf("post over mTLS: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	body := make([]byte, len("echo:hello"))
	_, _ = resp.Body.Read(body)
	if string(body) != "echo:hello" {
		t.Fatalf("body = %q, want echo:hello", body)
	}
}
