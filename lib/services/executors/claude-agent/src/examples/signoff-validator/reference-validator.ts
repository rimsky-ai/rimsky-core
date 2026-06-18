// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { sign as edSign, createPublicKey } from "node:crypto";
import * as canonicalizeModule from "canonicalize";

type Canonicalize = (input: unknown) => string | undefined;
const canonicalizeAny = canonicalizeModule as unknown as {
  default?: Canonicalize;
} & Canonicalize;
const canonicalize: Canonicalize = canonicalizeAny.default ?? canonicalizeAny;

export const SIGNOFF_DOMAIN = "rimsky/claude-agent/signoff/v1";

export function buildSignoffMessage(dispatchId: string, value: unknown): Buffer {
  const canonical = canonicalize(value) ?? "null";
  return Buffer.from(`${SIGNOFF_DOMAIN}\n${dispatchId}\n${canonical}`, "utf8");
}

export function signSignoff(
  privateKeyPem: string,
  dispatchId: string,
  value: unknown,
): string {
  const message = buildSignoffMessage(dispatchId, value);
  return edSign(null, message, privateKeyPem).toString("base64");
}

export function publicKeyForRequiredSignoffs(publicKeyPem: string): string {
  const key = createPublicKey(publicKeyPem);
  return key.export({ type: "spki", format: "pem" }).toString();
}
