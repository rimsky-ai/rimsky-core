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

// @deliberate: unknown_ack_id asserts that the conformance receiver's
// callback handler tolerates a POST carrying an `async_ack_id` that
// nobody Registered: the receiver records it for any later awaiter
// rather than failing the HTTP request. This is the executor-side
// shape — symmetric with the supervisor's persistent-registry
// behavior under TD-persist-async-callback-registry, where an
// unknown ackID returns 404. The supervisor-side 404 contract is
// exercised by the runtime's callback handler tests; this scenario
// pins the receiver-side shape so a conformance run can demonstrate
// the round-trip without coupling to a specific supervisor build.
//
// @concept: async-callback-persistence
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
