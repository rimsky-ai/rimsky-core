// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package scenarios

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	conformance "github.com/rimsky-ai/rimsky-core/lib/protocols/conformance/executor"
)

// @decision: async-callback-persistent-registry
func init() {
	conformance.Register(conformance.Scenario{
		Name: "unknown_ack_id",
		Run: func(_ context.Context, env conformance.Env) error {
			body := strings.NewReader(`{"success":{"changed":true}}`)
			url := env.Callbacks.URL() + "/v1/callback/unknown-ack-id-conformance"
			req, err := http.NewRequest(http.MethodPost, url, body)
			if err != nil {
				return fmt.Errorf("build callback POST: %w", err)
			}
			req.Header.Set("Content-Type", "application/json")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return fmt.Errorf("POST callback with unknown ackID: %w", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
				return fmt.Errorf("unknown-ackID POST returned status %d; expected 2xx", resp.StatusCode)
			}
			return nil
		},
	})
}
