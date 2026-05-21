import { JsonlMutex } from "../jsonl-mutex.js";

// One global mutex per producer process. Serializes all CommitFix flows
// across passes so the atomic commit-then-append-then-recovery transaction
// holds invariants without per-pass coordination overhead.
export class CommitMutex extends JsonlMutex {}
