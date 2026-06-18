// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { describe, it, expect } from "vitest";
import { verifyRequiredSignoffs } from "./signoff.js";
import { makeTestSigner } from "./signoff-test-signer.js";

describe("verifyRequiredSignoffs", () => {
  it("accepts a valid per-path signature", () => {
    const signer = makeTestSigner();
    const dispatchId = "disp-1";
    const delta = { endpoints: [{ url: "x" }] };
    const sig = signer.sign(dispatchId, delta.endpoints);
    const res = verifyRequiredSignoffs(
      [{ publicKey: signer.publicKeyPem, path: "endpoints" }],
      delta,
      dispatchId,
      [sig],
    );
    expect(res.ok).toBe(true);
    expect(res.unmet).toEqual([]);
  });

  it("rejects when the signature bag is empty (missing)", () => {
    const signer = makeTestSigner();
    const dispatchId = "disp-1";
    const delta = { endpoints: [{ url: "x" }] };
    const res = verifyRequiredSignoffs(
      [{ publicKey: signer.publicKeyPem, path: "endpoints" }],
      delta,
      dispatchId,
      [],
    );
    expect(res.ok).toBe(false);
    expect(res.unmet).toEqual([{ path: "endpoints", reason: "missing" }]);
  });

  it("rejects a signature over a different value (invalid)", () => {
    const signer = makeTestSigner();
    const dispatchId = "disp-1";
    const delta = { endpoints: [{ url: "x" }] };
    const sig = signer.sign(dispatchId, [{ url: "WRONG" }]);
    const res = verifyRequiredSignoffs(
      [{ publicKey: signer.publicKeyPem, path: "endpoints" }],
      delta,
      dispatchId,
      [sig],
    );
    expect(res.ok).toBe(false);
    expect(res.unmet).toEqual([{ path: "endpoints", reason: "invalid" }]);
  });

  it("rejects a signature minted by a different key", () => {
    const required = makeTestSigner();
    const attacker = makeTestSigner();
    const dispatchId = "disp-1";
    const delta = { endpoints: [{ url: "x" }] };
    const sig = attacker.sign(dispatchId, delta.endpoints);
    const res = verifyRequiredSignoffs(
      [{ publicKey: required.publicKeyPem, path: "endpoints" }],
      delta,
      dispatchId,
      [sig],
    );
    expect(res.ok).toBe(false);
    expect(res.unmet).toEqual([{ path: "endpoints", reason: "invalid" }]);
  });

  it("rejects a signature bound to a different dispatchId (anti-replay)", () => {
    const signer = makeTestSigner();
    const delta = { endpoints: [{ url: "x" }] };
    const sig = signer.sign("other-dispatch", delta.endpoints);
    const res = verifyRequiredSignoffs(
      [{ publicKey: signer.publicKeyPem, path: "endpoints" }],
      delta,
      "disp-1",
      [sig],
    );
    expect(res.ok).toBe(false);
    expect(res.unmet).toEqual([{ path: "endpoints", reason: "invalid" }]);
  });

  it("requires every key when multiple paths are required (multi-signer AND)", () => {
    const a = makeTestSigner();
    const b = makeTestSigner();
    const dispatchId = "disp-1";
    const delta = { endpoints: [{ url: "x" }], summary: "done" };
    const sigA = a.sign(dispatchId, delta.endpoints);
    const sigB = b.sign(dispatchId, delta.summary);

    const both = verifyRequiredSignoffs(
      [
        { publicKey: a.publicKeyPem, path: "endpoints" },
        { publicKey: b.publicKeyPem, path: "summary" },
      ],
      delta,
      dispatchId,
      [sigA, sigB],
    );
    expect(both.ok).toBe(true);
    expect(both.unmet).toEqual([]);

    const onlyOne = verifyRequiredSignoffs(
      [
        { publicKey: a.publicKeyPem, path: "endpoints" },
        { publicKey: b.publicKeyPem, path: "summary" },
      ],
      delta,
      dispatchId,
      [sigA],
    );
    expect(onlyOne.ok).toBe(false);
    expect(onlyOne.unmet).toEqual([{ path: "summary", reason: "invalid" }]);
  });

  it("isolates paths: a signature for one path does not satisfy another", () => {
    const signer = makeTestSigner();
    const dispatchId = "disp-1";
    const delta = { endpoints: [{ url: "x" }], summary: "done" };
    const sigEndpoints = signer.sign(dispatchId, delta.endpoints);
    const res = verifyRequiredSignoffs(
      [{ publicKey: signer.publicKeyPem, path: "summary" }],
      delta,
      dispatchId,
      [sigEndpoints],
    );
    expect(res.ok).toBe(false);
    expect(res.unmet).toEqual([{ path: "summary", reason: "invalid" }]);
  });

  it("verifies across key-order variants (RFC 8785 canonicalization)", () => {
    const signer = makeTestSigner();
    const dispatchId = "disp-1";
    const sig = signer.sign(dispatchId, { a: 1, b: 2 });
    const delta = { payload: { b: 2, a: 1 } };
    const res = verifyRequiredSignoffs(
      [{ publicKey: signer.publicKeyPem, path: "payload" }],
      delta,
      dispatchId,
      [sig],
    );
    expect(res.ok).toBe(true);
    expect(res.unmet).toEqual([]);
  });

  it("supports a root signature over the whole delta (path omitted)", () => {
    const signer = makeTestSigner();
    const dispatchId = "disp-1";
    const delta = { endpoints: [{ url: "x" }], summary: "done" };
    const sig = signer.sign(dispatchId, delta);
    const res = verifyRequiredSignoffs(
      [{ publicKey: signer.publicKeyPem }],
      delta,
      dispatchId,
      [sig],
    );
    expect(res.ok).toBe(true);

    const missing = verifyRequiredSignoffs(
      [{ publicKey: signer.publicKeyPem }],
      delta,
      dispatchId,
      [],
    );
    expect(missing.ok).toBe(false);
    expect(missing.unmet).toEqual([{ path: "$", reason: "missing" }]);
  });
});
