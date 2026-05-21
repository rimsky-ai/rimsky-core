// Vitest config for the slow / subprocess-orchestrating integration
// suite. Excluded from `npm test` by being in a separate config; the
// default config only matches `scenarios/**` and `e2e/**`.
import { defineConfig } from "vitest/config";
export default defineConfig({
  test: {
    include: ["integration/**/*.test.ts"],
    testTimeout: 180_000,
    hookTimeout: 60_000,
  },
});
