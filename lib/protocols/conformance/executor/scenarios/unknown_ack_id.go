// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package scenarios

// @deliberate: unknown_ack_id is intentionally NOT registered in Plan C v1.
// Validating the executor's handling of an unknown async_ack_id requires an
// orchestrator/supervisor round-trip (the supervisor sends an async callback
// referencing an ack id that the executor never issued). `rimsky conformance
// executor` dials an executor directly; it doesn't start a supervisor. The
// scenario is therefore out of scope for v1 conformance and will be added
// once the conformance suite is extended to cover supervisor↔executor async
// callback flows. Kept as a placeholder file so the deliverable count matches
// the plan and future authors have a documented home for the scenario.
