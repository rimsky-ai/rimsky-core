// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: host-daemon
package hostdaemon

import (
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/pki"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

const localForwardTimeout = 30 * time.Second

const bootstrapTokenTTL = pki.LeafTTL

func localForwardHandler(currentDaemon func() *daemon) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a := currentDaemon()
		if a == nil {
			http.Error(w, "host-daemon: no live proxy connection", http.StatusServiceUnavailable)
			return
		}
		a.forwardHTTP(w, r)
	})
}

func localEnrollHandler(trust *localTrust, currentDaemon func() *daemon) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "host-daemon: enroll requires POST", http.StatusMethodNotAllowed)
			return
		}
		a := currentDaemon()
		if a == nil {
			http.Error(w, "host-daemon: no live proxy connection", http.StatusServiceUnavailable)
			return
		}
		token := bearerToken(r)
		now := time.Now()
		principal, ok := a.principalForBootstrapToken(token, now)
		if !ok {
			http.Error(w, "host-daemon: unrecognized bootstrap token", http.StatusUnauthorized)
			return
		}
		resp, err := trust.issueChildLeaf(principal, now)
		if err != nil {
			http.Error(w, "host-daemon: issue leaf: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(h, prefix) {
		return ""
	}
	return strings.TrimSpace(h[len(prefix):])
}

type bootstrapToken struct {
	secret    string
	expiresAt time.Time
}

func (a *daemon) registerBootstrapToken(spawnID, secret string, now time.Time) {
	a.tokenMu.Lock()
	a.spawnTokens[spawnID] = bootstrapToken{secret: secret, expiresAt: now.Add(bootstrapTokenTTL)}
	a.tokenMu.Unlock()
}

func (a *daemon) forgetBootstrapToken(spawnID string) {
	a.tokenMu.Lock()
	delete(a.spawnTokens, spawnID)
	a.tokenMu.Unlock()
}

func (a *daemon) principalForBootstrapToken(presented string, now time.Time) (string, bool) {
	if presented == "" {
		return "", false
	}
	presentedBytes := []byte(presented)
	a.tokenMu.Lock()
	defer a.tokenMu.Unlock()
	for spawnID, tok := range a.spawnTokens {
		if subtle.ConstantTimeCompare(presentedBytes, []byte(tok.secret)) != 1 {
			continue
		}
		if now.After(tok.expiresAt) {
			return "", false
		}
		tok.expiresAt = now.Add(bootstrapTokenTTL)
		a.spawnTokens[spawnID] = tok
		return spawnID, true
	}
	return "", false
}

func (a *daemon) forwardHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "host-daemon: read body: "+err.Error(), http.StatusBadGateway)
		return
	}
	_ = r.Body.Close()

	headers := JoinHeaderValues(r.Header)

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
		http.Error(w, "host-daemon: proxy connection closed", http.StatusServiceUnavailable)
		return
	}

	select {
	case resp, ok := <-respCh:
		if !ok || resp == nil {
			http.Error(w, "host-daemon: forward cancelled", http.StatusServiceUnavailable)
			return
		}
		ApplyJoinedHeaders(w.Header(), resp.GetHeaders())
		status := int(resp.GetStatus())
		if status == 0 {
			status = http.StatusOK
		}
		w.WriteHeader(status)
		_, _ = w.Write(resp.GetBody())
	case <-time.After(localForwardTimeout):
		http.Error(w, "host-daemon: forward timed out", http.StatusGatewayTimeout)
	}
}

func (a *daemon) registerForward(forwardID string) chan *genv1.LocalHttpResponse {
	ch := make(chan *genv1.LocalHttpResponse, 1)
	a.forwardMu.Lock()
	a.pendingForwards[forwardID] = ch
	a.forwardMu.Unlock()
	return ch
}

func (a *daemon) clearForward(forwardID string) {
	a.forwardMu.Lock()
	if ch, ok := a.pendingForwards[forwardID]; ok {
		delete(a.pendingForwards, forwardID)
		close(ch)
	}
	a.forwardMu.Unlock()
}

func (a *daemon) deliverHTTPResponse(resp *genv1.LocalHttpResponse) {
	a.forwardMu.Lock()
	defer a.forwardMu.Unlock()
	ch, ok := a.pendingForwards[resp.GetForwardId()]
	if !ok {
		return
	}
	delete(a.pendingForwards, resp.GetForwardId())
	ch <- resp
}
