// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package hostagent

import (
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

func freeTestPort(t *testing.T) int {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := lis.Addr().(*net.TCPAddr).Port
	_ = lis.Close()
	return port
}

func TestLocalHTTPForwardRoundTrip(t *testing.T) {
	fp := startFakeProxy(t)
	port := freeTestPort(t)

	go func() {
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
				Headers:   map[string]string{"X-Echo-Method": fwd.GetMethod()},
			}}})
		}
	}()

	runAgentInBackground(t, Config{
		RimskyURL:  fp.addr,
		APIKey:     "k",
		ListenAddr: net.JoinHostPort("127.0.0.1", strconv.Itoa(port)),
	})
	fp.waitConnected(t)

	url := "http://127.0.0.1:" + strconv.Itoa(port) + "/v1/callback/ack-1"
	var resp *http.Response
	var err error
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err = http.Post(url, "application/json", strings.NewReader("hello"))
		if err == nil {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("post to local listener: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "echo:hello" {
		t.Fatalf("body = %q, want %q", body, "echo:hello")
	}
	if got := resp.Header.Get("X-Echo-Method"); got != http.MethodPost {
		t.Fatalf("X-Echo-Method = %q, want POST", got)
	}
}

func TestDeliverHTTPResponseNeverSendsOnClearedForward(t *testing.T) {
	a := newAgent(Config{}, "", nil)
	for i := 0; i < 10000; i++ {
		forwardID := strconv.Itoa(i)
		ch := a.registerForward(forwardID)

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			a.deliverHTTPResponse(&genv1.LocalHttpResponse{ForwardId: forwardID, Status: http.StatusOK})
		}()
		go func() {
			defer wg.Done()
			a.clearForward(forwardID)
		}()
		wg.Wait()

		a.forwardMu.Lock()
		_, stillPending := a.pendingForwards[forwardID]
		a.forwardMu.Unlock()
		if stillPending {
			t.Fatalf("iteration %d: forward %q still pending after deliver+clear", i, forwardID)
		}

		select {
		case resp, ok := <-ch:
			if ok && resp == nil {
				t.Fatalf("iteration %d: delivered response is nil", i)
			}
		default:
			t.Fatalf("iteration %d: channel neither closed nor delivered", i)
		}
	}
}

func TestLocalHTTPForwardTimeout(t *testing.T) {
	fp := startFakeProxy(t)
	port := freeTestPort(t)

	runAgentInBackground(t, Config{
		RimskyURL:  fp.addr,
		APIKey:     "k",
		ListenAddr: net.JoinHostPort("127.0.0.1", strconv.Itoa(port)),
	})
	fp.waitConnected(t)

	url := "http://127.0.0.1:" + strconv.Itoa(port) + "/whatever"
	go func() { _, _ = http.Post(url, "text/plain", strings.NewReader("x")) }()

	frame := fp.nextClientFrame(t)
	if frame.GetHttpForward() == nil {
		t.Fatalf("expected an http_forward frame, got %T", frame.GetBody())
	}
	if frame.GetHttpForward().GetMethod() != http.MethodPost {
		t.Fatalf("forward method = %q, want POST", frame.GetHttpForward().GetMethod())
	}
}
