// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/serverkit"
)

type RepeatedFlag []string

func (r *RepeatedFlag) String() string { return strings.Join(*r, ",") }
func (r *RepeatedFlag) Set(v string) error {
	*r = append(*r, v)
	return nil
}

func RunRegister(ctx context.Context, args []string) int {
	return runTemplateRegisterNamed(ctx, "register", args)
}

func RunDeploy(ctx context.Context, args []string) int {
	return runTemplateDeployNamed(ctx, "deploy", args)
}

func RunUndeploy(ctx context.Context, args []string) int {
	return runTemplateUndeployNamed(ctx, "undeploy", args)
}

func RunInstantiate(ctx context.Context, args []string) int {
	return runInstanceCreateNamed(ctx, "instantiate", args)
}

func RunRmInstance(ctx context.Context, args []string) int {
	return runInstanceDeleteNamed(ctx, "rm-instance", args)
}

func RunLs(ctx context.Context, args []string) int {
	if len(args) == 0 {
		return runInstanceListNamed(ctx, "ls", nil)
	}
	switch args[0] {
	case "templates":
		return runTemplateListNamed(ctx, "ls templates", args[1:])
	case "instances":
		return runInstanceListNamed(ctx, "ls instances", args[1:])
	case "tags":
		return runTagListNamed(ctx, "ls tags", args[1:])
	case "help", "--help", "-h":
		fmt.Fprintln(os.Stdout, UsageLine("ls", "[templates|instances|tags] [flags]"))
		return 0
	}
	if strings.HasPrefix(args[0], "-") {
		return runInstanceListNamed(ctx, "ls", args)
	}
	fmt.Fprintf(os.Stderr, "rimsky ls: unknown subcommand %q (want templates|instances|tags, or a flag for the default instance listing)\n", args[0])
	return 2
}

func RunLogs(ctx context.Context, args []string) int {
	return runInstanceEventsNamed(ctx, "logs", append([]string{"--follow"}, args...))
}

// @decision: rimsky-run-self-hosts-templates
type RunFlags struct {
	Params       map[string]any
	TemplateName string
	TemplateFile string
	Key          string
	Tag          string
	Keep         bool
	KeepSet      bool
	PollInterval time.Duration
	Timeout      time.Duration
	Services     RepeatedFlag
	SelfHost     bool
}

func ParseRunArgs(args []string) (*CommonFlags, RunFlags, int) {
	var (
		params  string
		keep    bool
		noKeep  bool
		rf      RunFlags
		paramKV RepeatedFlag
	)
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	SetUsage(fs, UsageLine("run", "{<file>|--template <name>} [flags]"))
	var common CommonFlags
	RegisterCommonFlags(fs, &common)
	fs.StringVar(&params, "params", "", "JSON object or @file path")
	fs.StringVar(&rf.TemplateName, "template", "", "name of an already-registered template (mutually exclusive with <file>)")
	fs.StringVar(&rf.Key, "instance-key", "", "instance_key")
	fs.StringVar(&rf.Tag, "tag", "", "tag to attach to the registered template")
	fs.BoolVar(&keep, "keep", true, "leave the instance and template after creation (default; remote endpoint only)")
	fs.BoolVar(&noKeep, "no-keep", false, "delete instance and template after terminal state")
	fs.DurationVar(&rf.PollInterval, "poll-interval", time.Second, "poll interval when --no-keep")
	fs.DurationVar(&rf.Timeout, "timeout", 0, "max wait for terminal state (0 = unbounded)")
	fs.Var(&paramKV, "param", "k=v param (repeatable); merged over --params (later wins)")
	fs.Var(&rf.Services, "service", "late-bound service binding: <name>=<path> or bare <name> (alias). "+
		"Remote: auto-starts the local daemon if its ~/.rimsky/daemon.pid is not live. Self-host: spawned directly on loopback ports")
	fs.BoolVar(&rf.SelfHost, "self-host", false,
		"boot an in-process all-in-one stack for this run even when a context endpoint is configured (incompatible with --endpoint)")
	if code, done := ParseVerbFlags(fs, args); done {
		return nil, RunFlags{}, code
	}
	if err := common.ResolveFormat("run", NoTable); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return nil, RunFlags{}, 2
	}
	SetActiveCommonFlags(&common)
	rest := fs.Args()

	if rf.TemplateName != "" && len(rest) > 0 {
		fmt.Fprintln(os.Stderr, "rimsky run: --template and a positional <file> are mutually exclusive")
		return nil, RunFlags{}, 2
	}
	if rf.TemplateName == "" && len(rest) != 1 {
		return nil, RunFlags{}, UsageError(fs)
	}
	if len(rest) == 1 {
		rf.TemplateFile = rest[0]
	}
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "keep" || f.Name == "no-keep" {
			rf.KeepSet = true
		}
	})
	rf.Keep = keep
	if noKeep || !keep {
		rf.Keep = false
	}

	pp, err := mergeParams(params, paramKV)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return nil, RunFlags{}, 2
	}
	rf.Params = pp

	return &common, rf, 0
}

func ResolveRunEndpoint(common *CommonFlags) (string, error) {
	cfgPath, _ := DefaultConfigPath()
	endpoint, err := ResolveEndpoint(common.Endpoint, os.Getenv("RIMSKY_CONTROL_API_URL"), cfgPath, "")
	if errors.Is(err, ErrNoEndpointConfigured) {
		return "", nil
	}
	return endpoint, err
}

func RunRunRemote(ctx context.Context, common *CommonFlags, endpoint string, rf RunFlags) int {
	bindings, err := resolveServiceBindings(rf.Services)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}

	c := NewClient(endpoint)
	apiKey := common.ResolveAPIKey(os.Getenv("RIMSKY_API_KEY"))
	c.SetAPIKey(apiKey)

	var hash string
	if rf.TemplateName != "" {
		tpl, rerr := c.GetTemplate(ctx, rf.TemplateName)
		if rerr != nil {
			return reportError(rerr)
		}
		hash = tpl.Hash()
	} else {
		spec, rerr := ReadSpecFile(rf.TemplateFile)
		if rerr != nil {
			fmt.Fprintln(os.Stderr, rerr)
			return 2
		}
		tpl, terr := c.RegisterTemplate(ctx, RegisterTemplateRequest{Spec: spec, Tag: rf.Tag})
		if terr != nil {
			return reportError(terr)
		}
		hash = tpl.Hash()
	}
	if _, err := c.DeployTemplate(ctx, hash); err != nil {
		return reportError(err)
	}

	// @decision: compose-driver-sends-empty-message-after-create
	// @story: one-shot-to-terminal
	hasRoot, rerr := TemplateHasStructuralRoot(ctx, c, hash)
	if rerr != nil {
		return reportError(rerr)
	}
	if !hasRoot && !rf.Keep {
		fmt.Fprintln(os.Stderr, "rimsky run:", NoStructuralRootError(hash))
		return 2
	}

	if len(bindings) > 0 {
		if startErr := ensureDaemonRunning(); startErr != nil {
			fmt.Fprintf(os.Stderr, "rimsky run: could not start host-daemon: %v\n", startErr)
			return 1
		}
	}

	body := CreateInstanceRequest{Template: hash, Params: rf.Params}
	body.TargetDaemon = ResolveTargetDaemon("", apiKey)
	if rf.Key != "" {
		key := rf.Key
		body.InstanceKey = &key
	}
	if len(bindings) > 0 {
		body.ServiceBindings = bindings
	}
	inst, err := c.CreateInstance(ctx, body)
	if err != nil {
		return reportError(err)
	}

	Render(common.Format, inst, func() {
		fmt.Fprintf(os.Stdout, "instance_id=%s\n", inst.UUID())
	})

	// @decision: compose-driver-sends-empty-message-after-create
	if hasRoot {
		if _, werr := c.CreateInstanceMessage(ctx, inst.UUID(), "run-wake-"+inst.UUID(),
			CreateInstanceMessageRequest{}); werr != nil {
			return reportError(werr)
		}
	}

	if rf.Keep {
		return 0
	}
	return waitAndCleanup(ctx, c, inst.UUID(), hash, rf.PollInterval, rf.Timeout)
}

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

func ensureDaemonRunning() error {
	if pid, ok, err := readDaemonPID(); err == nil && ok && processAlive(pid) {
		return nil
	}
	if code := runDaemonStart(nil); code != 0 {
		return fmt.Errorf("`rimsky daemon start` exited with code %d", code)
	}
	return nil
}

// @decision: exit-codes
// @story: script-friendly-outcome
func waitAndCleanup(ctx context.Context, c *Client, instanceID, hash string, pollInterval, timeout time.Duration) int {
	// @decision: graceful-shutdown
	signalCtx, stopSignals := serverkit.ShutdownContext(ctx, slog.Default())
	defer stopSignals()
	deadline := time.Time{}
	if timeout > 0 {
		deadline = time.Now().Add(timeout)
	}
	// @decision: termination
	// @story: one-shot-to-terminal
	for {
		quiescent, _, err := InstanceQuiescence(signalCtx, c, instanceID)
		if err != nil {
			if signalCtx.Err() != nil {
				return ExitInterrupt
			}
			return reportError(err)
		}
		if quiescent {
			break
		}
		if !deadline.IsZero() && time.Now().After(deadline) {
			fmt.Fprintln(os.Stderr, "timeout waiting for terminal state")
			return ExitTimeout
		}
		select {
		case <-signalCtx.Done():
			return ExitInterrupt
		case <-time.After(pollInterval):
		}
	}
	if _, err := c.TerminateInstance(signalCtx, instanceID, "one_shot_workflow_complete"); err != nil {
		if signalCtx.Err() != nil {
			return ExitInterrupt
		}
		return reportError(err)
	}
	outcome := ExitAllSuccess
	// @decision: exit-codes
	if nodes, err := PagedListInstanceNodes(signalCtx, c, instanceID, ListNodesQuery{}); err != nil {
		if signalCtx.Err() != nil {
			return ExitInterrupt
		}
		return reportError(err)
	} else if o, _ := ClassifyInstanceOutcome(nodes); o != OutcomeSuccess {
		outcome = ExitAnyFailure
	}
	if err := c.DeleteInstance(signalCtx, instanceID); err != nil {
		if signalCtx.Err() != nil {
			return ExitInterrupt
		}
		return reportError(err)
	}
	if _, err := c.UndeployTemplate(signalCtx, hash); err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.Status == 409 {
			fmt.Fprintf(os.Stderr, "warn: undeploy %s skipped (still referenced)\n", hash)
		} else if signalCtx.Err() != nil {
			return ExitInterrupt
		} else {
			return reportError(err)
		}
	}
	if err := c.DeleteTemplate(signalCtx, hash); err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.Status == 409 {
			fmt.Fprintf(os.Stderr, "warn: delete %s skipped (still referenced)\n", hash)
		} else if signalCtx.Err() != nil {
			return ExitInterrupt
		} else {
			return reportError(err)
		}
	}
	fmt.Fprintln(os.Stdout, "cleanup complete")
	return outcome
}
