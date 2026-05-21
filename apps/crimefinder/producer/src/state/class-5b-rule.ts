// Server-side citation rule. When a class-1-4 finding cites a concept slug,
// the description MUST quote ≥ minTokenRun consecutive load-bearing tokens
// from the concept's Boundaries: or Invariants: section verbatim — otherwise
// the row is rewritten as class-5b ("the design doc itself might be wrong").

export interface ShouldRerouteArgs {
  description: string;
  conceptBoundaries: string;
  conceptInvariants: string;
  minTokenRun?: number;
}

const EMPHASIS_RE = /[*_`]/g;
const PUNCT_ONLY_RE = /^[^\p{L}\p{N}]+$/u;

function tokenize(s: string): string[] {
  return s
    .toLowerCase()
    .replace(EMPHASIS_RE, "")
    .split(/\s+/)
    .map((t) => t.replace(/^[^\p{L}\p{N}]+|[^\p{L}\p{N}]+$/gu, ""))
    .filter((t) => t.length >= 4)
    .filter((t) => !PUNCT_ONLY_RE.test(t));
}

function hasContiguousSubsequence(needle: string[], haystack: string[], runLen: number): boolean {
  if (needle.length < runLen || haystack.length < runLen) return false;
  for (let i = 0; i + runLen <= needle.length; i++) {
    const slice = needle.slice(i, i + runLen);
    outer: for (let j = 0; j + runLen <= haystack.length; j++) {
      for (let k = 0; k < runLen; k++) {
        if (slice[k] !== haystack[j + k]) continue outer;
      }
      return true;
    }
  }
  return false;
}

export function shouldRerouteToClass5b(args: ShouldRerouteArgs): boolean {
  const run = args.minTokenRun ?? 8;
  const desc = tokenize(args.description);
  const boundaries = tokenize(args.conceptBoundaries);
  const invariants = tokenize(args.conceptInvariants);

  if (hasContiguousSubsequence(desc, boundaries, run)) return false;
  if (hasContiguousSubsequence(desc, invariants, run)) return false;
  return true;
}
