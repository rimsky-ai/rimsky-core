// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: host-daemon-proxy

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
	"github.com/rimsky-ai/rimsky-core/lib/runtime/hostdaemon"
)

const (
	maxConcurrentForwards   = 64
	maxForwardResponseBytes = 8 << 20
)

type httpForwarder struct {
	state  *proxyState
	client *http.Client
	sem    chan struct{}
}

func newHTTPForwarder(state *proxyState) *httpForwarder {
	return &httpForwarder{
		state:  state,
		client: &http.Client{Timeout: 30 * time.Second},
		sem:    make(chan struct{}, maxConcurrentForwards),
	}
}

func (f *httpForwarder) handle(daemon *daemonConnection, fwd *genv1.LocalHttpForward) {
	f.sem <- struct{}{}
	defer func() { <-f.sem }()

	target := f.targetURL(fwd)
	if target == "" {
		f.reply(daemon, fwd.GetForwardId(), http.StatusBadGateway, []byte("no upstream callback for spawn"), nil)
		return
	}

	method := fwd.GetMethod()
	if method == "" {
		method = http.MethodPost
	}
	req, err := http.NewRequestWithContext(context.Background(), method, target, bytes.NewReader(fwd.GetBody()))
	if err != nil {
		slog.Warn("PROXY.HTTPFORWARD.BUILDFAILED", "error", err, "forward_id", fwd.GetForwardId())
		f.reply(daemon, fwd.GetForwardId(), http.StatusBadGateway, []byte(err.Error()), nil)
		return
	}
	hostdaemon.ApplyJoinedHeaders(req.Header, fwd.GetHeaders())

	resp, err := f.client.Do(req)
	if err != nil {
		slog.Warn("PROXY.HTTPFORWARD.POSTFAILED", "error", err, "forward_id", fwd.GetForwardId())
		f.reply(daemon, fwd.GetForwardId(), http.StatusBadGateway, []byte(err.Error()), nil)
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxForwardResponseBytes))
	headers := hostdaemon.JoinHeaderValues(resp.Header)
	f.reply(daemon, fwd.GetForwardId(), resp.StatusCode, body, headers)
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

func (f *httpForwarder) reply(daemon *daemonConnection, forwardID string, status int, body []byte, headers map[string]string) {
	daemon.send(&genv1.ServerFrame{Body: &genv1.ServerFrame_HttpResponse{HttpResponse: &genv1.LocalHttpResponse{
		ForwardId: forwardID,
		Status:    int32(status),
		Body:      body,
		Headers:   headers,
	}}})
}
