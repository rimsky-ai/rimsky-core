// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

// @deliberate: behavioral test for the copy-and-modify reference sign-off validator.
//
// The reference validator is the artifact under test; the executor's REAL
// `verifyRequiredSignoffs` (src/signoff.ts) is the value-delivering verifier.
// The test proves byte-contract compatibility: a signature the reference
// validator produces over the documented sign-off message is accepted by the
// real verifier, and a signature over a different value is rejected.
//
// This keeps the reference validator's correctness in the repo gate, not in a
// dist-excluded fixture (the contrast artifact is src/signoff-test-signer.ts).

import { describe, it, expect } from "vitest";
import { generateKeyPairSync } from "node:crypto";
import { verifyRequiredSignoffs } from "../../signoff.js";
import {
  signSignoff,
  publicKeyForRequiredSignoffs,
} from "./reference-validator.js";

describe("reference sign-off validator", () => {
  it("the reference sign-off validator produces an Ed25519 signature the executor's verifyRequiredSignoffs accepts", () => {
    // @deliberate: operator key material (Ed25519). The reference validator signs with the
    // private key; the public key (PEM SPKI) is what `cli.required_signoffs`
    // carries and what the executor's verifier re-derives the message under.
    const { publicKey, privateKey } = generateKeyPairSync("ed25519");
    const privateKeyPem = privateKey
      .export({ type: "pkcs8", format: "pem" })
      .toString();
    const publicKeyPem = publicKey
      .export({ type: "spki", format: "pem" })
      .toString();

    const dispatchId = "disp-ref-1";
    const path = "endpoints";
    const value = [{ url: "https://example.com/a" }, { url: "https://example.com/b" }];
    const delta = { [path]: value };

    // @deliberate: the reference validator emits the public key form the gate expects, and
    // signs the bound value at the gated path per the documented byte-contract.
    const requiredPublicKey = publicKeyForRequiredSignoffs(publicKeyPem);
    const signature = signSignoff(privateKeyPem, dispatchId, value);

    // @deliberate: positive: the REAL executor verifier accepts the reference signature.
    const accepted = verifyRequiredSignoffs(
      [{ publicKey: requiredPublicKey, path }],
      delta,
      dispatchId,
      [signature],
    );
    expect(accepted.ok).toBe(true);
    expect(accepted.unmet).toEqual([]);

    // @deliberate: negative: a signature over a DIFFERENT value is rejected by the real
    // verifier (the gate binds the actual bound output, not an attacker-chosen
    // value).
    const wrongSignature = signSignoff(privateKeyPem, dispatchId, [
      { url: "https://attacker.example/evil" },
    ]);
    const rejected = verifyRequiredSignoffs(
      [{ publicKey: requiredPublicKey, path }],
      delta,
      dispatchId,
      [wrongSignature],
    );
    expect(rejected.ok).toBe(false);
    expect(rejected.unmet).toEqual([{ path, reason: "invalid" }]);
  });
});
