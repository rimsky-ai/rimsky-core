// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package hostagent

import "testing"

// @concept: host-agent
func TestReconnectLogGateStopsRepeatingOneErrorAtTheMaximumBackoff(t *testing.T) {
	var gate reconnectLogGate
	const stuck = "x509: proxy certificate is not trusted"

	emit, last := gate.decide(stuck, reconnectMinBackoff)
	if !emit || last {
		t.Fatalf("the first failure must reach the log; emit=%v last=%v", emit, last)
	}
	if emit, _ := gate.decide(stuck, reconnectMaxBackoff/2); !emit {
		t.Fatal("the same error must keep reaching the log while the backoff is still growing")
	}

	emit, last = gate.decide(stuck, reconnectMaxBackoff)
	if !emit || !last {
		t.Fatalf("the agent must say once that it is going quiet; emit=%v last=%v", emit, last)
	}
	for attempt := 0; attempt < 200; attempt++ {
		if emit, _ := gate.decide(stuck, reconnectMaxBackoff); emit {
			t.Fatalf("attempt %d repeated one unchanged error at the maximum backoff. Every repeat is noise an "+
				"operator has already read, and it buries the lines that carry new information", attempt)
		}
	}
}

// @concept: host-agent
func TestReconnectLogGateSpeaksAgainWhenTheErrorChangesOrTheAgentConnects(t *testing.T) {
	var gate reconnectLogGate
	gate.decide("proxy refused the connection", reconnectMaxBackoff)
	gate.decide("proxy refused the connection", reconnectMaxBackoff)

	if emit, _ := gate.decide("x509: proxy certificate is not trusted", reconnectMaxBackoff); !emit {
		t.Fatal("a changed error names a new failure mode and must reach the log")
	}

	gate.decide("x509: proxy certificate is not trusted", reconnectMaxBackoff)
	gate.reset()
	if emit, _ := gate.decide("x509: proxy certificate is not trusted", reconnectMaxBackoff); !emit {
		t.Fatal("after a successful connect the agent starts over, so the next failure must reach the log")
	}
}
