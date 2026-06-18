// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: host-agent
package hostagent

import (
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

const localForwardTimeout = 30 * time.Second

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

func (a *agent) registerForward(forwardID string) chan *genv1.LocalHttpResponse {
	ch := make(chan *genv1.LocalHttpResponse, 1)
	a.forwardMu.Lock()
	a.pendingForwards[forwardID] = ch
	a.forwardMu.Unlock()
	return ch
}

func (a *agent) clearForward(forwardID string) {
	a.forwardMu.Lock()
	if ch, ok := a.pendingForwards[forwardID]; ok {
		delete(a.pendingForwards, forwardID)
		close(ch)
	}
	a.forwardMu.Unlock()
}

func (a *agent) deliverHTTPResponse(resp *genv1.LocalHttpResponse) {
	a.forwardMu.Lock()
	ch, ok := a.pendingForwards[resp.GetForwardId()]
	a.forwardMu.Unlock()
	if !ok {
		return
	}
	select {
	case ch <- resp:
	default:
	}
}
