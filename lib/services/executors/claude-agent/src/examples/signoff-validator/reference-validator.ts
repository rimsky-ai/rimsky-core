// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

// Reference sign-off validator (copy-and-modify) — Apache-licensed.
//
// This is the copyable artifact a host operator adapts to build their own
// sign-off validator for the claude-agent sign-off gate. It demonstrates the
// exact bytes a validator signs (the SIGNOFF_DOMAIN / dispatch_id / canonical
// value message) and how to produce an Ed25519 signature the executor's
// `verifyRequiredSignoffs` accepts, plus how to emit the public key in the PEM
// SPKI form that `cli.required_signoffs` expects.
//
// Copy-and-modify guidance for an operator building a real validator:
//   1. Replace `signSignoff`'s private-key source with however your validator
//      holds its signing key (an HSM handle, a KMS sign call, a key file).
//   2. Keep `buildSignoffMessage` byte-for-byte: the executor re-derives the
//      identical message under the operator's public key, so any divergence in
//      the domain string, the newline framing, or the JCS canonicalization
//      makes every signature fail verification.
//   3. Publish the public key in PEM SPKI form (see
//      `publicKeyForRequiredSignoffs`) and paste it into the node's
//      `cli.required_signoffs[].public_key`.
//
// The byte-contract below is a deliberate, tracked copy of the executor's own
// message builder so this reference file is self-contained (an operator can lift
// it wholesale). If the executor's signing contract changes, this copy must be
// updated in lockstep.
// @source: lib/services/executors/claude-agent/src/signoff.ts::buildSignoffMessage
// @diverged: false

import { sign as edSign, createPublicKey } from "node:crypto";
import * as canonicalizeModule from "canonicalize";

// `canonicalize` is a CommonJS package whose runtime export is a bare function
// (`module.exports = serialize`), but whose bundled `.d.ts` declares it as an
// ESM `export default`. Under NodeNext that mismatch makes a plain default
// import non-callable at type-check time even though it is callable at runtime.
// Normalize both layers here: prefer the synthesized `default`, fall back to the
// namespace object itself (the CJS function under esModuleInterop's runtime).
// @source: lib/services/executors/claude-agent/src/signoff.ts (canonicalize import normalization)
type Canonicalize = (input: unknown) => string | undefined;
const canonicalizeAny = canonicalizeModule as unknown as {
  default?: Canonicalize;
} & Canonicalize;
const canonicalize: Canonicalize = canonicalizeAny.default ?? canonicalizeAny;

/**
 * The signing domain separator. Must match the executor's `SIGNOFF_DOMAIN`
 * exactly — it is part of the signed bytes and binds the signature to this
 * gate's purpose (anti cross-protocol signature reuse).
 * @source: lib/services/executors/claude-agent/src/signoff.ts::SIGNOFF_DOMAIN
 */
export const SIGNOFF_DOMAIN = "rimsky/claude-agent/signoff/v1";

/**
 * The exact bytes a validator signs and the executor re-derives:
 * `SIGNOFF_DOMAIN ‖ "\n" ‖ dispatchId ‖ "\n" ‖ canonical_json(value)`.
 *
 * `value` is JCS/RFC-8785-canonicalized so signer and verifier agree on the
 * byte representation regardless of key order or whitespace; a `null`/undefined
 * value canonicalizes to the literal string `"null"` (matching the executor).
 * @source: lib/services/executors/claude-agent/src/signoff.ts::buildSignoffMessage
 */
export function buildSignoffMessage(dispatchId: string, value: unknown): Buffer {
  const canonical = canonicalize(value) ?? "null";
  return Buffer.from(`${SIGNOFF_DOMAIN}\n${dispatchId}\n${canonical}`, "utf8");
}

/**
 * Produce a base64 Ed25519 signature over the documented sign-off message
 * (`SIGNOFF_DOMAIN ‖ "\n" ‖ dispatchId ‖ "\n" ‖ canonical_json(value)`) for the
 * given bound value, signed with the operator's Ed25519 private key (PEM PKCS#8).
 *
 * Ed25519 takes no separate hash algorithm — `edSign(null, …)` signs the raw
 * message bytes, which is exactly what the executor's verifier checks via
 * `edVerify(null, …)`.
 *
 * @param privateKeyPem operator's Ed25519 private key, PEM PKCS#8
 * @param dispatchId    the dispatch the sign-off is bound to (anti-replay)
 * @param value         the bound output value at the gated path
 * @returns base64-encoded Ed25519 signature, as carried in `report_complete`'s
 *          signoffs bag
 */
export function signSignoff(
  privateKeyPem: string,
  dispatchId: string,
  value: unknown,
): string {
  const message = buildSignoffMessage(dispatchId, value);
  return edSign(null, message, privateKeyPem).toString("base64");
}

/**
 * Emit the public key, in PEM SPKI form, that an operator pastes into a node's
 * `cli.required_signoffs[].public_key`. Accepts any `node:crypto`-importable
 * public-key form (PEM SPKI, PEM PKCS#1, DER, or a JWK-bearing object string)
 * and normalizes it to the canonical PEM SPKI string the executor parses with
 * `createPublicKey`.
 *
 * Normalizing here (rather than trusting the caller's input form) guarantees the
 * emitted key is byte-stable and verifier-importable: the executor's
 * `verifyRequiredSignoffs` calls `createPublicKey(req.publicKey)`, so an
 * un-normalized or malformed key would silently mark every required entry
 * `invalid`.
 *
 * @param publicKeyPem operator's Ed25519 public key (any importable form)
 * @returns the canonical PEM SPKI string for `cli.required_signoffs`
 */
export function publicKeyForRequiredSignoffs(publicKeyPem: string): string {
  const key = createPublicKey(publicKeyPem);
  return key.export({ type: "spki", format: "pem" }).toString();
}
