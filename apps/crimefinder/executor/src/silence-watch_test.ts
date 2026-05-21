import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { pino } from "pino";
import { SilenceWatch } from "./silence-watch.js";

const logger = pino({ level: "silent" });

describe("SilenceWatch", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it("fires onTimeout after the silence window", () => {
    let fired = false;
    const w = new SilenceWatch({ timeoutMs: 1000, onTimeout: () => (fired = true), logger });
    vi.advanceTimersByTime(999);
    expect(fired).toBe(false);
    vi.advanceTimersByTime(2);
    expect(fired).toBe(true);
    w.stop();
  });

  it("touch() resets the timer", () => {
    let fired = false;
    const w = new SilenceWatch({ timeoutMs: 1000, onTimeout: () => (fired = true), logger });
    vi.advanceTimersByTime(800);
    w.touch();
    vi.advanceTimersByTime(800);
    expect(fired).toBe(false);
    vi.advanceTimersByTime(300);
    expect(fired).toBe(true);
    w.stop();
  });

  it("stop() cancels the timer", () => {
    let fired = false;
    const w = new SilenceWatch({ timeoutMs: 1000, onTimeout: () => (fired = true), logger });
    w.stop();
    vi.advanceTimersByTime(5000);
    expect(fired).toBe(false);
  });
});
