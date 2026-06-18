// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package cli

import (
	"errors"
	"fmt"
	"os"
)

func ResolveEndpoint(flag, env, cfgPath, manifestContext string) (string, error) {
	return resolveEndpoint(flag, env, cfgPath, manifestContext, false)
}

func ResolveEndpointForCompose(flag, env, cfgPath, manifestContext string) (string, error) {
	if flag != "" && manifestContext != "" {
		manifestEndpoint, err := resolveManifestContext(cfgPath, manifestContext)
		if err != nil {
			return "", err
		}
		if flag != manifestEndpoint {
			return "", fmt.Errorf(
				"--endpoint %q contradicts manifest's pinned context %q (resolves to %q); "+
					"drop --endpoint or change the manifest's context: field",
				flag, manifestContext, manifestEndpoint)
		}
		return manifestEndpoint, nil
	}
	return resolveEndpoint(flag, env, cfgPath, manifestContext, true)
}

func resolveEndpoint(flag, env, cfgPath, manifestContext string, manifestPinPriority bool) (string, error) {
	if manifestPinPriority && manifestContext != "" {
		return resolveManifestContext(cfgPath, manifestContext)
	}
	if flag != "" {
		return flag, nil
	}
	if env != "" {
		return env, nil
	}
	if manifestContext != "" {
		return resolveManifestContext(cfgPath, manifestContext)
	}
	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		return "", err
	}
	if envCtx := os.Getenv("RIMSKY_CONTEXT"); envCtx != "" {
		ctx, ok := cfg.Contexts[envCtx]
		if !ok {
			return "", fmt.Errorf("RIMSKY_CONTEXT=%q not found in %s", envCtx, cfgPath)
		}
		if ctx.Endpoint == "" {
			return "", fmt.Errorf("context %q has no endpoint set", envCtx)
		}
		return ctx.Endpoint, nil
	}
	if cfg.CurrentContext != "" {
		ctx, ok := cfg.Contexts[cfg.CurrentContext]
		if !ok {
			return "", fmt.Errorf("current_context %q not defined in %s", cfg.CurrentContext, cfgPath)
		}
		if ctx.Endpoint == "" {
			return "", fmt.Errorf("context %q has no endpoint set", cfg.CurrentContext)
		}
		return ctx.Endpoint, nil
	}
	return "", errors.New("no endpoint configured: pass --endpoint, set RIMSKY_CONTROL_API, or run `rimsky ctx use <name>`")
}

func resolveManifestContext(cfgPath, manifestContext string) (string, error) {
	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		return "", err
	}
	ctx, ok := cfg.Contexts[manifestContext]
	if !ok {
		return "", fmt.Errorf("manifest pins context %q which is not defined in %s", manifestContext, cfgPath)
	}
	if ctx.Endpoint == "" {
		return "", fmt.Errorf("context %q has no endpoint set", manifestContext)
	}
	return ctx.Endpoint, nil
}
