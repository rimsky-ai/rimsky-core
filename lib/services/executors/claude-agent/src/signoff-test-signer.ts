// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { generateKeyPairSync, sign as edSign } from "node:crypto";
import { buildSignoffMessage } from "./signoff.js";

export interface TestSigner {
  publicKeyPem: string;
  sign(dispatchId: string, value: unknown): string;
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
