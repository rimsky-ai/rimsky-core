/**
 * @source: executors/claude-agent/src/cli-env.ts
 * @diverged: false
 */

import fs from "node:fs";
import os from "node:os";
import path from "node:path";

export interface CliAuthConfig {
  anthropicApiKey: string;
  claudeCodeOauthToken: string;
}

export interface CliEnvResult {
  env: Record<string, string>;
  cleanup: () => void;
}

export function buildCliEnv(config: CliAuthConfig): CliEnvResult {
  const basePath = process.env.PATH ?? "/usr/local/bin:/usr/bin:/bin";

  if (config.anthropicApiKey) {
    const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), "crimefinder-env-"));
    const keyFilePath = path.join(tmpDir, "api-key");
    fs.writeFileSync(keyFilePath, config.anthropicApiKey, { mode: 0o600 });

    const helperPath = path.join(tmpDir, "api-key-helper.sh");
    fs.writeFileSync(helperPath, `#!/bin/sh\ncat '${keyFilePath}'`, { mode: 0o700 });

    const claudeDir = path.join(tmpDir, ".claude");
    fs.mkdirSync(claudeDir);
    fs.writeFileSync(
      path.join(claudeDir, "settings.json"),
      JSON.stringify({ apiKeyHelper: helperPath }),
    );

    return {
      env: { HOME: tmpDir, PATH: basePath },
      cleanup: () => {
        try { fs.unlinkSync(keyFilePath); } catch { /* ignore */ }
        try { fs.unlinkSync(helperPath); } catch { /* ignore */ }
        try { fs.unlinkSync(path.join(claudeDir, "settings.json")); } catch { /* ignore */ }
        try { fs.rmdirSync(claudeDir); } catch { /* ignore */ }
        try { fs.rmdirSync(tmpDir); } catch { /* ignore */ }
      },
    };
  }

  const env: Record<string, string> = {
    HOME: process.env.HOME ?? os.homedir(),
    PATH: basePath,
  };
  if (config.claudeCodeOauthToken) {
    env.CLAUDE_CODE_OAUTH_TOKEN = config.claudeCodeOauthToken;
  }
  return { env, cleanup: () => undefined };
}
