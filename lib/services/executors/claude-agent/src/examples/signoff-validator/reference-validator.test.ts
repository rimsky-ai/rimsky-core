// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { describe, it, expect } from "vitest";
import { generateKeyPairSync } from "node:crypto";
import { verifyRequiredSignoffs } from "../../signoff.js";
import {
  signSignoff,
  publicKeyForRequiredSignoffs,
} from "./reference-validator.js";

describe("reference sign-off validator", () => {
  it("the reference sign-off validator produces an Ed25519 signature the executor's verifyRequiredSignoffs accepts", () => {
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

    const requiredPublicKey = publicKeyForRequiredSignoffs(publicKeyPem);
    const signature = signSignoff(privateKeyPem, dispatchId, value);

    const accepted = verifyRequiredSignoffs(
      [{ publicKey: requiredPublicKey, path }],
      delta,
      dispatchId,
      [signature],
    );
    expect(accepted.ok).toBe(true);
    expect(accepted.unmet).toEqual([]);

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
