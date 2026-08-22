// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @story: one-shot-to-terminal
// @story: audit-artifact
// @story: spawned-local-services
// @story: live-progress
// @story: script-friendly-outcome
// @decision: cli-verb
// @decision: timeout-flag
// @decision: progress-flags
// @decision: service-spawn-flag
// @decision: artifact-root-discovery
// @decision: services-source
// @decision: launch-integration
// @decision: network-binding
// @decision: termination
// @decision: exit-codes
// @decision: graceful-shutdown
package compose

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/serverkit"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/hostagent"
)

type composeRunFlags struct {
	manifestPath string
	name         string
	workdir      string
	timeout      time.Duration
	quiet        bool
	verbose      bool
	json         bool
	services     cli.RepeatedFlag
}

// @story: one-shot-to-terminal
// @decision: rimsky-compose-run-scope
// @decision: exposure-no-config
func RunComposeRun(ctx context.Context, args []string) int {
	if err := ctx.Err(); err != nil {
		return 130
	}
	flags, code := parseComposeRunFlags(args)
	if code != 0 {
		return code
	}

	logger := serverkit.NewJSONLogger()

	return runComposeRunCore(ctx, flags, logger)
}

func runComposeRunCore(ctx context.Context, flags *composeRunFlags, logger *slog.Logger) int {
	spawned := &SpawnedRegistry{}
	// @decision: graceful-shutdown
	escalation, stopSignals := NewSignalEscalation(spawned, logger)
	defer stopSignals()
	sigCh := escalation.Signals()
	bootCtx, cancelBoot := context.WithCancel(ctx)
	defer cancelBoot()
	bootSignalDone := make(chan struct{})
	var releaseOnce sync.Once
	releaseBootSignalWatcher := func() {
		releaseOnce.Do(func() { close(bootSignalDone) })
	}
	defer releaseBootSignalWatcher()
	go watchBootSignal(sigCh, bootSignalDone, cancelBoot, escalation, logger)

	m, err := LoadManifest(flags.manifestPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "rimsky compose run:", err)
		return 2
	}
	resolveTemplatePaths(m, flags.manifestPath)

	cwd, _ := os.Getwd()
	root, err := DiscoverArtifactRoot(cwd, flags.workdir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "rimsky compose run: discover artifact root:", err)
		return 2
	}

	// @decision: run-name
	name := flags.name
	if name == "" {
		name = m.Project
	}
	runDir, err := EnsureRunDir(root, FormatRunTimestamp(time.Now()), name)
	if err != nil {
		fmt.Fprintln(os.Stderr, "rimsky compose run: ensure run dir:", err)
		return 2
	}
	logger.Info("run dir", "path", runDir)

	services, spawnOverlay, err := spawnServices(bootCtx, flags.services, logger)
	spawned.Set(services)
	if err != nil {
		fmt.Fprintln(os.Stderr, "rimsky compose run: spawn services:", err)
		reapSpawnedFatal(services, logger)
		if bootCtx.Err() != nil {
			return 130
		}
		return 2
	}

	siblingPath, err := SiblingRimskyYMLPath(flags.manifestPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "rimsky compose run: resolve sibling rimsky.yml:", err)
		reapSpawnedFatal(services, logger)
		return 2
	}
	siblings, err := LoadSiblingBlocks(siblingPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "rimsky compose run: load sibling rimsky.yml:", err)
		reapSpawnedFatal(services, logger)
		return 2
	}
	if err := WriteSyntheticRimskyYAML(runDir, m, spawnOverlay, siblings, 0); err != nil {
		fmt.Fprintln(os.Stderr, "rimsky compose run: write rimsky.yml:", err)
		reapSpawnedFatal(services, logger)
		return 2
	}

	controlAPIPort, err := hostagent.FreeLocalPort()
	if err != nil {
		fmt.Fprintln(os.Stderr, "rimsky compose run: allocate control-api port:", err)
		reapSpawnedFatal(services, logger)
		return 2
	}
	endpoint := fmt.Sprintf("http://127.0.0.1:%d", controlAPIPort)
	restoreEnv := snapshotAndSetEnv(map[string]string{
		"RIMSKY_CONFIG":           filepath.Join(runDir, "rimsky.yml"),
		"RIMSKY_PROCESS_ROLE":     "unified",
		"RIMSKY_CONTROL_API_HOST": "127.0.0.1",
		"RIMSKY_CONTROL_API_PORT": strconv.Itoa(controlAPIPort),
	})
	defer restoreEnv()

	stack, err := startRoleStackWithBindRetry(bootCtx, logger, runDir, &controlAPIPort, &endpoint)
	if err != nil {
		fmt.Fprintln(os.Stderr, "rimsky compose run: start role stack:", err)
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
		fmt.Fprintln(os.Stderr, "rimsky compose run: control-api not ready:", err)
		return coord.Drain(context.Background(), bootFailureReason(bootCtx))
	}

	c := cli.NewClient(stack.Endpoint())
	c.SetTimeout(3 * time.Second)

	state, err := QueryState(bootCtx, c, m.Project)
	if err != nil {
		fmt.Fprintln(os.Stderr, "rimsky compose run: query state:", err)
		return coord.Drain(context.Background(), bootFailureReason(bootCtx))
	}
	plan, err := ComputePlan(bootCtx, c, m, state)
	if err != nil {
		fmt.Fprintln(os.Stderr, "rimsky compose run: compute plan:", err)
		return coord.Drain(context.Background(), bootFailureReason(bootCtx))
	}

	printer := newProgressPrinter(os.Stderr, flags.quiet, flags.verbose, flags.json)

	applyLogger := io.Writer(os.Stderr)
	if flags.json {
		applyLogger = io.Discard
	}
	// @decision: compose-engine-reuse
	created, _, err := ApplyPlan(bootCtx, c, plan, ApplyOpts{Logger: applyLogger})
	if err != nil {
		fmt.Fprintln(os.Stderr, "rimsky compose run: apply:", err)
		return coord.Drain(context.Background(), bootFailureReason(bootCtx))
	}

	if err := WakeCreatedInstances(bootCtx, c, created, logger); err != nil {
		fmt.Fprintln(os.Stderr, "rimsky compose run:", err)
		return coord.Drain(context.Background(), bootFailureReason(bootCtx))
	}

	instanceIDs, keyByID := extractInstanceIDs(created)
	if err := UpdateLatestSymlink(root, runDir); err != nil {
		logger.Warn("compose run: update latest symlink", "err", err.Error())
	}
	if len(instanceIDs) == 0 {
		return coord.Drain(context.Background(), ReasonAllSuccess)
	}

	releaseBootSignalWatcher()
	if bootCtx.Err() != nil {
		printer.Summary("compose run", reasonString(ReasonSignal), len(instanceIDs))
		printer.Finalize()
		return coord.Drain(context.Background(), ReasonSignal)
	}

	reason := waitOneShotToTerminal(bootCtx, oneShotWait{
		client:       c,
		stack:        stack,
		services:     services,
		sigCh:        sigCh,
		escalation:   escalation,
		printer:      printer,
		instanceIDs:  instanceIDs,
		keyByID:      keyByID,
		project:      m.Project,
		timeout:      flags.timeout,
		pollInterval: DefaultWaitPollInterval,
		verb:         "compose run",
		logger:       logger,
	})

	printer.Summary("compose run", reasonString(reason), len(instanceIDs))
	printer.Finalize()

	return coord.Drain(context.Background(), reason)
}

type oneShotWait struct {
	client       instanceClient
	stack        *RoleStack
	services     []*hostagent.SpawnedService
	sigCh        chan os.Signal
	escalation   *SignalEscalation
	printer      ProgressPrinter
	instanceIDs  []string
	keyByID      map[string]string
	project      string
	timeout      time.Duration
	pollInterval time.Duration
	verb         string
	logger       *slog.Logger
}

func waitOneShotToTerminal(bootCtx context.Context, w oneShotWait) ShutdownReason {
	waitCtx, cancelWait := context.WithCancel(bootCtx)
	defer cancelWait()
	waitDone := make(chan struct{})
	var waitOutcomes map[string]string
	var waitErr error
	go func() {
		waitOutcomes, waitErr = WaitForInstancesTerminal(
			waitCtx, w.client, w.instanceIDs, w.project, w.keyByID,
			w.printer, w.pollInterval,
		)
		close(waitDone)
	}()

	armEscalator := w.escalation.Arm

	var timeoutCh <-chan time.Time
	if w.timeout > 0 {
		timer := time.NewTimer(w.timeout)
		defer timer.Stop()
		timeoutCh = timer.C
	}
	select {
	case <-waitDone:
		w.escalation.ArmOnFirstSignal(w.logger, w.verb)
		terminateInstancesForOneShot(bootCtx, w.client, w.instanceIDs, w.verb, w.logger)
		switch {
		case waitErr != nil:
			return classifyWaitErr(waitErr)
		case AnyOutcomeFailed(waitOutcomes):
			return ReasonAnyFailure
		default:
			return ReasonAllSuccess
		}
	case sig := <-w.sigCh:
		w.logger.Info(w.verb+": signal received; draining", "signal", sig.String())
		cancelWait()
		armEscalator()
		waitForOrTimeout(waitDone, waitDrainTimeout, "signal")
		return ReasonSignal
	case <-timeoutCh:
		w.logger.Info(w.verb+": timeout fired; draining", "timeout", w.timeout.String())
		cancelWait()
		armEscalator()
		waitForOrTimeout(waitDone, waitDrainTimeout, "timeout")
		return ReasonTimeout
	case rf := <-w.stack.FailCh():
		w.logger.Error(w.verb+": role runner failed", "role", rf.Role, "err", rf.Err.Error())
		cancelWait()
		armEscalator()
		waitForOrTimeout(waitDone, waitDrainTimeout, "role-failure")
		return ReasonAnyFailure
	}
}

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// @decision: compose-driver-sends-empty-message-after-create
// @story: one-shot-to-terminal
func WakeCreatedInstances(ctx context.Context, c *cli.Client, created []CreatedInstance, logger *slog.Logger) error {
	rootByHash := map[string]bool{}
	for _, ci := range created {
		if ci.ID == "" {
			continue
		}
		hasRoot, known := rootByHash[ci.TemplateHash]
		if !known {
			h, err := cli.TemplateHasStructuralRoot(ctx, c, ci.TemplateHash)
			if err != nil {
				return fmt.Errorf("inspect template: %w", err)
			}
			rootByHash[ci.TemplateHash] = h
			hasRoot = h
		}
		if !hasRoot {
			logger.Warn("compose.rootless_instance_not_woken",
				"instance_key", ci.Key,
				"instance_id", ci.ID,
				"template_hash", ci.TemplateHash,
				"consequence", "the template declares no structural root, so compose run sends this instance no "+
					"wake message. It reaches terminal only when a sensor or another instance drives it.")
			continue
		}
		if _, err := c.CreateInstanceMessage(ctx, ci.ID, "compose-wake-"+ci.Key,
			cli.CreateInstanceMessageRequest{}); err != nil {
			return fmt.Errorf("emit wake message: %w", err)
		}
	}
	return nil
}

func extractInstanceIDs(created []CreatedInstance) ([]string, map[string]string) {
	ids := make([]string, 0, len(created))
	keys := make(map[string]string, len(created))
	for _, ci := range created {
		if ci.ID == "" {
			continue
		}
		ids = append(ids, ci.ID)
		keys[ci.ID] = bareInstanceName(ci.Key)
	}
	return ids, keys
}

func bareInstanceName(prefixedKey string) string {
	idx := strings.IndexByte(prefixedKey, ':')
	if idx < 0 {
		return prefixedKey
	}
	return prefixedKey[idx+1:]
}

func spawnServices(
	ctx context.Context,
	values cli.RepeatedFlag,
	logger *slog.Logger,
) ([]*hostagent.SpawnedService, map[string]ManifestExecutorEntry, error) {
	spawnOverlay := map[string]ManifestExecutorEntry{}
	if len(values) == 0 {
		return nil, spawnOverlay, nil
	}

	spawns := make([]*hostagent.SpawnedService, 0, len(values))
	var aliases map[string]string
	for _, raw := range values {
		if cerr := ctx.Err(); cerr != nil {
			reapSpawnedFatal(spawns, logger)
			return nil, nil, cerr
		}
		name, path, explicit := strings.Cut(raw, "=")
		name = strings.TrimSpace(name)
		path = strings.TrimSpace(path)
		if name == "" {
			reapSpawnedFatal(spawns, logger)
			return nil, nil, fmt.Errorf("--service %q: service name is empty", raw)
		}
		if explicit && path == "" {
			reapSpawnedFatal(spawns, logger)
			return nil, nil, fmt.Errorf("--service %q: path is empty", raw)
		}
		if !explicit {
			// @decision: service-spawn-flag
			if aliases == nil {
				aliases = cli.LoadServiceAliases()
			}
			resolved, ok := aliases[name]
			if !ok {
				reapSpawnedFatal(spawns, logger)
				return nil, nil, fmt.Errorf("--service %q: no alias defined; use --service %s=<path>", name, name)
			}
			path = resolved
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			reapSpawnedFatal(spawns, logger)
			return nil, nil, fmt.Errorf("--service %q: resolve absolute path: %w", raw, err)
		}
		spawned, err := hostagent.SpawnService(ctx, hostagent.SpawnServiceParams{
			BinaryPath:   abs,
			Env:          os.Environ(),
			ReadyTimeout: 30 * time.Second,
		})
		if err != nil {
			reapSpawnedFatal(spawns, logger)
			return nil, nil, fmt.Errorf("spawn %q (%s): %w", name, abs, err)
		}
		spawns = append(spawns, spawned)
		spawnOverlay[name] = ManifestExecutorEntry{
			Transport: "grpc",
			Endpoint:  fmt.Sprintf("127.0.0.1:%d", spawned.Port),
		}
		logger.Info("spawned service",
			"name", name,
			"path", abs,
			"pid", spawned.Cmd.Process.Pid,
			"port", spawned.Port,
		)
	}
	return spawns, spawnOverlay, nil
}

func reapSpawnedFatal(spawns []*hostagent.SpawnedService, logger *slog.Logger) {
	for _, s := range spawns {
		if s == nil || s.Cmd == nil || s.Cmd.Process == nil {
			continue
		}
		_ = s.Cmd.Process.Kill()
		if s.Exited != nil {
			<-s.Exited
		}
		if logger != nil {
			logger.Warn("compose run: reaped spawned child during fatal error path",
				"pid", s.Cmd.Process.Pid,
			)
		}
	}
}

func parseComposeRunFlags(args []string) (*composeRunFlags, int) {
	flags := &composeRunFlags{}
	fs := flag.NewFlagSet("compose run", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		out := fs.Output()
		fmt.Fprintln(out, "usage: rimsky compose run [flags] <manifest>")
		fmt.Fprintln(out, "")
		fmt.Fprintln(out, "Flags:")
		fs.PrintDefaults()
	}
	fs.StringVar(&flags.name, "name", "", "run name; appended to the per-run artifact directory (defaults to manifest project)")
	fs.StringVar(&flags.workdir, "workdir", "", "override the artifact root; suppresses walk-up discovery")
	fs.DurationVar(&flags.timeout, "timeout", 0, "max wall-clock duration; 0 = unbounded")
	fs.BoolVar(&flags.quiet, "quiet", false, "suppress per-instance progress; emit only a final summary")
	fs.BoolVar(&flags.verbose, "verbose", false, "include frame ticks in progress output")
	fs.BoolVar(&flags.json, "json", false, "emit progress as JSON Lines on stderr")
	fs.Var(&flags.services, "service", "late-bound service binding: <name>=<path>. Repeatable.")

	if err := fs.Parse(args); err != nil {
		return nil, 2
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprintln(os.Stderr, "rimsky compose run: exactly one positional <manifest> required")
		fs.Usage()
		return nil, 2
	}
	flags.manifestPath = strings.TrimSpace(rest[0])
	if flags.manifestPath == "" {
		fmt.Fprintln(os.Stderr, "rimsky compose run: manifest path is empty")
		return nil, 2
	}
	if flags.quiet && flags.verbose {
		fmt.Fprintln(os.Stderr, "rimsky compose run: --quiet and --verbose are mutually exclusive")
		return nil, 2
	}
	return flags, 0
}

var envMutex sync.Mutex

// @decision: graceful-shutdown
const waitDrainTimeout = serverkit.CLIChildGrace

func waitForOrTimeout(waitDone <-chan struct{}, d time.Duration, trigger string) {
	select {
	case <-waitDone:
	case <-time.After(d):
		slog.Default().Warn("compose run: wait goroutine did not exit within drain budget; proceeding",
			"trigger", trigger,
			"budget", d.String(),
		)
	}
}

func terminateInstancesForOneShot(ctx context.Context, c instanceClient, instanceIDs []string, verb string, logger *slog.Logger) {
	if c == nil {
		return
	}
	for _, id := range instanceIDs {
		if _, err := c.TerminateInstance(ctx, id, "one_shot_workflow_complete"); err != nil {
			if logger != nil {
				logger.Warn(verb+": terminate instance after wait failed",
					"instance_id", id, "err", err.Error())
			}
		}
	}
}

// @decision: graceful-shutdown
func watchBootSignal(sigCh <-chan os.Signal, done <-chan struct{}, cancel context.CancelFunc, escalation *SignalEscalation, logger *slog.Logger) {
	select {
	case <-done:
		return
	case sig := <-sigCh:
		escalation.Arm()
		if logger != nil {
			logger.Info("compose run: signal during boot; cancelling", "signal", sig.String())
		}
		cancel()
	}
}

func startRoleStackWithBindRetry(ctx context.Context, logger *slog.Logger, runDir string, port *int, endpoint *string) (*RoleStack, error) {
	const maxAttempts = 3
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		stack, err := StartRoleStack(ctx, logger, filepath.Join(runDir, "rimsky.yml"), *endpoint)
		if err == nil {
			return stack, nil
		}
		lastErr = err
		if !isBindInUseErr(err) {
			return nil, err
		}
		if attempt == maxAttempts {
			break
		}
		newPort, perr := hostagent.FreeLocalPort()
		if perr != nil {
			return nil, fmt.Errorf("start role stack: re-pick port after bind failure: %w", perr)
		}
		*port = newPort
		*endpoint = fmt.Sprintf("http://127.0.0.1:%d", newPort)
		_ = os.Setenv("RIMSKY_CONTROL_API_PORT", strconv.Itoa(newPort))
		logger.Warn("compose run: control-api bind hit address-in-use; retrying on fresh port",
			"attempt", attempt,
			"new_port", newPort,
		)
	}
	return nil, lastErr
}

func isBindInUseErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "address already in use") ||
		strings.Contains(msg, "bind: address already in use")
}

func snapshotAndSetEnv(vars map[string]string) func() {
	envMutex.Lock()
	type prev struct {
		val string
		ok  bool
	}
	saved := make(map[string]prev, len(vars))
	for k := range vars {
		v, ok := os.LookupEnv(k)
		saved[k] = prev{val: v, ok: ok}
	}
	for k, v := range vars {
		_ = os.Setenv(k, v)
	}
	var restoreOnce sync.Once
	return func() {
		restoreOnce.Do(func() {
			for k, p := range saved {
				if p.ok {
					_ = os.Setenv(k, p.val)
				} else {
					_ = os.Unsetenv(k)
				}
			}
			envMutex.Unlock()
		})
	}
}
