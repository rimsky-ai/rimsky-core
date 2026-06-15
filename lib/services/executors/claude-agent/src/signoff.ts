// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { createPublicKey, verify as edVerify } from "node:crypto";
import * as canonicalizeModule from "canonicalize";

/**
 * Canonicalize wraps the `canonicalize` CommonJS package, whose runtime export
 * is a bare function (`module.exports = serialize`) but whose bundled `.d.ts`
 * declares it as an ESM `export default`. Under NodeNext that mismatch makes
 * a plain default import non-callable at type-check time even though it is
 * callable at runtime. Normalize both layers here: prefer the synthesized
 * `default`, fall back to the namespace object itself (the CJS function under
 * esModuleInterop's runtime).
 */
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

/** The exact bytes a validator signs and the executor re-derives. */
export function buildSignoffMessage(dispatchId: string, value: unknown): Buffer {
  const canonical = canonicalize(value) ?? "null";
  return Buffer.from(`${SIGNOFF_DOMAIN}\n${dispatchId}\n${canonical}`, "utf8");
}

/** Dotted path into an object; undefined/empty path ⇒ the whole object. */
export function valueAtPath(obj: unknown, path?: string): unknown {
  if (!path) return obj;
  let cur: unknown = obj;
  for (const seg of path.split(".")) {
    if (cur == null || typeof cur !== "object") return undefined;
    cur = (cur as Record<string, unknown>)[seg];
  }
  return cur;
}

/**
 * For each required `{ publicKey, path }`, derive the bound value at `path`,
 * build the canonical signing message, and search the (base64) signature bag
 * for one signature that Ed25519-verifies under that key. A required entry is
 * unmet with reason `"missing"` when the bag is empty, else `"invalid"`.
 *
 * The signature bag is decoded once up front; every `edVerify` is wrapped so a
 * malformed signature or key for one required entry cannot abort the scan for
 * the others (rigor: a single bad input must not silently weaken the gate by
 * short-circuiting verification of an unrelated required entry).
 */
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
      // @deliberate: a required entry whose configured key cannot be parsed
      // can never be satisfied — treat it as invalid rather than letting the
      // throw escape.
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
        // @deliberate: malformed signature for this candidate — keep scanning the bag.
      }
    }

    if (!satisfied) unmet.push({ path, reason: "invalid" });
  }

  return { ok: unmet.length === 0, unmet };
}
