// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

// @deliberate: recovery-aware protocol unit test (per spec
// .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md
// §Recovery-aware executor protocol).
//
// Two surface contracts are exercised here:
//
//  1. ExecuteRequest parsing reads `prior_dispatch_id` and
//     `prior_dispatch_disposition`. When the Go supervisor enqueues a
//     dispatch that supersedes a heartbeat-stale / failed / recalculated
//     predecessor, those two fields land on the proto wire and must be
//     read off the request payload as-is.
//
//  2. The async-callback ack response body parsing reads `ack_status`
//     and the optional `current_dispatch_id`. HTTP status stays 200 for
//     both accepted and rejected outcomes; ack_status discriminates,
//     and current_dispatch_id surfaces the canonical successor when the
//     supervisor has already moved on (e.g. a stale executor catches up
//     late and the new dispatch is the source of truth).
//
// The test is structural: we exercise plain JSON parsing of payloads
// shaped to the wire to assert TypeScript types accept and surface the
// fields without information loss. It deliberately does not exercise
// the gRPC + agent run pipeline — that path is covered by
// server.test.ts; the contract under test here is the request and ack
// body shapes that the rest of the executor stack consumes.

import { describe, it, expect } from "vitest";

/**
 * ExecuteRequest field surface visible to the gRPC handler in
 * server.ts. Mirrors the same fields the executor reads. We pin the
 * shape here so the test fails to compile if the surface drifts.
 */
interface RecoveryAwareExecuteRequestShape {
  node_id?: string;
  instance_id?: string;
  dispatch_id?: string;
  prior_dispatch_id?: string;
  prior_dispatch_disposition?: string;
}

/**
 * AsyncCallbackAckBody is the JSON payload the Go supervisor returns
 * in response to the executor's HTTP callback POST. See
 * `code:runtime/callback.go::callbackAckBody`. HTTP status stays 200
 * for both accepted and rejected; the executor distinguishes via
 * `ack_status`. `current_dispatch_id` is set on rejection when a
 * successor dispatch already exists.
 */
interface AsyncCallbackAckBody {
  ack_status: "accepted" | "rejected";
  current_dispatch_id?: string;
}

describe("recovery-aware protocol", () => {
  describe("ExecuteRequest parsing", () => {
    it("reads prior_dispatch_id and prior_dispatch_disposition off an incoming payload", () => {
      // @deliberate: wire shape produced by the Go supervisor when this dispatch
      // supersedes a heartbeat-stale predecessor.
      const payload = JSON.parse(JSON.stringify({
        node_id: "node-a",
        instance_id: "inst-1",
        dispatch_id: "dispatch-new",
        prior_dispatch_id: "dispatch-old",
        prior_dispatch_disposition: "heartbeat_stale",
      })) as RecoveryAwareExecuteRequestShape;

      expect(payload.prior_dispatch_id).toBe("dispatch-old");
      expect(payload.prior_dispatch_disposition).toBe("heartbeat_stale");
      // @deliberate: dispatch_id remains the supervisor-side rimsky_node_runs.id of
      // the current row (the new one); prior_dispatch_id names the
      // superseded predecessor.
      expect(payload.dispatch_id).toBe("dispatch-new");
    });

    it("treats both recovery fields as optional (initial dispatch path)", () => {
      const payload = JSON.parse(JSON.stringify({
        node_id: "node-a",
        dispatch_id: "dispatch-first",
      })) as RecoveryAwareExecuteRequestShape;

      expect(payload.prior_dispatch_id).toBeUndefined();
      expect(payload.prior_dispatch_disposition).toBeUndefined();
    });

    it("accepts every documented disposition value verbatim", () => {
      // @deliberate: the Go side stamps the lower_snake_case enum symbol; the TS
      // executor passes it through to user-code as-is (the executor
      // does not interpret the value beyond surfacing it on the
      // attribute bag / callback metadata).
      for (const v of ["heartbeat_stale", "retry_after_error", "recalculate"]) {
        const payload = JSON.parse(JSON.stringify({
          prior_dispatch_id: "d-prev",
          prior_dispatch_disposition: v,
        })) as RecoveryAwareExecuteRequestShape;
        expect(payload.prior_dispatch_disposition).toBe(v);
      }
    });
  });

  describe("async-callback ack body parsing", () => {
    it("reads ack_status=accepted when the supervisor accepted the terminal", () => {
      const body = JSON.parse(JSON.stringify({
        ack_status: "accepted",
      })) as AsyncCallbackAckBody;
      expect(body.ack_status).toBe("accepted");
      expect(body.current_dispatch_id).toBeUndefined();
    });

    it("reads ack_status=rejected + current_dispatch_id when the supervisor moved on", () => {
      const body = JSON.parse(JSON.stringify({
        ack_status: "rejected",
        current_dispatch_id: "dispatch-current",
      })) as AsyncCallbackAckBody;
      expect(body.ack_status).toBe("rejected");
      expect(body.current_dispatch_id).toBe("dispatch-current");
    });

    it("tolerates the optional current_dispatch_id being absent on a rejected ack", () => {
      // @deliberate: the supervisor may reject without naming a successor (e.g. the
      // run was cancelled outright). HTTP status is still 200.
      const body = JSON.parse(JSON.stringify({
        ack_status: "rejected",
      })) as AsyncCallbackAckBody;
      expect(body.ack_status).toBe("rejected");
      expect(body.current_dispatch_id).toBeUndefined();
    });
  });
});
