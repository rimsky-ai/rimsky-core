// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// endpoint.go — endpoint resolution per spec §2.3 / §4.1.
//
// Two precedence rules, depending on whether a manifest is in play:
//
//   - Non-compose verbs (ResolveEndpoint):
//     flag > env > manifestContext > RIMSKY_CONTEXT > config.current_context.
//     In live use no non-compose verb passes a manifestContext, so this
//     collapses to flag > env > RIMSKY_CONTEXT > config.current_context.
//
//   - Compose verbs (ResolveEndpointForCompose, manifest's `context:` field):
//     manifestContext > flag > env > RIMSKY_CONTEXT > config.current_context.
//     When both --endpoint and manifestContext are set, the resolved
//     endpoints must match; otherwise the call errors out and the
//     operator is asked to drop one. This surfaces cross-environment
//     misfires loudly rather than silently honoring the pin.
//
// Spec §2.3 / §4.1's compose-verb override clause requires the manifest
// pin to win over flag/env for compose verbs — the manifest pins the
// deployment to prevent cross-environment misfires. ResolveEndpointFor-
// Compose implements that precedence; ResolveEndpoint preserves the
// standard (flag-highest) precedence used by every non-compose verb.
package cli

import (
	"errors"
	"fmt"
	"os"
)

// ResolveEndpoint returns the API endpoint URL for non-compose verbs.
//
// Precedence (when manifestContext is empty, the common case for
// non-compose callers):
//
//	flag > env > RIMSKY_CONTEXT > config.current_context
//
// When a manifestContext is supplied, it slots ahead of RIMSKY_CONTEXT
// but stays below flag and env:
//
//	flag > env > manifestContext > RIMSKY_CONTEXT > config.current_context
//
// Compose verbs must instead use ResolveEndpointForCompose, which moves
// manifestContext to the top of the precedence chain (spec §4.1: the
// manifest pin overrides flag/env to prevent cross-environment misfires).
func ResolveEndpoint(flag, env, cfgPath, manifestContext string) (string, error) {
	return resolveEndpoint(flag, env, cfgPath, manifestContext, false)
}

// ResolveEndpointForCompose returns the API endpoint URL for compose
// verbs. When manifestContext is non-empty it overrides flag and env
// per spec §2.3 / §4.1's compose-verb override clause: the manifest's
// `context:` field pins the deployment to prevent cross-environment
// misfires.
//
// Precedence: manifestContext > flag > env > RIMSKY_CONTEXT >
// config.current_context.
//
// When manifestContext is empty this collapses to ResolveEndpoint's
// precedence.
//
// To surface configuration mistakes loudly rather than silently
// honoring the manifest pin, when both flag and manifestContext are
// set and they resolve to different endpoints, this function returns
// an error instructing the operator to drop one. Equal endpoints are
// accepted (idempotent).
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
