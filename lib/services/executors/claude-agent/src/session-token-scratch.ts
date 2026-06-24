export function sessionTokenToScratchBuffer(token: string): Buffer {
  return Buffer.from(token, "utf8");
}

export function sessionTokenToScratchBase64(token: string): string {
  return Buffer.from(token, "utf8").toString("base64");
}

export function sessionTokenFromScratch(
  scratch: Buffer | Uint8Array | string | null | undefined,
): string | null {
  if (scratch === null || scratch === undefined) return null;
  if (typeof scratch === "string") {
    if (scratch.length === 0) return null;
    const decoded = Buffer.from(scratch, "base64").toString("utf8");
    return decoded.length > 0 ? decoded : null;
  }
  const buf = Buffer.isBuffer(scratch) ? scratch : Buffer.from(scratch);
  if (buf.length === 0) return null;
  return buf.toString("utf8");
}
