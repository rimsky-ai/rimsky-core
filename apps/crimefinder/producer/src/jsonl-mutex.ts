// Single-writer mutex per JSONL file. Promise-chain based: every withLock
// caller awaits the previous tail's release before entering the critical
// section. Throughput is LLM-paced, so the mutex doesn't bottleneck.

export class JsonlMutex {
  private queue: Promise<void> = Promise.resolve();

  async withLock<T>(fn: () => Promise<T>): Promise<T> {
    const prev = this.queue;
    let release!: () => void;
    const next = new Promise<void>((r) => {
      release = r;
    });
    this.queue = prev.then(() => next);
    await prev;
    try {
      return await fn();
    } finally {
      release();
    }
  }
}
