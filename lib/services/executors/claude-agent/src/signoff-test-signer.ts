// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

// Test-only helper: real Ed25519 crypto, reused by signoff.test.ts and the
// acceptance e2e test. Named without a `.test.ts` suffix because it is imported
// by multiple test files; no runtime code imports it. It is excluded from the
// build via tsconfig.json's `exclude` so it never lands in dist/.

import { generateKeyPairSync, sign as edSign } from "node:crypto";
import { buildSignoffMessage } from "./signoff.js";

export interface TestSigner {
  publicKeyPem: string;
  sign(dispatchId: string, value: unknown): string; // base64
}

export function makeTestSigner(): TestSigner {
  const { publicKey, privateKey } = generateKeyPairSync("ed25519");
  const publicKeyPem = publicKey.export({ type: "spki", format: "pem" }).toString();
  return {
    publicKeyPem,
    sign: (dispatchId, value) =>
      edSign(null, buildSignoffMessage(dispatchId, value), privateKey).toString("base64"),
  };
}
