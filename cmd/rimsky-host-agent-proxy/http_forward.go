// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// http_forward.go — un-rewrites and relays the spawned process's local
// HTTP callbacks back to the supervisor. When a spawned child POSTs to
// the rewritten callback URL on the agent's local listener, the agent
// wraps it as a LocalHttpForward and tunnels it here. The proxy un-
// rewrites the URL to the original supervisor callback (recorded at spawn
// time), POSTs the body upstream, and returns the response to the agent
// as a LocalHttpResponse.
//
// @concept: host-agent-proxy

package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// httpForwarder relays agent LocalHttpForward frames to the original
// supervisor callback URL recorded on the originating spawn.
type httpForwarder struct {
	state  *proxyState
	client *http.Client
}

func newHTTPForwarder(state *proxyState) *httpForwarder {
	return &httpForwarder{
		state:  state,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

// handle relays one LocalHttpForward upstream and sends the
// LocalHttpResponse back over the originating agent connection.
func (f *httpForwarder) handle(agent *agentConnection, fwd *genv1.LocalHttpForward) {
	target := f.targetURL(fwd)
	if target == "" {
		f.reply(agent, fwd.GetForwardId(), http.StatusBadGateway, []byte("no upstream callback for spawn"), nil)
		return
	}

	method := fwd.GetMethod()
	if method == "" {
		method = http.MethodPost
	}
	req, err := http.NewRequestWithContext(context.Background(), method, target, bytes.NewReader(fwd.GetBody()))
	if err != nil {
		slog.Warn("http_forward: build request failed", "error", err, "forward_id", fwd.GetForwardId())
		f.reply(agent, fwd.GetForwardId(), http.StatusBadGateway, []byte(err.Error()), nil)
		return
	}
	for k, v := range fwd.GetHeaders() {
		req.Header.Set(k, v)
	}

	resp, err := f.client.Do(req)
	if err != nil {
		slog.Warn("http_forward: upstream POST failed", "error", err, "forward_id", fwd.GetForwardId())
		f.reply(agent, fwd.GetForwardId(), http.StatusBadGateway, []byte(err.Error()), nil)
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	headers := map[string]string{}
	for k := range resp.Header {
		headers[k] = resp.Header.Get(k)
	}
	f.reply(agent, fwd.GetForwardId(), resp.StatusCode, body, headers)
}

// targetURL un-rewrites the spawned child's callback back to the
// supervisor. The supervisor builds a distinct callback path per dispatch
// (e.g. /v1/callback/{async_ack_id}), so a spawn that serves more than one
// dispatch in a run-scope must un-rewrite using the *current* forward's
// path — not the path recorded at the spawn's first dispatch. The spawn's
// originalCallback supplies the supervisor scheme+host:port (stable across
// a run-scope); the forward's url supplies the per-callback path+query.
// Returns "" when the spawn is unknown or has no recorded callback base
// (e.g. a claim-producer spawn, which has no callback URL).
func (f *httpForwarder) targetURL(fwd *genv1.LocalHttpForward) string {
	sp, ok := f.state.lookupSpawn(fwd.GetSpawnId())
	if !ok || sp.originalCallback == "" {
		return ""
	}
	base, err := url.Parse(sp.originalCallback)
	if err != nil || base.Host == "" {
		return ""
	}
	fwdURL, err := url.Parse(fwd.GetUrl())
	if err != nil || fwdURL.Path == "" {
		// @deliberate: no usable per-callback path on the forward; fall
		// back to the recorded callback verbatim (single-dispatch happy
		// path).
		return sp.originalCallback
	}
	base.Path = fwdURL.Path
	base.RawQuery = fwdURL.RawQuery
	return base.String()
}

// reply sends a LocalHttpResponse back to the agent.
func (f *httpForwarder) reply(agent *agentConnection, forwardID string, status int, body []byte, headers map[string]string) {
	agent.send(&genv1.ServerFrame{Body: &genv1.ServerFrame_HttpResponse{HttpResponse: &genv1.LocalHttpResponse{
		ForwardId: forwardID,
		Status:    int32(status),
		Body:      body,
		Headers:   headers,
	}}})
}
