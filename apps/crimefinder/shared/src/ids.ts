/**
 * @source: prototype-repo:src/shared/ids.ts
 *          prototype-repo:src/features/zones/partition.ts (for generateZoneId)
 * @diverged: true
 * @reason: consolidated generateZoneId (originally in partition.ts) into
 *          the shared ID-generator module; extended ID set (added pass /
 *          row / session-token prefixes); base32 charset normalized to
 *          RFC4648 lower-case.
 */

import crypto from "node:crypto";

// RFC 4648 lower-case base32 alphabet.
const BASE32_ALPHABET = "abcdefghijklmnopqrstuvwxyz234567";

function randomBase32(length: number): string {
  const bytes = crypto.randomBytes(length);
  let out = "";
  for (let i = 0; i < length; i++) {
    out += BASE32_ALPHABET[bytes[i] % 32];
  }
  return out;
}

function bytesToBase32(buf: Buffer, length: number): string {
  let bits = 0;
  let value = 0;
  let out = "";
  for (let i = 0; i < buf.length && out.length < length; i++) {
    value = (value << 8) | buf[i];
    bits += 8;
    while (bits >= 5 && out.length < length) {
      out += BASE32_ALPHABET[(value >>> (bits - 5)) & 31];
      bits -= 5;
    }
  }
  return out;
}

export function generatePassId(): string {
  return `p_${randomBase32(24)}`;
}

export function generateFindingId(): string {
  return `f_${randomBase32(24)}`;
}

export function generateZoneId(label: string): string {
  const hash = crypto.createHash("sha256").update(label).digest();
  return `z_${bytesToBase32(hash, 12)}`;
}

export function generateRowId(): string {
  return randomBase32(24);
}

export function generateSessionToken(): string {
  return randomBase32(32);
}
