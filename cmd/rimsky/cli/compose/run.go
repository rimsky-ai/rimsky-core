// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// run.go — `rimsky compose run` one-shot in-process orchestrator
// entry point. Wires the flag parser, manifest loader, artifact-root
// discovery, spawn helpers, synthetic config emission, role-stack
// boot, control-api readiness, manifest apply, terminal-wait loop,
// progress printer, signal handling, and graceful drain into one
// run.
//
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
// @decision: instance-self-termination
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
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/hostagent"
)

// composeRunFlags holds the parsed flag/positional surface for
// `rimsky compose run <manifest>`. Each field maps to one flag the
// downstream launcher consults during boot, configuration emission,
// terminal-wait, and drain. See @decision: cli-verb, timeout-flag,
// progress-flags, service-spawn-flag, artifact-root-discovery.
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

// RunComposeRun implements `rimsky compose run <manifest>`. Drives a
// compose manifest to terminal in a self-hosted runtime stack.
// @story: one-shot-to-terminal
func RunComposeRun(ctx context.Context, args []string) int {
	// @constraint: a ctx already cancelled at entry (parent timeout,
	// parent SIGINT) short-circuits before any side effect — including
	// flag parsing, which would otherwise print usage on -h despite the
	// caller having signaled cancellation. 130 is the conventional
	// SIGINT exit code per @decision: exit-codes.
	if err := ctx.Err(); err != nil {
		return 130
	}
	flags, code := parseComposeRunFlags(args)
	if code != 0 {
		return code
	}

	// @constraint: slog JSON over stderr so the executable proof for
	// STORY-spawned-local-services can parse the `spawned service`
	// log line and capture child PIDs from the structured envelope.
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	return runComposeRunCore(ctx, flags, logger)
}

// runComposeRunCore is the testable body of RunComposeRun without the
// signal-handler installation glue, so the integration test in this
// package can drive the same orchestration sequence without
// SIGINT-ing the test process.
func runComposeRunCore(ctx context.Context, flags *composeRunFlags, logger *slog.Logger) int {
	// @constraint: install the SIGINT/SIGTERM handler BEFORE any side-
	// effecting boot phase. spawnServices, WriteSyntheticRimskyYAML,
	// WriteSyntheticSupervisorYAMLWithCallbackPort, FreeLocalPort,
	// StartRoleStack (which runs migrate + boots 3 role runners),
	// WaitForControlAPIReady, QueryState, ComputePlan, and ApplyPlan
	// all sit between the verb entry and the multiplex wait-loop.
	// Without an early handler a Ctrl-C during any of those phases
	// would either be ignored (parent ctx is context.Background) or
	// leak every already-spawned --service child until the verb
	// arrives at the wait-loop select.
	//
	// The sigCh is buffered to admit a first cooperative signal AND
	// the second-SIGINT escalator's signal without blocking the
	// kernel-side delivery. The handler is installed exactly once
	// and used by both the boot-time cancellation watcher and the
	// wait-loop multiplex select below — the latter takes over once
	// the watcher exits (releaseBootSignalWatcher closes
	// bootSignalDone, and the watcher returns without consuming a
	// signal that lands after that close).
	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	bootCtx, cancelBoot := context.WithCancel(ctx)
	defer cancelBoot()
	bootSignalDone := make(chan struct{})
	// @deliberate: releaseBootSignalWatcher closes bootSignalDone on the
	// first invocation; subsequent calls (the deferred call at function
	// exit when the wait-loop owned the channel) are no-ops.
	var releaseOnce sync.Once
	releaseBootSignalWatcher := func() {
		releaseOnce.Do(func() { close(bootSignalDone) })
	}
	defer releaseBootSignalWatcher()
	go watchBootSignal(sigCh, bootSignalDone, cancelBoot, logger)

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

	// @deliberate: failure here returns before the role stack starts, so
	// there is nothing yet to drain on the role-stack side; the spawn
	// helper itself reaps partial spawns. The bootCtx is threaded so a
	// SIGINT during the spawn loop bails between iterations and reaps
	// the partial set.
	services, spawnOverlay, err := spawnServices(bootCtx, flags.services, logger)
	if err != nil {
		fmt.Fprintln(os.Stderr, "rimsky compose run: spawn services:", err)
		reapSpawnedFatal(services, logger)
		if bootCtx.Err() != nil {
			return 130
		}
		return 2
	}

	// @deliberate: write synthetic rimsky.yml + supervisor.yml under the
	// per-run dir so the in-process role runners can load configuration
	// via the same RIMSKY_CONFIG / RIMSKY_SUPERVISOR_CONFIG seam the
	// deployed binaries use. The publishers + named_locks blocks fold
	// through from a sibling rimsky.yml next to the manifest when one
	// exists — the compose schema doesn't carry these blocks
	// (@decision: services-source), so a manifest that needs them leans
	// on the sibling file. An absent sibling is fine; a malformed one
	// is a startup error.
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
	if err := WriteSyntheticRimskyYAML(runDir, m, spawnOverlay, siblings); err != nil {
		fmt.Fprintln(os.Stderr, "rimsky compose run: write rimsky.yml:", err)
		reapSpawnedFatal(services, logger)
		return 2
	}
	// @constraint: the supervisor's callback HTTP listener binds the
	// port the supervisor.yml names. The baked all-in-one default
	// (9100) collides with any other rimsky process holding the port
	// — most commonly a parallel `compose run` on the same host, or
	// a long-lived dev stack — and the bind failure surfaces as
	// `start supervisor: listen tcp 0.0.0.0:9100: bind: address
	// already in use`. The verb writes port=0 into the per-run
	// supervisor.yml so the kernel picks a free port at bind time.
	// The advertise host stays at 127.0.0.1 because the verb only
	// spawns loopback-local services (`--service <name>=<path>`); a
	// per-run callback port is unobservable to anything outside the
	// process tree.
	if err := WriteSyntheticSupervisorYAMLWithCallbackPort(runDir, 0); err != nil {
		fmt.Fprintln(os.Stderr, "rimsky compose run: write supervisor.yml:", err)
		reapSpawnedFatal(services, logger)
		return 2
	}

	// @deliberate: pre-pick the control-api port via the same FreeLocalPort
	// helper SpawnService uses, then plumb env vars the role runners
	// read at startup. Each runner reads RIMSKY_CONFIG etc. on its own
	// Open path; the launcher pre-loads the config for the up-front
	// Migrate.
	controlAPIPort, err := hostagent.FreeLocalPort()
	if err != nil {
		fmt.Fprintln(os.Stderr, "rimsky compose run: allocate control-api port:", err)
		reapSpawnedFatal(services, logger)
		return 2
	}
	endpoint := fmt.Sprintf("http://127.0.0.1:%d", controlAPIPort)
	// @constraint: snapshot + restore the role-runner env vars so an
	// in-process caller running the verb more than once (e.g., an
	// embedding host process, or two parallel test goroutines) does
	// not see one run's per-run paths and ports leak into the next.
	// The role runners load these on Open; once StartRoleStack returns,
	// the env-var values are no longer load-bearing for the running
	// stack, so restoring on function return is safe.
	restoreEnv := snapshotAndSetEnv(map[string]string{
		"RIMSKY_CONFIG":            filepath.Join(runDir, "rimsky.yml"),
		"RIMSKY_SUPERVISOR_CONFIG": filepath.Join(runDir, "supervisor.yml"),
		"RIMSKY_PROCESS_ROLE":      "unified",
		"RIMSKY_CONTROL_API_HOST":  "127.0.0.1",
		"RIMSKY_CONTROL_API_PORT":  strconv.Itoa(controlAPIPort),
	})
	defer restoreEnv()

	// @deliberate: startRoleStackWithBindRetry retries on a control-api
	// bind-EADDRINUSE (the TOCTOU between FreeLocalPort returning and
	// the role runner actually binding); see its comment for the
	// recovery loop.
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

	// @deliberate: a 10s deadline is generous on a loopback boot but bounds
	// the failure surface so a wedged runner is not silently hung.
	if err := WaitForControlAPIReady(bootCtx, stack.Endpoint(), 10*time.Second); err != nil {
		fmt.Fprintln(os.Stderr, "rimsky compose run: control-api not ready:", err)
		return coord.Drain(context.Background(), ReasonAnyFailure)
	}

	// @deliberate: apply the manifest with TerminateAfterRun=true so
	// every CreateInstance opts the instance into self-termination
	// the terminal-wait loop observes via `terminated_at`.
	c := cli.NewClient(stack.Endpoint())
	c.SetComposeOrigin(true)
	// @constraint: tighten the HTTP timeout from the deployed-stack
	// default (30s) to a loopback-friendly 3s so the terminal-wait
	// poll loop wakes quickly on a wedged control-api — a role-runner
	// crash that leaves the HTTP listener accepting-but-not-responding
	// would otherwise eat up to 30s per poll attempt while the wait
	// loop circles waiting on ctx.Done() to propagate mid-poll.
	c.SetTimeout(3 * time.Second)

	state, err := QueryState(bootCtx, c, m.Project)
	if err != nil {
		fmt.Fprintln(os.Stderr, "rimsky compose run: query state:", err)
		return coord.Drain(context.Background(), ReasonAnyFailure)
	}
	plan, err := ComputePlan(bootCtx, c, m, state)
	if err != nil {
		fmt.Fprintln(os.Stderr, "rimsky compose run: compute plan:", err)
		return coord.Drain(context.Background(), ReasonAnyFailure)
	}

	printer := newProgressPrinter(os.Stderr, flags.quiet, flags.verbose, flags.json)

	// @constraint: in --json mode, the verb's stderr is JSON Lines
	// (TD-progress-flags). ApplyPlan's Logger writes prose step lines
	// ("  create foo:bar ok") which would interleave on the same stream
	// and break a `jq` pipe. Route the apply logger to io.Discard in
	// --json mode; the JSON apply-step events are reconstructable from
	// the eventual NodeRunTerminal/InstanceTerminal records.
	applyLogger := io.Writer(os.Stderr)
	if flags.json {
		applyLogger = io.Discard
	}
	created, err := ApplyPlan(bootCtx, c, plan, ApplyOpts{Logger: applyLogger, TerminateAfterRun: true})
	if err != nil {
		fmt.Fprintln(os.Stderr, "rimsky compose run: apply:", err)
		return coord.Drain(context.Background(), ReasonAnyFailure)
	}

	// @constraint: instance creation is idle post-spec
	// (story:instance-create-is-idle). Emit an empty message to each
	// newly created instance via the universal message-emit path so
	// the structural roots wake and the wait-for-terminal loop has
	// work to observe. The Idempotency-Key is deterministic on the
	// instance key so a manifest re-run does not enqueue a second
	// wake frame.
	//
	// @deliberate: skip the wake when the instance's template has no
	// structural root (every node carries `subscribes:`). An empty-
	// message wake against a rootless template queues a frame nobody
	// consumes; the wait-for-terminal loop would then hang waiting
	// for a terminal that never arrives (especially under
	// `terminate_after_run: true`). The introspection mirrors
	// `Harness.CreateInstance` and `RunRun`: a `GET
	// /v1/templates/{hash}` resolves the spec; absence of any
	// structural root means the empty wake fires nothing.
	// @decision: compose-driver-emits-empty-message-after-create
	// @story: one-shot-to-terminal
	rootByHash := map[string]bool{}
	for _, ci := range created {
		if ci.ID == "" {
			continue
		}
		hasRoot, ok := rootByHash[ci.TemplateHash]
		if !ok {
			h, herr := cli.TemplateHasStructuralRoot(bootCtx, c, ci.TemplateHash)
			if herr != nil {
				fmt.Fprintln(os.Stderr, "rimsky compose run: inspect template:", herr)
				return coord.Drain(context.Background(), ReasonAnyFailure)
			}
			rootByHash[ci.TemplateHash] = h
			hasRoot = h
		}
		if !hasRoot {
			continue
		}
		wakeKey := "compose-wake-" + ci.Key
		if _, err := c.CreateInstanceMessage(bootCtx, ci.ID, wakeKey,
			cli.CreateInstanceMessageRequest{}); err != nil {
			fmt.Fprintln(os.Stderr, "rimsky compose run: emit wake message:", err)
			return coord.Drain(context.Background(), ReasonAnyFailure)
		}
	}

	// @deliberate: terminal-wait runs in a goroutine so the verb's main
	// thread can multiplex over the four ready conditions (signal,
	// timeout, role-failure, all-instances-terminated).
	instanceIDs, keyByID := extractInstanceIDs(created)
	if len(instanceIDs) == 0 {
		// @deliberate: manifest converged but had no instances to wait
		// on (no-changes case) — refresh the latest-symlink for any
		// audit-artifact reader and drain successfully without entering
		// the wait-loop multiplex.
		if err := UpdateLatestSymlink(root, runDir); err != nil {
			logger.Warn("compose run: update latest symlink", "err", err.Error())
		}
		return coord.Drain(context.Background(), ReasonAllSuccess)
	}

	// @deliberate: update latest-symlink as soon as the apply lands so
	// an audit-artifact reader can `cd .rimsky/latest` and follow along
	// during the run rather than only after it terminates.
	if err := UpdateLatestSymlink(root, runDir); err != nil {
		logger.Warn("compose run: update latest symlink", "err", err.Error())
	}

	// @constraint: hand the sigCh from the boot-time goroutine back to
	// the wait-loop / drain multiplex. releaseBootSignalWatcher closes
	// bootSignalDone, which wakes the watcher; if a signal raced the
	// release-close, Go's select may choose either branch, but the
	// post-release bootCtx.Err() check below catches the signal-fired
	// case and routes through the drain path the multiplex select
	// would have taken (cancelWait + waitForOrTimeout + ReasonSignal).
	releaseBootSignalWatcher()
	if bootCtx.Err() != nil {
		// @constraint: a boot-time signal fired and was consumed by the
		// watcher (which already cancelled bootCtx). Skip wait-loop
		// setup entirely; drain with ReasonSignal so the exit code
		// matches what a signal-in-wait-loop would have produced.
		printer.Finalize()
		fmt.Fprintf(os.Stderr, "compose run: %s (%d instance%s)\n",
			reasonString(ReasonSignal), len(instanceIDs), pluralS(len(instanceIDs)))
		return coord.Drain(context.Background(), ReasonSignal)
	}

	waitCtx, cancelWait := context.WithCancel(bootCtx)
	defer cancelWait()
	waitDone := make(chan struct{})
	var waitOutcomes map[string]string
	var waitErr error
	go func() {
		waitOutcomes, waitErr = WaitForInstancesTerminal(
			waitCtx, c, instanceIDs, m.Project, keyByID,
			printer, DefaultWaitPollInterval,
		)
		close(waitDone)
	}()

	// @constraint: escalatorDone is hoisted to the outer scope so the
	// second-SIGINT escape hatch stays armed through the drain that
	// follows the select below — a wedged drain is the case the
	// escalator exists for. The deferred close fires on function
	// exit, after coord.Drain returns.
	escalatorDone := make(chan struct{})
	defer close(escalatorDone)

	// @constraint: armEscalator installs the second-signal escalator
	// after the first non-natural multiplex case fires. Bound to every
	// drain-triggering case (signal, timeout, role-failure) so a wedged
	// drain on ANY trigger has the safety valve armed — binding it to
	// the SIGINT case alone defeats it on the two most likely wedge
	// surfaces (drain after a role-runner crash, drain after --timeout
	// expiry). The escalator MUST be installed AFTER the first signal
	// is consumed by the multiplex select; otherwise the first signal
	// would race the select and be consumed by the escalator goroutine,
	// triggering an immediate hard-exit instead of a cooperative drain.
	armEscalator := func() {
		InstallSecondSignalEscalator(sigCh, escalatorDone, services, logger)
	}

	// @constraint: timeoutCh is nil when --timeout is 0 (unbounded). A
	// select over a nil channel blocks forever, which is exactly the
	// no-timeout semantic we want without a separate branch.
	// @constraint: the timer is scoped to the multiplex select below
	// — stop it immediately after the select resolves so the Timer
	// is released before the drain begins rather than at function
	// exit.
	reason := func() ShutdownReason {
		var timeoutCh <-chan time.Time
		if flags.timeout > 0 {
			timer := time.NewTimer(flags.timeout)
			defer timer.Stop()
			timeoutCh = timer.C
		}
		select {
		case <-waitDone:
			switch {
			case waitErr != nil:
				return classifyWaitErr(waitErr)
			case AnyOutcomeFailed(waitOutcomes):
				return ReasonAnyFailure
			default:
				return ReasonAllSuccess
			}
		case sig := <-sigCh:
			logger.Info("compose run: signal received; draining", "signal", sig.String())
			cancelWait()
			armEscalator()
			waitForOrTimeout(waitDone, waitDrainTimeout, "signal")
			return ReasonSignal
		case <-timeoutCh:
			logger.Info("compose run: timeout fired; draining", "timeout", flags.timeout.String())
			cancelWait()
			armEscalator()
			waitForOrTimeout(waitDone, waitDrainTimeout, "timeout")
			return ReasonTimeout
		case rf := <-stack.FailCh():
			logger.Error("compose run: role runner failed", "role", rf.Role, "err", rf.Err.Error())
			cancelWait()
			armEscalator()
			waitForOrTimeout(waitDone, waitDrainTimeout, "role-failure")
			return ReasonAnyFailure
		}
	}()

	// @deliberate: surface a final aggregate summary line for the
	// operator. The quiet printer suppresses every per-event line but
	// Finalize runs the closing flush; the prose printers also flush
	// here.
	printer.Finalize()
	fmt.Fprintf(os.Stderr, "compose run: %s (%d instance%s)\n",
		reasonString(reason), len(instanceIDs), pluralS(len(instanceIDs)))

	return coord.Drain(context.Background(), reason)
}

// pluralS returns "s" when n != 1 — small helper for human-readable
// summary lines.
func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// extractInstanceIDs walks the ApplyPlan-returned CreatedInstance
// slice and returns (ids, keyByID). The keyByID values are the
// manifest-author-supplied bare names ("ok", "oops"), not the
// project-prefixed form ApplyPlan threads through to the control-api
// ("compose:<project>:ok"). The progress printer surfaces the bare
// names so the operator's per-instance summary lines read in the same
// terms the manifest declares; the prefixed form is internal
// bookkeeping the verb does not need to leak. Empty slice when
// ApplyPlan landed no creates (no-changes apply) — the caller
// short-circuits the wait loop in that case.
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

// bareInstanceName strips the `compose:<project>:` prefix from a
// project-prefixed instance key, returning the manifest-author-supplied
// name. A key without the prefix is returned as-is. The prefix is
// applied uniformly by Manifest.PrefixedInstanceKey on the way in, so
// reversing it on the way out is well-defined.
func bareInstanceName(prefixedKey string) string {
	const prefix = cli.ReservedTagPrefix
	if !strings.HasPrefix(prefixedKey, prefix) {
		return prefixedKey
	}
	rest := prefixedKey[len(prefix):]
	// @constraint: rest is "<project>:<name>" — split once on the first
	// ':' so a project name containing only the alphabet-validator
	// characters cannot accidentally swallow extra segments. A
	// malformed key without a separator returns rest verbatim,
	// mirroring the prefix-only fall-through above.
	idx := strings.IndexByte(rest, ':')
	if idx < 0 {
		return rest
	}
	return rest[idx+1:]
}

// spawnServices parses --service entries, calls hostagent.SpawnService
// for each binary, and returns the resulting endpoints as the spawn
// overlay map the verb passes to WriteSyntheticRimskyYAML. The returned
// map contains ONLY the spawned entries — the manifest's own executors
// are folded in as the base layer by WriteSyntheticRimskyYAML itself,
// where the spawn overlay overrides per-name. Returning a pure overlay
// (rather than a pre-merged map) keeps the priority rule readable at
// the synthesizer's call site. On any spawn error, the helper reaps
// every already-spawned child before returning so a partial spawn
// never leaks a process.
//
// Each successful spawn is logged at info level via slog so the
// executable proof for STORY-spawned-local-services can parse the
// JSON envelope and capture the child PID for its post-run signal-0
// check.
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
	// @constraint: aliases load lazily — a values slice with only
	// explicit <name>=<path> entries never touches the alias files,
	// matching cli.resolveServiceBindings.
	var aliases map[string]string
	for _, raw := range values {
		// @constraint: bail between iterations if the supplied ctx has
		// already been cancelled (e.g., a boot-time SIGINT propagated
		// through bootCtx). Without this check the loop would keep
		// spawning every remaining --service entry and accumulate
		// children in spawns; reapSpawnedFatal would then run only on
		// a per-iteration spawn error. With the check, a cancellation
		// stops the loop and reaps the partial set cleanly.
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
			// @constraint: `--service foo=` is an empty-path explicit
			// form, not a bare-name; always an error regardless of aliases.
			reapSpawnedFatal(spawns, logger)
			return nil, nil, fmt.Errorf("--service %q: path is empty", raw)
		}
		if !explicit {
			// @decision: service-spawn-flag — bare `--service <name>`
			// resolves via the same alias file `rimsky run` uses
			// (cli.LoadServiceAliases: ~/.rimsky/aliases.yml overlaid
			// by .rimsky/aliases.yml). Loaded lazily, once.
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

// reapSpawnedFatal SIGKILLs every spawned child immediately. Used on
// a fatal error before the ShutdownCoordinator is available — the
// coordinator's drain handles the in-flight case. A SIGTERM here
// would buy nothing (the verb is bailing out, not coordinating a
// cooperative shutdown) and would risk leaving a child alive on a
// signal-ignoring binary.
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

// parseComposeRunFlags parses the `compose run` flag set and returns
// the populated flags struct, or (nil, exitCode) on a usage error.
// The usage error path mirrors the convention the rest of the CLI
// uses: exit 2, message on stderr.
func parseComposeRunFlags(args []string) (*composeRunFlags, int) {
	flags := &composeRunFlags{}
	fs := flag.NewFlagSet("compose run", flag.ContinueOnError)
	// @constraint: default Output is set to stderr explicitly so the
	// observable behavior for end users is unchanged; the Usage closure
	// writes through fs.Output() rather than os.Stderr directly so a
	// programmatic caller (test harness, structured-error wrapper) can
	// redirect via fs.SetOutput instead of having to monkeypatch
	// os.Stderr.
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
	fs.BoolVar(&flags.verbose, "verbose", false, "include frame ticks and claim events in progress output")
	fs.BoolVar(&flags.json, "json", false, "emit progress as JSON Lines on stderr")
	fs.Var(&flags.services, "service", "late-bound service binding: <name>=<path>. Repeatable.")

	if err := fs.Parse(args); err != nil {
		// @constraint: Flag.ContinueOnError already printed the usage to fs.Output(); just translate to exit code 2.
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

// envMutex serializes snapshotAndSetEnv across concurrent verb
// invocations within the same process. The role runners read the
// config-path env vars synchronously on startup; in-process callers
// running the verb in parallel goroutines (an embedding host process,
// or two parallel test goroutines) would otherwise interleave the
// snapshot/set/restore steps and one caller could observe the other's
// values mid-boot — including the gate-load-bearing
// env:RIMSKY_PROCESS_ROLE=unified that controls the memory-blob
// backend admission. The mutex held across the full life of the verb
// (acquire in snapshotAndSetEnv, release in the returned restore fn)
// pins the env-mutating region to one verb invocation at a time.
//
// @constraint: holding the mutex for the entire verb invocation
// means parallel `compose run` calls within one process serialize
// end-to-end on the env vars; this is the only correct shape today,
// because the role-runner env-load is the only seam. This is a
// limitation of the current shape — fine for a CLI binary where the
// process is the verb, accepted as a known constraint for any
// in-process embedding (testcontainers harness, plugin host) where
// two concurrent verb invocations will block each other for the
// full per-run duration. The fix is per-runner config injection
// (skipping the env-var seam entirely), tracked as a v1 follow-up.
var envMutex sync.Mutex

// waitDrainTimeout bounds the wait-loop drain after the multiplex
// select fires on a non-natural case (signal, timeout, role-failure).
// The wait goroutine's exit normally races ctx-cancellation through
// the next per-poll attempt; on a wedged control-api the cli.Client's
// SetTimeout bounds each attempt at 3s, but the per-instance pass
// could still drag in pathological cases. The deadline here means a
// stuck wait goroutine cannot stall the drain.
const waitDrainTimeout = 5 * time.Second

// waitForOrTimeout blocks until waitDone closes or timeout elapses.
// Used by the multiplex select's non-natural cases (signal, timeout,
// role-failure) so a wedged wait goroutine cannot stall the drain.
// A timeout fires a logged warning but does not panic — the drain
// proceeds anyway and the role-stack drain reaps anything still
// running.
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

// watchBootSignal blocks on either a signal arrival or the done-channel
// close. On signal arrival the boot context is cancelled (so every
// phase below the verb's entry point observes cancellation) and the
// watcher returns. On done close the watcher returns without consuming
// a signal — the channel stays quiescent for the wait-loop multiplex
// select to re-acquire.
//
// @constraint: only ONE signal is consumed by this goroutine. The
// kernel-side buffered channel (sigCh has width 2) admits the second
// signal during a wedged boot; the wait-loop's second-signal escalator
// is what owns the hard-exit safety valve. The boot-time path does not
// install an escalator because the boot returns through reapSpawned-
// Fatal + role-stack drain anyway — a hard-exit on top of in-flight
// reap would race the spawn loop's partial-cleanup guarantees.
func watchBootSignal(sigCh <-chan os.Signal, done <-chan struct{}, cancel context.CancelFunc, logger *slog.Logger) {
	select {
	case <-done:
		return
	case sig := <-sigCh:
		if logger != nil {
			logger.Info("compose run: signal during boot; cancelling", "signal", sig.String())
		}
		cancel()
	}
}

// startRoleStackWithBindRetry wraps StartRoleStack with a small retry
// loop on the control-api bind-EADDRINUSE failure. FreeLocalPort
// returns an OS-assigned port, but the kernel may hand the same port
// to an unrelated process between the FreeLocalPort call and the
// role-runner bind (the TOCTOU window noted in spawn.go's FreeLocalPort
// comment). The spawn path tolerates the race because the child binds
// the port and a ready-poll surfaces the failure; the role-stack path
// is different — the verb pre-picks the port and threads it via env
// vars, so a bind failure surfaces deep inside StartRoleStack and
// looks like a flake. Retrying with a fresh port matches the existing
// spawn-time tolerance pattern.
//
// On a non-bind-EADDRINUSE failure, returns immediately. The retry
// budget is small (3 attempts) so a persistent failure surfaces fast
// rather than spinning.
//
// On retry: rewrites the env vars to point at the new port (via
// snapshotAndSetEnv's outer scope is not reachable here; the function
// callers re-set RIMSKY_CONTROL_API_PORT in-place via os.Setenv —
// envMutex is already held by snapshotAndSetEnv across the verb's
// lifetime so this writes under the same lock).
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
		// @constraint: re-pick the control-api port and re-publish
		// RIMSKY_CONTROL_API_PORT so the in-process control-api runner
		// reads the new value on its next start. The env var is mutated
		// under the same envMutex held by snapshotAndSetEnv, so no other
		// verb invocation can race here.
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

// isBindInUseErr reports whether err is a bind failure caused by
// EADDRINUSE. The role-stack startup wraps the underlying net.Listen
// error multiple times, so the check walks the wrapped chain. We use
// a string match because the underlying syscall error type varies
// across Linux/Darwin and a typed assertion would miss either.
func isBindInUseErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "address already in use") ||
		strings.Contains(msg, "bind: address already in use")
}

// snapshotAndSetEnv records the current values (and present/absent
// state) of every named env var, calls os.Setenv with the supplied
// value for each, and returns a function that restores the pre-call
// values when invoked. Used by RunComposeRun to plumb the role-runner
// config-path env vars without leaking the per-run paths into the
// process environment after the verb returns — important for any
// in-process caller (embedding host, test harness) that runs the
// verb more than once in the same process. The package-level envMutex
// makes the snapshot+set+restore region exclusive across goroutines.
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
