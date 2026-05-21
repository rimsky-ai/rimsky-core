import { describe, it, expect } from "vitest";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { setupHarness } from "./harness.js";

const here = path.dirname(fileURLToPath(import.meta.url));

describe("scenario harness", () => {
  it("brings up a producer + tmp repo and tears down cleanly", async () => {
    const h = await setupHarness({
      fixtureDir: path.resolve(here, "fixtures/tiny-repo"),
    });
    try {
      expect(h.producer.address).toMatch(/^127\.0\.0\.1:/);
      const passes = await h.readPasses();
      expect(passes).toEqual([]);
    } finally {
      await h.teardown();
    }
  });
});
