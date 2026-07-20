// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package claudeagent

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type CliAuthConfig struct {
	AnthropicAPIKey      string
	ClaudeCodeOauthToken string
}

type CliEnvResult struct {
	Env     map[string]string
	Cleanup func()
}

func BuildCliEnv(config CliAuthConfig) (CliEnvResult, error) {
	basePath := os.Getenv("PATH")
	if basePath == "" {
		basePath = "/usr/local/bin:/usr/bin:/bin"
	}

	if config.AnthropicAPIKey != "" {
		tmpDir, err := os.MkdirTemp("", "claude-agent-env-")
		if err != nil {
			return CliEnvResult{}, err
		}
		succeeded := false
		defer func() {
			if !succeeded {
				_ = os.RemoveAll(tmpDir)
			}
		}()
		keyFilePath := filepath.Join(tmpDir, "api-key")
		if err := os.WriteFile(keyFilePath, []byte(config.AnthropicAPIKey), 0o600); err != nil {
			return CliEnvResult{}, err
		}
		helperPath := filepath.Join(tmpDir, "api-key-helper.sh")
		helper := "#!/bin/sh\ncat '" + keyFilePath + "'"
		if err := os.WriteFile(helperPath, []byte(helper), 0o700); err != nil {
			return CliEnvResult{}, err
		}
		claudeDir := filepath.Join(tmpDir, ".claude")
		if err := os.Mkdir(claudeDir, 0o755); err != nil {
			return CliEnvResult{}, err
		}
		settings, err := json.Marshal(map[string]string{"apiKeyHelper": helperPath})
		if err != nil {
			return CliEnvResult{}, err
		}
		if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), settings, 0o644); err != nil {
			return CliEnvResult{}, err
		}
		succeeded = true
		return CliEnvResult{
			Env: map[string]string{"HOME": tmpDir, "PATH": basePath},
			Cleanup: func() {
				_ = os.RemoveAll(tmpDir)
			},
		}, nil
	}

	home := os.Getenv("HOME")
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	env := map[string]string{"HOME": home, "PATH": basePath}
	if config.ClaudeCodeOauthToken != "" {
		env["CLAUDE_CODE_OAUTH_TOKEN"] = config.ClaudeCodeOauthToken
	}
	return CliEnvResult{Env: env, Cleanup: func() {}}, nil
}
