export type FindingClass = 1 | 2 | 3 | 4 | "5a" | "5b";

const VALID_WIRE = new Set(["1", "2", "3", "4", "5a", "5b"]);

export function encodeClass(c: FindingClass): string {
  return typeof c === "number" ? String(c) : c;
}

export function decodeClass(s: string): FindingClass {
  if (typeof s !== "string") {
    throw new TypeError(`expected string class on the wire, got ${typeof s}`);
  }
  if (!VALID_WIRE.has(s)) {
    throw new Error(`invalid wire class: ${JSON.stringify(s)}`);
  }
  if (s === "5a" || s === "5b") return s;
  return Number(s) as 1 | 2 | 3 | 4;
}

export function isFindingClass(value: unknown): value is FindingClass {
  return (
    value === 1 ||
    value === 2 ||
    value === 3 ||
    value === 4 ||
    value === "5a" ||
    value === "5b"
  );
}
