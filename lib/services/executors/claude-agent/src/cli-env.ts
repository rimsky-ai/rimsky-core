// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

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
    const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), "claude-agent-env-"));
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
        try { fs.unlinkSync(keyFilePath); } catch {}
        try { fs.unlinkSync(helperPath); } catch {}
        try { fs.unlinkSync(path.join(claudeDir, "settings.json")); } catch {}
        try { fs.rmdirSync(claudeDir); } catch {}
        try { fs.rmdirSync(tmpDir); } catch {}
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
  return { env, cleanup: () => {} };
}
