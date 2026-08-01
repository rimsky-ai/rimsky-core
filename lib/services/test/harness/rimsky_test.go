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
