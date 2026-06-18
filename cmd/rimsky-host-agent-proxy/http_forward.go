// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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
		return sp.originalCallback
	}
	base.Path = fwdURL.Path
	base.RawQuery = fwdURL.RawQuery
	return base.String()
}

func (f *httpForwarder) reply(agent *agentConnection, forwardID string, status int, body []byte, headers map[string]string) {
	agent.send(&genv1.ServerFrame{Body: &genv1.ServerFrame_HttpResponse{HttpResponse: &genv1.LocalHttpResponse{
		ForwardId: forwardID,
		Status:    int32(status),
		Body:      body,
		Headers:   headers,
	}}})
}
