// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { describe, it, expect } from "vitest";

interface RecoveryAwareExecuteRequestShape {
  node_id?: string;
  instance_id?: string;
  dispatch_id?: string;
  prior_dispatch_id?: string;
  prior_dispatch_disposition?: string;
}

interface AsyncCallbackAckBody {
  ack_status: "accepted" | "rejected";
  current_dispatch_id?: string;
}

describe("recovery-aware protocol", () => {
  describe("ExecuteRequest parsing", () => {
    it("reads prior_dispatch_id and prior_dispatch_disposition off an incoming payload", () => {
      const payload = JSON.parse(JSON.stringify({
        node_id: "node-a",
        instance_id: "inst-1",
        dispatch_id: "dispatch-new",
        prior_dispatch_id: "dispatch-old",
        prior_dispatch_disposition: "stale_recovery",
      })) as RecoveryAwareExecuteRequestShape;

      expect(payload.prior_dispatch_id).toBe("dispatch-old");
      expect(payload.prior_dispatch_disposition).toBe("stale_recovery");
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
      for (const v of ["stale_recovery", "retry_after_error", "recalculate"]) {
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
      const body = JSON.parse(JSON.stringify({
        ack_status: "rejected",
      })) as AsyncCallbackAckBody;
      expect(body.ack_status).toBe("rejected");
      expect(body.current_dispatch_id).toBeUndefined();
    });
  });
});
