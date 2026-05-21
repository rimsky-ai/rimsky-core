import { describe, it, expect } from "vitest";
import { JsonlMutex } from "./jsonl-mutex.js";

describe("JsonlMutex", () => {
  it("serializes concurrent calls in call order", async () => {
    const m = new JsonlMutex();
    const out: number[] = [];
    const tasks = Array.from({ length: 100 }, (_, i) =>
      m.withLock(async () => {
        // Tiny sleep to encourage interleaving without the mutex.
        await new Promise((r) => setTimeout(r, 0));
        out.push(i);
      }),
    );
    await Promise.all(tasks);
    expect(out).toEqual(Array.from({ length: 100 }, (_, i) => i));
  });

  it("releases the lock even when the body throws", async () => {
    const m = new JsonlMutex();
    await expect(
      m.withLock(async () => {
        throw new Error("boom");
      }),
    ).rejects.toThrow("boom");
    // Next call still succeeds.
    await expect(m.withLock(async () => 42)).resolves.toBe(42);
  });
});
