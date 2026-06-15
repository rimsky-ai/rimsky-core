// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import fs from "node:fs";
import os from "node:os";
import path from "node:path";

/**
 * Auth credentials read from the executor process's environment at startup.
 * At least one field must be non-empty in non-stub mode (validated in
 * `main.ts`).
 *
 * @source skillprompting/brain/src/cli-env.ts (semantic port)
 */
export interface CliAuthConfig {
  anthropicApiKey: string;
  claudeCodeOauthToken: string;
}

export interface CliEnvResult {
  env: Record<string, string>;
  cleanup: () => void;
}

/**
 * Builds a minimal, hermetic environment for the Claude CLI subprocess.
 *
 * Precedence:
 *   1. `anthropicApiKey` (production): writes the key to a 0600 temp file
 *      and points a temp `$HOME/.claude/settings.json` at an `apiKeyHelper`
 *      shell wrapper that prints it. The key never enters the child's env.
 *   2. `claudeCodeOauthToken` (dev): passes `CLAUDE_CODE_OAUTH_TOKEN` in
 *      env with the executor process's real `$HOME` (so the CLI can read
 *      its own credential cache if present).
 *
 * The parent `process.env` is intentionally NOT inherited — only `HOME`,
 * `PATH`, and (in OAuth mode) the OAuth token reach the child. This keeps
 * unrelated secrets from the executor pod (DB DSNs, internal callback
 * tokens, AWS creds, etc.) out of the spawned `claude` process.
 *
 * Callers MUST invoke `cleanup()` after the subprocess exits to remove the
 * apiKeyHelper temp dir.
 */
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
        try { fs.unlinkSync(keyFilePath); } catch { /* @deliberate: ignore */ }
        try { fs.unlinkSync(helperPath); } catch { /* @deliberate: ignore */ }
        try { fs.unlinkSync(path.join(claudeDir, "settings.json")); } catch { /* @deliberate: ignore */ }
        try { fs.rmdirSync(claudeDir); } catch { /* @deliberate: ignore */ }
        try { fs.rmdirSync(tmpDir); } catch { /* @deliberate: ignore */ }
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
