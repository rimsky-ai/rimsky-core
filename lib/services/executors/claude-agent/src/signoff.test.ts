// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { describe, it, expect } from "vitest";
import { verifyRequiredSignoffs } from "./signoff.js";
import { makeTestSigner } from "./signoff-test-signer.js";

describe("verifyRequiredSignoffs", () => {
  // (a) a valid signature for path "endpoints" ⇒ ok:true
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

  // (b) no signatures ⇒ ok:false with reason:"missing"
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

  // (c) a signature over a different value ⇒ ok:false reason:"invalid"
  it("rejects a signature over a different value (invalid)", () => {
    const signer = makeTestSigner();
    const dispatchId = "disp-1";
    const delta = { endpoints: [{ url: "x" }] };
    // Sign a different value than the one submitted at the path.
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

  // (d) a signature from a different signer's key ⇒ unmet
  it("rejects a signature minted by a different key", () => {
    const required = makeTestSigner();
    const attacker = makeTestSigner();
    const dispatchId = "disp-1";
    const delta = { endpoints: [{ url: "x" }] };
    // The attacker signs the correct value+dispatch, but with the wrong key.
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

  // (e) anti-replay: a signature bound to a different dispatchId ⇒ unmet
  it("rejects a signature bound to a different dispatchId (anti-replay)", () => {
    const signer = makeTestSigner();
    const delta = { endpoints: [{ url: "x" }] };
    // Mint the signature against a different dispatch, then try to replay it.
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

  // (f) two required keys at two paths, both signed ⇒ ok:true; only one signed ⇒ unmet
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
      [sigA], // b's signature absent
    );
    expect(onlyOne.ok).toBe(false);
    expect(onlyOne.unmet).toEqual([{ path: "summary", reason: "invalid" }]);
  });

  // (g) per-path isolation: a signature for endpoints does not satisfy summary
  it("isolates paths: a signature for one path does not satisfy another", () => {
    const signer = makeTestSigner();
    const dispatchId = "disp-1";
    const delta = { endpoints: [{ url: "x" }], summary: "done" };
    // The same key signs only the endpoints value; the required entry is at summary.
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

  // (h) canonicalization equivalence: signature over {a:1,b:2} verifies
  //     against the key-reordered {b:2,a:1}
  it("verifies across key-order variants (RFC 8785 canonicalization)", () => {
    const signer = makeTestSigner();
    const dispatchId = "disp-1";
    // Signature minted over the value with keys in one order.
    const sig = signer.sign(dispatchId, { a: 1, b: 2 });
    // Submitted delta carries the same value with keys reordered.
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

  // Root-path binding (omitted path ⇒ whole attributes_delta), confirming the
  // "$" placeholder used in unmet reporting and root signing both work.
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
