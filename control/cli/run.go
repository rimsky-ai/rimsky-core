// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// run.go — ergonomic top-level verbs.
//
// `run`, `register`, `deploy`, `undeploy`, `instantiate`, `rm-instance`,
// `ls`, `logs` — all aliases or thin compositions over the literal
// subgroup verbs in templates.go / instances.go / nodes.go.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"
)

// RunRegister aliases `template register`.
func RunRegister(ctx context.Context, args []string) int { return RunTemplateRegister(ctx, args) }

// RunDeploy aliases `template deploy`.
func RunDeploy(ctx context.Context, args []string) int { return RunTemplateDeploy(ctx, args) }

// RunUndeploy aliases `template undeploy`.
func RunUndeploy(ctx context.Context, args []string) int { return RunTemplateUndeploy(ctx, args) }

// RunInstantiate aliases `instance create`.
func RunInstantiate(ctx context.Context, args []string) int { return RunInstanceCreate(ctx, args) }

// RunRmInstance aliases `instance delete`.
func RunRmInstance(ctx context.Context, args []string) int { return RunInstanceDelete(ctx, args) }

// RunLs implements the polymorphic `ls [templates|instances|tags]`.
func RunLs(ctx context.Context, args []string) int {
	if len(args) == 0 {
		return RunInstanceList(ctx, nil)
	}
	switch args[0] {
	case "templates":
		return RunTemplateList(ctx, args[1:])
	case "instances":
		return RunInstanceList(ctx, args[1:])
	case "tags":
		return RunTagList(ctx, args[1:])
	}
	// Treat unrecognized first positional as instance-list args.
	return RunInstanceList(ctx, args)
}

// RunLogs aliases `instance events --follow`.
func RunLogs(ctx context.Context, args []string) int {
	return RunInstanceEvents(ctx, append([]string{"--follow"}, args...))
}

// RunRun is the composed `run` verb: register + deploy + create. With
// --no-keep, polls until terminal then cleans up.
func RunRun(ctx context.Context, args []string) int {
	var (
		params       string
		key          string
		tag          string
		keep         bool
		noKeep       bool
		pollInterval time.Duration
		timeout      time.Duration
	)
	fs, common, endpoint, code := runWithCommon("run", args, func(fs *flag.FlagSet) {
		fs.StringVar(&params, "params", "", "JSON object or @file path")
		fs.StringVar(&key, "instance-key", "", "instance_key")
		fs.StringVar(&tag, "tag", "", "tag to attach to the registered template")
		fs.BoolVar(&keep, "keep", true, "leave the instance and template after creation (default)")
		fs.BoolVar(&noKeep, "no-keep", false, "delete instance and template after terminal state")
		fs.DurationVar(&pollInterval, "poll-interval", time.Second, "poll interval when --no-keep")
		fs.DurationVar(&timeout, "timeout", 0, "max wait for terminal state when --no-keep (0 = unbounded)")
	})
	if code != 0 {
		return code
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprintln(os.Stderr, "usage: rimsky run <file> [--params ...] [--key ...] [--tag ...] [--no-keep]")
		return 2
	}
	if noKeep {
		keep = false
	}
	pp, err := parseParams(params)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if tag != "" && strings.HasPrefix(tag, ReservedTagPrefix) {
		fmt.Fprintf(os.Stderr, "tag %q uses reserved prefix %q\n", tag, ReservedTagPrefix)
		return 2
	}
	spec, err := readSpecFile(rest[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	c := NewClient(endpoint)
	c.SetAPIKey(common.ResolveAPIKey(os.Getenv("RIMSKY_API_KEY")))

	tpl, err := c.RegisterTemplate(ctx, RegisterTemplateRequest{Spec: spec, Tag: tag})
	if err != nil {
		return reportError(err)
	}
	hash := tpl.Hash()
	if _, err := c.DeployTemplate(ctx, hash); err != nil {
		return reportError(err)
	}
	body := CreateInstanceRequest{Template: hash, Params: pp}
	if key != "" {
		body.InstanceKey = &key
	}
	inst, err := c.CreateInstance(ctx, body)
	if err != nil {
		return reportError(err)
	}

	if common.Format == FormatJSON {
		_ = EmitJSON(os.Stdout, inst)
	} else {
		fmt.Fprintf(os.Stdout, "instance_id=%s\n", inst.UUID())
	}

	if keep {
		return 0
	}
	return waitAndCleanup(ctx, c, inst.UUID(), hash, pollInterval, timeout)
}

// waitAndCleanup polls GetInstance until terminal, then deletes the
// instance and undeploy + delete the template. 409s on undeploy / delete-
// template are degraded to warnings rather than hard failures (per spec
// §1.3).
func waitAndCleanup(ctx context.Context, c *Client, instanceID, hash string, pollInterval, timeout time.Duration) int {
	signalCtx, cancel := signal.NotifyContext(ctx, os.Interrupt)
	defer cancel()
	deadline := time.Time{}
	if timeout > 0 {
		deadline = time.Now().Add(timeout)
	}
	for {
		inst, err := c.GetInstance(signalCtx, instanceID)
		if err != nil {
			return reportError(err)
		}
		if inst.TerminatedAt != nil {
			break
		}
		if !deadline.IsZero() && time.Now().After(deadline) {
			fmt.Fprintln(os.Stderr, "timeout waiting for terminal state")
			return 1
		}
		select {
		case <-signalCtx.Done():
			return 0
		case <-time.After(pollInterval):
		}
	}
	if err := c.DeleteInstance(signalCtx, instanceID); err != nil {
		return reportError(err)
	}
	if _, err := c.UndeployTemplate(signalCtx, hash); err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.Status == 409 {
			fmt.Fprintf(os.Stderr, "warn: undeploy %s skipped (still referenced)\n", hash)
		} else {
			return reportError(err)
		}
	}
	if err := c.DeleteTemplate(signalCtx, hash); err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.Status == 409 {
			fmt.Fprintf(os.Stderr, "warn: delete %s skipped (still referenced)\n", hash)
		} else {
			return reportError(err)
		}
	}
	fmt.Fprintln(os.Stdout, "cleanup complete")
	return 0
}
