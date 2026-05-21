export interface TestResult {
  exitCode: number;
  stdoutTail: string;
  stderrTail: string;
  ranAt: string;
  treeMtimeAtRun: number;
  commandSha: string;
}

export class TestCache {
  private readonly byPass = new Map<string, TestResult>();

  get(passId: string, currentMtime: number, commandSha: string): TestResult | null {
    const cached = this.byPass.get(passId);
    if (!cached) return null;
    if (cached.commandSha !== commandSha) return null;
    if (currentMtime > cached.treeMtimeAtRun) return null;
    return cached;
  }

  set(passId: string, result: TestResult): void {
    this.byPass.set(passId, result);
  }

  // peek returns whatever's cached for the pass, ignoring mtime/sha gates.
  // Use only for observability (e.g. "did the next call hit the cache?").
  peek(passId: string): TestResult | null {
    return this.byPass.get(passId) ?? null;
  }
}
