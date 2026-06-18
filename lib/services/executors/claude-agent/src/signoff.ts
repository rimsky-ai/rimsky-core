// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { createPublicKey, verify as edVerify } from "node:crypto";
import * as canonicalizeModule from "canonicalize";

type Canonicalize = (input: unknown) => string | undefined;
const canonicalizeAny = canonicalizeModule as unknown as {
  default?: Canonicalize;
} & Canonicalize;
const canonicalize: Canonicalize = canonicalizeAny.default ?? canonicalizeAny;

export const SIGNOFF_DOMAIN = "rimsky/claude-agent/signoff/v1";

export interface RequiredSignoff {
  publicKey: string;
  path?: string;
}

export interface SignoffResult {
  ok: boolean;
  unmet: { path: string; reason: "missing" | "invalid" }[];
}

export function buildSignoffMessage(dispatchId: string, value: unknown): Buffer {
  const canonical = canonicalize(value) ?? "null";
  return Buffer.from(`${SIGNOFF_DOMAIN}\n${dispatchId}\n${canonical}`, "utf8");
}

export function valueAtPath(obj: unknown, path?: string): unknown {
  if (!path) return obj;
  let cur: unknown = obj;
  for (const seg of path.split(".")) {
    if (cur == null || typeof cur !== "object") return undefined;
    cur = (cur as Record<string, unknown>)[seg];
  }
  return cur;
}

export function verifyRequiredSignoffs(
  required: RequiredSignoff[],
  attributesDelta: Record<string, unknown> | null,
  dispatchId: string,
  signatures: string[],
): SignoffResult {
  const sigBufs = signatures.map((s) => {
    try {
      return Buffer.from(s, "base64");
    } catch {
      return Buffer.alloc(0);
    }
  });

  const unmet: { path: string; reason: "missing" | "invalid" }[] = [];

  for (const req of required) {
    const path = req.path ?? "$";
    if (sigBufs.length === 0) {
      unmet.push({ path, reason: "missing" });
      continue;
    }

    const value = valueAtPath(attributesDelta, req.path);
    const message = buildSignoffMessage(dispatchId, value);

    let keyObj;
    try {
      keyObj = createPublicKey(req.publicKey);
    } catch {
      unmet.push({ path, reason: "invalid" });
      continue;
    }

    let satisfied = false;
    for (const sig of sigBufs) {
      try {
        if (edVerify(null, message, keyObj, sig)) {
          satisfied = true;
          break;
        }
      } catch {
      }
    }

    if (!satisfied) unmet.push({ path, reason: "invalid" });
  }

  return { ok: unmet.length === 0, unmet };
}
