// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package harness

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestBringUpRimsky_HealthGreen(t *testing.T) {
	ep := BringUpRimsky(context.Background(), t)
	status, body := ep.GetJSON(t, "/v1/health", "")
	if status != http.StatusOK {
		t.Fatalf("/v1/health = %d, want 200; body=%s", status, string(body))
	}
}

func TestWritePeerBlocks_QuotesNamesAndTokensForYAMLSafety(t *testing.T) {
	cb := &configBuilder{
		claimProducers: map[string]producerCfg{
			"svc: evil": {endpoint: "http://x", writeSemanticsAllowed: []string{"at: least: once", "exactly_once"}},
		},
		executors: map[string]executorCfg{
			"exec, name": {endpoint: "http://y", transport: "grpc"},
		},
		publishers: map[string]publisherCfg{
			"pub[name]": {endpoint: "http://z"},
		},
		namedLocks: map[string]int{"lock: name": 1},
	}
	var b strings.Builder
	writePeerBlocks(&b, cb)

	var doc map[string]any
	if err := yaml.Unmarshal([]byte(b.String()), &doc); err != nil {
		t.Fatalf("rendered config is not valid YAML: %v\n%s", err, b.String())
	}

	cps, _ := doc["claim_producers"].(map[string]any)
	if _, ok := cps["svc: evil"]; !ok {
		t.Errorf("claim_producers key with YAML-significant chars did not round-trip: %+v", cps)
	}
	execs, _ := doc["executors"].(map[string]any)
	if _, ok := execs["exec, name"]; !ok {
		t.Errorf("executors key with YAML-significant chars did not round-trip: %+v", execs)
	}
	pubs, _ := doc["publishers"].(map[string]any)
	if _, ok := pubs["pub[name]"]; !ok {
		t.Errorf("publishers key with YAML-significant chars did not round-trip: %+v", pubs)
	}
	locks, _ := doc["named_locks"].(map[string]any)
	if _, ok := locks["lock: name"]; !ok {
		t.Errorf("named_locks key did not round-trip: %+v", locks)
	}
}

// @decision: peer-auth-mtls
// @story: peer-auth-mtls-mutual
func TestWritePeerBlocks_LeavesExecutorTLSToThePeerAuthDefault(t *testing.T) {
	for _, tc := range []struct {
		name         string
		peerAuthMTLS bool
		override     string
		wantKeyGone  bool
		wantValue    string
	}{
		{name: "mtls with no override leaves tls unset so the flip implies required", peerAuthMTLS: true, wantKeyGone: true},
		{name: "default posture leaves tls unset so the config default off applies", wantKeyGone: true},
		{name: "an explicit override survives the flip", peerAuthMTLS: true, override: "off", wantValue: "off"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cb := &configBuilder{
				claimProducers: map[string]producerCfg{},
				publishers:     map[string]publisherCfg{},
				namedLocks:     map[string]int{},
				peerAuthMTLS:   tc.peerAuthMTLS,
				executors: map[string]executorCfg{
					"exec": {endpoint: "exec:9091", transport: "grpc", tlsOverride: tc.override},
				},
			}
			var b strings.Builder
			writePeerBlocks(&b, cb)
			var doc map[string]any
			if err := yaml.Unmarshal([]byte(b.String()), &doc); err != nil {
				t.Fatalf("rendered config is not valid YAML: %v\n%s", err, b.String())
			}
			execs, _ := doc["executors"].(map[string]any)
			entry, _ := execs["exec"].(map[string]any)
			got, present := entry["tls"]
			if tc.wantKeyGone {
				if present {
					t.Fatalf("tls = %v, want the key absent — hardcoding it here would override the very "+
						"peer_auth default the harness exists to exercise\n%s", got, b.String())
				}
				return
			}
			if got != tc.wantValue {
				t.Fatalf("tls = %v, want %q", got, tc.wantValue)
			}
		})
	}
}
