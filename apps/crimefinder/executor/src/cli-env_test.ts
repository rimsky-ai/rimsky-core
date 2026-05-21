import { describe, it, expect } from "vitest";
import fs from "node:fs";
import path from "node:path";
import { buildCliEnv } from "./cli-env.js";

describe("buildCliEnv", () => {
  it("anthropicApiKey path materializes a 0600 key + apiKeyHelper settings", () => {
    const r = buildCliEnv({ anthropicApiKey: "sk-test-xyz", claudeCodeOauthToken: "" });
    try {
      expect(r.env.HOME).toBeTruthy();
      expect(r.env.PATH).toBeTruthy();
      expect(r.env.CLAUDE_CODE_OAUTH_TOKEN).toBeUndefined();
      const settings = JSON.parse(
        fs.readFileSync(path.join(r.env.HOME, ".claude", "settings.json"), "utf-8"),
      );
      expect(settings.apiKeyHelper).toContain("api-key-helper.sh");
    } finally {
      r.cleanup();
    }
  });

  it("OAuth path passes the env var through with the real HOME", () => {
    const r = buildCliEnv({ anthropicApiKey: "", claudeCodeOauthToken: "oat_abc" });
    try {
      expect(r.env.CLAUDE_CODE_OAUTH_TOKEN).toBe("oat_abc");
    } finally {
      r.cleanup();
    }
  });
});
