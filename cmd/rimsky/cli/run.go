// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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
	"strconv"
	"strings"
	"time"
)

// RepeatedFlag collects a string flag that may be passed multiple times,
// preserving declaration order. Used by `run`'s --param and --service.
type RepeatedFlag []string

func (r *RepeatedFlag) String() string { return strings.Join(*r, ",") }
func (r *RepeatedFlag) Set(v string) error {
	*r = append(*r, v)
	return nil
}

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
	return RunInstanceList(ctx, args)
}

// RunLogs aliases `instance events --follow`.
func RunLogs(ctx context.Context, args []string) int {
	return RunInstanceEvents(ctx, append([]string{"--follow"}, args...))
}

// RunRun is the composed `run` verb: register + deploy + create. With
// --no-keep, polls until terminal then cleans up.
//
// Additive flags (per spec 2026-05-24-host-agent-and-proxy-design.md):
//   - --template <name>: name an already-registered template instead of
//     passing a positional spec <file>. Mutually exclusive with <file>.
//   - --param k=v (repeatable): merged into the params map; merged with
//     --params JSON with later-wins precedence (--params is applied first,
//     then each --param k=v in declaration order).
//   - --service <name>=<path> | <name> (repeatable): per-instance late-bound
//     service binding. A bare name resolves via the alias files; supplying
//     any --service auto-starts the local host-agent if it is not already
//     running (PID-existence check on ~/.rimsky/agent.pid).
func RunRun(ctx context.Context, args []string) int {
	var (
		params            string
		templateName      string
		key               string
		tag               string
		keep              bool
		noKeep            bool
		terminateAfterRun bool
		pollInterval      time.Duration
		timeout           time.Duration
		paramKV           RepeatedFlag
		services          RepeatedFlag
	)
	fs, common, endpoint, code := runWithCommon("run", args, func(fs *flag.FlagSet) {
		fs.StringVar(&params, "params", "", "JSON object or @file path")
		fs.StringVar(&templateName, "template", "", "name of an already-registered template (mutually exclusive with <file>)")
		fs.StringVar(&key, "instance-key", "", "instance_key")
		fs.StringVar(&tag, "tag", "", "tag to attach to the registered template")
		fs.BoolVar(&keep, "keep", true, "leave the instance and template after creation (default)")
		fs.BoolVar(&noKeep, "no-keep", false, "delete instance and template after terminal state")
		fs.BoolVar(&terminateAfterRun, "terminate-after-run", false,
			"create the instance with terminate_after_run=true — it self-terminates once its nodes settle, "+
				"so terminal-flag polling (e.g. `rimsky watch`) exits. Implied by --no-keep.")
		fs.DurationVar(&pollInterval, "poll-interval", time.Second, "poll interval when --no-keep")
		fs.DurationVar(&timeout, "timeout", 0, "max wait for terminal state when --no-keep (0 = unbounded)")
		fs.Var(&paramKV, "param", "k=v param (repeatable); merged over --params (later wins)")
		fs.Var(&services, "service", "late-bound service binding: <name>=<path> or bare <name> (alias). "+
			"Supplying any --service auto-starts the local agent if its ~/.rimsky/agent.pid is not live")
	})
	if code != 0 {
		return code
	}
	rest := fs.Args()

	if templateName != "" && len(rest) > 0 {
		fmt.Fprintln(os.Stderr, "rimsky run: --template and a positional <file> are mutually exclusive")
		return 2
	}
	if templateName == "" && len(rest) != 1 {
		fmt.Fprintln(os.Stderr, "usage: rimsky run {<file>|--template <name>} [--params ...] [--param k=v ...] [--service <name>=<path> ...] [--instance-key ...] [--tag ...] [--no-keep]")
		return 2
	}
	// @constraint: "Don't keep the instance" can be expressed two equivalent ways —
	// `--no-keep` (sets noKeep=true) or `--keep=false` (clears keep).
	// Both must drive the same waitAndCleanup path AND both must imply
	// terminate-after-run, otherwise the polling loop in waitAndCleanup
	// hangs forever waiting on a terminated_at flip that durable-by-
	// default semantics never produce. Coalesce here so the rest of the
	// flow keys off the single `keep` boolean.
	if noKeep || !keep {
		keep = false
		terminateAfterRun = true
	}

	pp, err := mergeParams(params, paramKV)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}

	bindings, err := resolveServiceBindings(services)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}

	if tag != "" && strings.HasPrefix(tag, ReservedTagPrefix) {
		fmt.Fprintf(os.Stderr, "tag %q uses reserved prefix %q\n", tag, ReservedTagPrefix)
		return 2
	}

	c := NewClient(endpoint)
	c.SetAPIKey(common.ResolveAPIKey(os.Getenv("RIMSKY_API_KEY")))

	var hash string
	if templateName != "" {
		tpl, rerr := c.GetTemplate(ctx, templateName)
		if rerr != nil {
			return reportError(rerr)
		}
		hash = tpl.Hash()
	} else {
		spec, rerr := readSpecFile(rest[0])
		if rerr != nil {
			fmt.Fprintln(os.Stderr, rerr)
			return 2
		}
		tpl, terr := c.RegisterTemplate(ctx, RegisterTemplateRequest{Spec: spec, Tag: tag})
		if terr != nil {
			return reportError(terr)
		}
		hash = tpl.Hash()
	}
	if _, err := c.DeployTemplate(ctx, hash); err != nil {
		return reportError(err)
	}

	if len(bindings) > 0 {
		if startErr := ensureAgentRunning(); startErr != nil {
			fmt.Fprintf(os.Stderr, "rimsky run: could not start host-agent: %v\n", startErr)
			return 1
		}
	}

	body := CreateInstanceRequest{Template: hash, Params: pp}
	if key != "" {
		body.InstanceKey = &key
	}
	if len(bindings) > 0 {
		body.ServiceBindings = bindings
	}
	if terminateAfterRun {
		body.TerminateAfterRun = true
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

// mergeParams builds the params map from the --params JSON blob (applied
// first) then overlays each --param k=v in declaration order (later wins).
// Returns nil when neither source contributes anything.
func mergeParams(paramsJSON string, kvs RepeatedFlag) (map[string]any, error) {
	base, err := parseParams(paramsJSON)
	if err != nil {
		return nil, err
	}
	if len(kvs) == 0 {
		return base, nil
	}
	out := map[string]any{}
	for k, v := range base {
		out[k] = v
	}
	for _, kv := range kvs {
		k, v, found := strings.Cut(kv, "=")
		if !found || k == "" {
			return nil, fmt.Errorf("--param %q: expected k=v", kv)
		}
		out[k] = coerceParamValue(v)
	}
	return out, nil
}

// coerceParamValue interprets a --param value string as a bool, integer, or
// float when it parses cleanly, otherwise leaving it as a string. This keeps
// `--param count=3` an integer and `--param enabled=true` a bool without
// requiring the user to write JSON; ambiguous values stay strings.
func coerceParamValue(v string) any {
	switch v {
	case "true":
		return true
	case "false":
		return false
	}
	if i, err := strconv.ParseInt(v, 10, 64); err == nil {
		return i
	}
	if f, err := strconv.ParseFloat(v, 64); err == nil {
		return f
	}
	return v
}

// resolveServiceBindings turns each --service flag value into a binding.
// `name=path` is explicit; a bare `name` resolves via the alias files
// (Task 53). Returns nil when no --service flags were supplied.
func resolveServiceBindings(values RepeatedFlag) (map[string]bindingSpec, error) {
	if len(values) == 0 {
		return nil, nil
	}
	var aliases map[string]string
	out := make(map[string]bindingSpec, len(values))
	for _, raw := range values {
		name, path, explicit := strings.Cut(raw, "=")
		if name == "" {
			return nil, fmt.Errorf("--service %q: service name is empty", raw)
		}
		if explicit {
			if path == "" {
				return nil, fmt.Errorf("--service %q: path is empty", raw)
			}
			out[name] = bindingSpec{Path: path}
			continue
		}
		if aliases == nil {
			aliases = LoadServiceAliases()
		}
		p, ok := aliases[name]
		if !ok {
			return nil, fmt.Errorf("--service %q: no alias defined; use --service %s=<path>", name, name)
		}
		out[name] = bindingSpec{Path: p}
	}
	return out, nil
}

// ensureAgentRunning starts the local host-agent daemon when no live agent
// is recorded. v1 connection-state contract is PID-existence only: read
// ~/.rimsky/agent.pid and send signal 0 to confirm the process is alive. A
// live PID is assumed connected to the proxy (the proxy surfaces
// host_agent_not_connected on dispatch if it isn't, which the operator
// policy retries). When no live agent is recorded, daemonize one inline
// before submitting.
func ensureAgentRunning() error {
	if pid, ok, err := readAgentPID(); err == nil && ok && processAlive(pid) {
		return nil
	}
	if code := runAgentStart(nil); code != 0 {
		return fmt.Errorf("`rimsky agent start` exited with code %d", code)
	}
	return nil
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
