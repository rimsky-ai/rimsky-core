// ScopesConflict implements the producer's conflict-detection rule. Two
// zone sub-claims with disjoint file lists do NOT conflict — rimsky can
// dispatch them concurrently. Overlapping file lists DO conflict.
// Pass-state claims are single-holder per pass. Dedup-batch claims are
// indexed by (pass_id, batch_index); different batches don't conflict.
// Different scope kinds are never the same scope, so they don't conflict.
// On malformed bytes, fall back to byte-equality.

interface ZoneScope {
  kind: "source-tree-zone";
  pass_id: string;
  zone_files: string[];
}
interface PassStateScope {
  kind: "pass-state";
  pass_id: string;
}
interface DedupBatchScope {
  kind: "dedup-batch";
  pass_id: string;
  batch_index: number;
  files: string[];
}

type Parsed = ZoneScope | PassStateScope | DedupBatchScope;

function bytesEqual(a: Uint8Array, b: Uint8Array): boolean {
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i++) if (a[i] !== b[i]) return false;
  return true;
}

function tryParse(bytes: Uint8Array): Parsed | null {
  try {
    const obj = JSON.parse(new TextDecoder().decode(bytes));
    if (
      obj &&
      (obj.kind === "source-tree-zone" ||
        obj.kind === "pass-state" ||
        obj.kind === "dedup-batch")
    ) {
      return obj as Parsed;
    }
  } catch {
    return null;
  }
  return null;
}

export function scopesConflict(a: Uint8Array, b: Uint8Array): boolean {
  const sa = tryParse(a);
  const sb = tryParse(b);
  if (!sa || !sb) return bytesEqual(a, b);

  if (sa.kind === "source-tree-zone" && sb.kind === "source-tree-zone") {
    if (sa.pass_id !== sb.pass_id) return false;
    const setA = new Set(sa.zone_files);
    for (const f of sb.zone_files) if (setA.has(f)) return true;
    return false;
  }
  if (sa.kind === "pass-state" && sb.kind === "pass-state") {
    return sa.pass_id === sb.pass_id;
  }
  if (sa.kind === "dedup-batch" && sb.kind === "dedup-batch") {
    return sa.pass_id === sb.pass_id && sa.batch_index === sb.batch_index;
  }
  // Mixed kinds: not the same scope, never conflict.
  return false;
}
