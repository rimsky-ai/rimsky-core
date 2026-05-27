// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// local_http.go — the agent's local HTTP listener. Spawned children post
// their rimsky-side HTTP traffic (async callbacks, attribute writebacks,
// publisher message emits) to this listener because the proxy rewrote those
// URLs onto the agent's local_callback_base_url. The catch-all handler wraps
// each request as a LocalHttpForward, tunnels it through the live stream, and
// awaits the matching LocalHttpResponse (keyed by a fresh forward_id) before
// writing the proxied response back to the child.
//
// @concept: host-agent
package hostagent

import (
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"

	genv1 "github.com/rimsky-ai/rimsky-core/protocols/proto/v1/gen"
)

// localForwardTimeout bounds how long the handler waits for a
// LocalHttpResponse before returning 504 to the spawned child.
const localForwardTimeout = 30 * time.Second

// localForwardHandler returns the catch-all HTTP handler. currentAgent
// resolves the live connection's agent (nil when no stream is up — the agent
// reconnects with backoff, so a callback that races a reconnect gets 503).
func localForwardHandler(currentAgent func() *agent) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a := currentAgent()
		if a == nil {
			http.Error(w, "host-agent: no live proxy connection", http.StatusServiceUnavailable)
			return
		}
		a.forwardHTTP(w, r)
	})
}

// forwardHTTP wraps one inbound request as a LocalHttpForward, tunnels it,
// and writes the awaited LocalHttpResponse back to the spawned child.
func (a *agent) forwardHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "host-agent: read body: "+err.Error(), http.StatusBadGateway)
		return
	}
	_ = r.Body.Close()

	headers := map[string]string{}
	for k := range r.Header {
		headers[k] = r.Header.Get(k)
	}

	forwardID := uuid.NewString()
	respCh := a.registerForward(forwardID)
	defer a.clearForward(forwardID)

	// The proxy un-rewrites the URL against the originating spawn's recorded
	// callback, so the full URL the child saw is forwarded verbatim.
	url := "http://" + r.Host + r.URL.RequestURI()
	if !a.send(&genv1.ClientFrame{Body: &genv1.ClientFrame_HttpForward{HttpForward: &genv1.LocalHttpForward{
		ForwardId: forwardID,
		Method:    r.Method,
		Url:       url,
		Body:      body,
		Headers:   headers,
	}}}) {
		http.Error(w, "host-agent: proxy connection closed", http.StatusServiceUnavailable)
		return
	}

	select {
	case resp, ok := <-respCh:
		if !ok || resp == nil {
			http.Error(w, "host-agent: forward cancelled", http.StatusServiceUnavailable)
			return
		}
		for k, v := range resp.GetHeaders() {
			w.Header().Set(k, v)
		}
		status := int(resp.GetStatus())
		if status == 0 {
			status = http.StatusOK
		}
		w.WriteHeader(status)
		_, _ = w.Write(resp.GetBody())
	case <-time.After(localForwardTimeout):
		http.Error(w, "host-agent: forward timed out", http.StatusGatewayTimeout)
	}
}

// registerForward creates the pending channel for a forward_id.
func (a *agent) registerForward(forwardID string) chan *genv1.LocalHttpResponse {
	ch := make(chan *genv1.LocalHttpResponse, 1)
	a.forwardMu.Lock()
	a.pendingForwards[forwardID] = ch
	a.forwardMu.Unlock()
	return ch
}

// clearForward removes and closes the pending channel for a forward_id.
func (a *agent) clearForward(forwardID string) {
	a.forwardMu.Lock()
	if ch, ok := a.pendingForwards[forwardID]; ok {
		delete(a.pendingForwards, forwardID)
		close(ch)
	}
	a.forwardMu.Unlock()
}

// deliverHTTPResponse routes an inbound LocalHttpResponse to its waiter.
func (a *agent) deliverHTTPResponse(resp *genv1.LocalHttpResponse) {
	a.forwardMu.Lock()
	ch, ok := a.pendingForwards[resp.GetForwardId()]
	a.forwardMu.Unlock()
	if !ok {
		return // late or unknown forward_id; drop.
	}
	select {
	case ch <- resp:
	default:
	}
}
