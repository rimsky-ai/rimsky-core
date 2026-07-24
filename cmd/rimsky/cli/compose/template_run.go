// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @decision: rimsky-run-self-hosts-templates
package compose

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/hostagent"
)

func RunTemplateRun(ctx context.Context, args []string) int {
	if err := ctx.Err(); err != nil {
		return 130
	}
	common, rf, code := cli.ParseRunArgs(args)
	if code != 0 {
		return code
	}
	if rf.SelfHost && common.Endpoint != "" {
		fmt.Fprintln(os.Stderr, "rimsky run: --self-host and --endpoint are mutually exclusive")
		return 2
	}
	if !rf.SelfHost {
		endpoint, err := cli.ResolveRunEndpoint(common)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		if endpoint != "" {
			return cli.RunRunRemote(ctx, common, endpoint, rf)
		}
	}
	if rf.TemplateName != "" {
		fmt.Fprintln(os.Stderr, "rimsky run: --template needs a running rimsky (a self-hosted stack starts with an empty template registry); pass a template <file> or configure an endpoint")
		return 2
	}
	if rf.KeepSet && rf.Keep {
		fmt.Fprintln(os.Stderr, "rimsky run: --keep is incompatible with self-host — the stack exits with this process, so nothing survives to keep")
		return 2
	}
	return runTemplateSelfHost(ctx, common, rf)
}

func runTemplateSelfHost(ctx context.Context, common *cli.CommonFlags, rf cli.RunFlags) int {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	bootCtx, cancelBoot := context.WithCancel(ctx)
	defer cancelBoot()
	bootSignalDone := make(chan struct{})
	var releaseOnce sync.Once
	releaseBootSignalWatcher := func() {
		releaseOnce.Do(func() { close(bootSignalDone) })
	}
	defer releaseBootSignalWatcher()
	go watchBootSignal(sigCh, bootSignalDone, cancelBoot, logger)

	spec, err := cli.ReadSpecFile(rf.TemplateFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "rimsky run:", err)
		return 2
	}

	cwd, _ := os.Getwd()
	root, err := DiscoverArtifactRoot(cwd, "")
	if err != nil {
		fmt.Fprintln(os.Stderr, "rimsky run: discover artifact root:", err)
		return 2
	}
	runDir, err := EnsureRunDir(root, FormatRunTimestamp(time.Now()), spec.Name)
	if err != nil {
		fmt.Fprintln(os.Stderr, "rimsky run: ensure run dir:", err)
		return 2
	}
	logger.Info("run dir", "path", runDir)

	services, spawnOverlay, err := spawnServices(bootCtx, rf.Services, logger)
	if err != nil {
		fmt.Fprintln(os.Stderr, "rimsky run: spawn services:", err)
		reapSpawnedFatal(services, logger)
		if bootCtx.Err() != nil {
			return 130
		}
		return 2
	}

	if err := WriteSyntheticRimskyYAML(runDir, nil, spawnOverlay, nil); err != nil {
		fmt.Fprintln(os.Stderr, "rimsky run: write rimsky.yml:", err)
		reapSpawnedFatal(services, logger)
		return 2
	}
	if err := WriteSyntheticSupervisorYAMLWithCallbackPort(runDir, 0); err != nil {
		fmt.Fprintln(os.Stderr, "rimsky run: write supervisor.yml:", err)
		reapSpawnedFatal(services, logger)
		return 2
	}

	controlAPIPort, err := hostagent.FreeLocalPort()
	if err != nil {
		fmt.Fprintln(os.Stderr, "rimsky run: allocate control-api port:", err)
		reapSpawnedFatal(services, logger)
		return 2
	}
	endpoint := fmt.Sprintf("http://127.0.0.1:%d", controlAPIPort)
	restoreEnv := snapshotAndSetEnv(map[string]string{
		"RIMSKY_CONFIG":            filepath.Join(runDir, "rimsky.yml"),
		"RIMSKY_SUPERVISOR_CONFIG": filepath.Join(runDir, "supervisor.yml"),
		"RIMSKY_PROCESS_ROLE":      "unified",
		"RIMSKY_CONTROL_API_HOST":  "127.0.0.1",
		"RIMSKY_CONTROL_API_PORT":  strconv.Itoa(controlAPIPort),
	})
	defer restoreEnv()

	stack, err := startRoleStackWithBindRetry(bootCtx, logger, runDir, &controlAPIPort, &endpoint)
	if err != nil {
		fmt.Fprintln(os.Stderr, "rimsky run: start role stack:", err)
		reapSpawnedFatal(services, logger)
		if bootCtx.Err() != nil {
			return 130
		}
		return 2
	}
	coord := &ShutdownCoordinator{
		Stack:    stack,
		Services: services,
		Logger:   logger,
	}

	if err := WaitForControlAPIReady(bootCtx, stack.Endpoint(), 10*time.Second); err != nil {
		fmt.Fprintln(os.Stderr, "rimsky run: control-api not ready:", err)
		return coord.Drain(context.Background(), bootFailureReason(bootCtx))
	}

	c := cli.NewClient(stack.Endpoint())
	c.SetTimeout(3 * time.Second)

	tpl, err := c.RegisterTemplate(bootCtx, cli.RegisterTemplateRequest{Spec: spec, Tag: rf.Tag})
	if err != nil {
		fmt.Fprintln(os.Stderr, "rimsky run: register template:", err)
		return coord.Drain(context.Background(), bootFailureReason(bootCtx))
	}
	hash := tpl.Hash()
	if _, err := c.DeployTemplate(bootCtx, hash); err != nil {
		fmt.Fprintln(os.Stderr, "rimsky run: deploy template:", err)
		return coord.Drain(context.Background(), bootFailureReason(bootCtx))
	}

	body := cli.CreateInstanceRequest{Template: hash, Params: rf.Params, TargetAgent: cli.ResolveTargetAgent("", "")}
	if rf.Key != "" {
		key := rf.Key
		body.InstanceKey = &key
	}
	inst, err := c.CreateInstance(bootCtx, body)
	if err != nil {
		fmt.Fprintln(os.Stderr, "rimsky run: create instance:", err)
		return coord.Drain(context.Background(), bootFailureReason(bootCtx))
	}
	if common.Format == cli.FormatJSON {
		_ = cli.EmitJSON(os.Stdout, inst)
	} else {
		fmt.Fprintf(os.Stdout, "instance_id=%s\n", inst.UUID())
	}

	// @decision: compose-driver-sends-empty-message-after-create
	hasRoot, err := cli.TemplateHasStructuralRoot(bootCtx, c, hash)
	if err != nil {
		fmt.Fprintln(os.Stderr, "rimsky run: inspect template:", err)
		return coord.Drain(context.Background(), bootFailureReason(bootCtx))
	}
	if hasRoot {
		if _, err := c.CreateInstanceMessage(bootCtx, inst.UUID(), "run-wake-"+inst.UUID(),
			cli.CreateInstanceMessageRequest{}); err != nil {
			fmt.Fprintln(os.Stderr, "rimsky run: emit wake message:", err)
			return coord.Drain(context.Background(), bootFailureReason(bootCtx))
		}
	}

	if err := UpdateLatestSymlink(root, runDir); err != nil {
		logger.Warn("rimsky run: update latest symlink", "err", err.Error())
	}

	printer := newProgressPrinter(os.Stderr, false, false, common.Format == cli.FormatJSON)

	releaseBootSignalWatcher()
	if bootCtx.Err() != nil {
		printer.Summary("rimsky run", reasonString(ReasonSignal), 0)
		printer.Finalize()
		return coord.Drain(context.Background(), ReasonSignal)
	}

	displayKey := rf.Key
	if displayKey == "" {
		displayKey = spec.Name
	}
	reason := waitOneShotToTerminal(bootCtx, oneShotWait{
		client:       c,
		stack:        stack,
		services:     services,
		sigCh:        sigCh,
		printer:      printer,
		instanceIDs:  []string{inst.UUID()},
		keyByID:      map[string]string{inst.UUID(): displayKey},
		project:      spec.Name,
		timeout:      rf.Timeout,
		pollInterval: rf.PollInterval,
		verb:         "rimsky run",
		logger:       logger,
	})

	printer.Summary("rimsky run", reasonString(reason), 0)
	printer.Finalize()
	return coord.Drain(context.Background(), reason)
}
