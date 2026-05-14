// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// dev.go — `dev up/down/status` plus the infra-hook helpers shared with
// `compose down --infra`.
//
// dev up materializes inline rimsky_config to ./.rimsky/rimsky.yml,
// runs infra.up.command, optionally polls wait_for, then delegates to
// compose up. dev down delegates to compose down and runs
// infra.down.command when --infra is passed.
package compose

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/fallguy/rimsky/control/cli"
)

// MaterializeRimskyConfig writes m.RimskyConfig.Inline to
// <targetDir>/.rimsky/rimsky.yml when present (always overwriting).
// Returns the resolved path. If only RimskyConfig.Path is set, returns
// the absolute path of the referenced file. If neither is present,
// returns ("", nil).
func MaterializeRimskyConfig(m *Manifest, targetDir string) (string, error) {
	if m.RimskyConfig == nil {
		return "", nil
	}
	if m.RimskyConfig.Path != "" {
		abs, err := filepath.Abs(filepath.Join(targetDir, m.RimskyConfig.Path))
		if err != nil {
			return "", err
		}
		return abs, nil
	}
	if m.RimskyConfig.Inline == nil {
		return "", nil
	}
	dir := filepath.Join(targetDir, ".rimsky")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	out, err := yaml.Marshal(m.RimskyConfig.Inline)
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "rimsky.yml")
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// RunInfraUp executes infra.up.command, then polls wait_for if set.
func RunInfraUp(ctx context.Context, infra *Infra, manifestDir string) error {
	if infra == nil || infra.Up == nil {
		return nil
	}
	if len(infra.Up.Command) == 0 {
		return fmt.Errorf("infra.up.command is empty")
	}
	env := []string{"RIMSKY_PROJECT="}
	if err := runCommand(ctx, infra.Up.Command, manifestDir, env); err != nil {
		return fmt.Errorf("infra.up.command: %w", err)
	}
	if infra.Up.WaitFor == "" {
		return nil
	}
	timeout := 60 * time.Second
	if infra.Up.Timeout != "" {
		d, err := time.ParseDuration(infra.Up.Timeout)
		if err != nil {
			return err
		}
		timeout = d
	}
	deadline := time.Now().Add(timeout)
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, infra.Up.WaitFor, nil)
		if err != nil {
			return err
		}
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("wait_for %s did not return 2xx within %s", infra.Up.WaitFor, timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// RunInfraDown runs infra.down.command if defined.
func RunInfraDown(ctx context.Context, infra *Infra, manifestDir string) error {
	if infra == nil || infra.Down == nil {
		return fmt.Errorf("no infra.down command defined")
	}
	if len(infra.Down.Command) == 0 {
		return fmt.Errorf("infra.down.command is empty")
	}
	return runCommand(ctx, infra.Down.Command, manifestDir, []string{"RIMSKY_PROJECT="})
}

// RunDevUp implements `dev up`. Loads the manifest once and threads
// it through the compose-up body (via runComposeUpWithManifest), so the
// manifest is parsed once even when dev does its own infra.up step.
func RunDevUp(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("dev up", flag.ContinueOnError)
	manifest := fs.String("f", "rimsky-compose.yml", "manifest path")
	var common cli.CommonFlags
	cli.RegisterCommonFlags(fs, &common)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := common.ResolveFormat(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	cli.SetActiveCommonFlags(&common)
	m, err := LoadManifest(*manifest)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	resolveTemplatePaths(m, *manifest)
	manifestDir := filepath.Dir(*manifest)
	if _, err := MaterializeRimskyConfig(m, manifestDir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if m.Infra != nil && m.Infra.Up != nil {
		if err := RunInfraUp(ctx, m.Infra, manifestDir); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
	flags := &composeUpFlags{manifestPath: *manifest, common: common}
	c, _, code := clientForManifest(flags, m)
	if code != 0 {
		return code
	}
	return runComposeUpWithManifest(ctx, m, c, flags)
}

// RunDevDown implements `dev down`. Loads the manifest once and threads
// it through both the compose-down body (via runComposeDownWithManifest)
// and the optional infra.down hook.
func RunDevDown(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("dev down", flag.ContinueOnError)
	manifest := fs.String("f", "rimsky-compose.yml", "manifest path")
	infra := fs.Bool("infra", false, "also run infra.down.command")
	var common cli.CommonFlags
	cli.RegisterCommonFlags(fs, &common)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := common.ResolveFormat(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	cli.SetActiveCommonFlags(&common)
	m, err := LoadManifest(*manifest)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	cfgPath, _ := cli.DefaultConfigPath()
	endpoint, err := cli.ResolveEndpointForCompose(common.Endpoint, os.Getenv("RIMSKY_CONTROL_API"), cfgPath, m.Context)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	c := cli.NewClient(endpoint)
	// dev down's own --infra flag is handled here, after compose-down's
	// body runs. The compose-down body's infra flag is left false so it
	// doesn't try to also run infra.down.
	flags := &composeDownFlags{manifestPath: *manifest, infra: false, common: common}
	if code := runComposeDownWithManifest(ctx, m, c, flags); code != 0 {
		return code
	}
	if !*infra {
		return 0
	}
	if err := RunInfraDown(ctx, m.Infra, filepath.Dir(*manifest)); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// RunDevStatus is a thin wrapper around RunComposeStatus.
func RunDevStatus(ctx context.Context, args []string) int {
	return RunComposeStatus(ctx, args)
}
