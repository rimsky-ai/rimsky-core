import crypto from "node:crypto";

const HEX_RE = /0x[0-9a-f]+/g;
const UUID_RE = /[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/g;
const DIGIT_RE = /\d+/g;
const EMPHASIS_RE = /[*_`]/g;
const WHITESPACE_RE = /\s+/g;

export function normalizeDescription(description: string): string {
  let s = description.toLowerCase();
  s = s.replace(EMPHASIS_RE, "");
  s = s.replace(UUID_RE, "<uuid>");
  s = s.replace(HEX_RE, "<hex>");
  s = s.replace(DIGIT_RE, "<num>");
  s = s.replace(WHITESPACE_RE, " ");
  s = s.trim();
  return s;
}

export interface FingerprintInput {
  file: string;
  symbol?: string;
  description: string;
}

export function computeFingerprint(args: FingerprintInput): string {
  const canonical =
    args.file + "|" + (args.symbol ?? "") + "|" + normalizeDescription(args.description);
  const hex = crypto.createHash("sha256").update(canonical).digest("hex");
  return "sha256:" + hex;
}
