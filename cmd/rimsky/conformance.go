// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/sillyname"
	claimproducerproto "github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/conformance/claimproducer"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/conformance/dataprocessing"
	conformance "github.com/rimsky-ai/rimsky-core/lib/protocols/conformance/executor"
	_ "github.com/rimsky-ai/rimsky-core/lib/protocols/conformance/executor/scenarios"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/conformance/hostdaemon"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/conformance/lifecyclesubscriber"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/conformance/publisher"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/conformance/validation"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/grpcdial"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	service "github.com/rimsky-ai/rimsky-core/lib/runtime/service"
)

func reportConformanceResults[T any](prefix string, results []T, name func(T) string, errOf func(T) error) int {
	failed := 0
	for _, r := range results {
		if err := errOf(r); err != nil {
			failed++
			fmt.Printf("FAIL  %s: %v\n", name(r), err)
			continue
		}
		fmt.Printf("ok    %s\n", name(r))
	}
	if failed > 0 {
		fmt.Fprintf(os.Stderr, "%s: %d/%d checks failed\n", prefix, failed, len(results))
		return 1
	}
	return 0
}

// @decision: conformance-suite-per-protocol
func dispatchConformance(args []string) int {
	if len(args) < 1 {
		printConformanceUsage(os.Stderr)
		return 2
	}
	rest := args[1:]
	switch args[0] {
	case "executor":
		return runConformanceExecutor(rest)
	case "claim-producer":
		return runConformanceClaimProducer(rest)
	case "publisher":
		return runConformancePublisher(rest)
	case "validation":
		return runConformanceValidation(rest)
	case "data-processing":
		return runConformanceDataProcessing(rest)
	case "lifecycle-subscriber":
		return runConformanceLifecycleSubscriber(rest)
	case "host-daemon":
		return runConformanceHostDaemon(rest)
	case "probe":
		return runConformanceProbe(rest)
	case "help", "--help", "-h":
		printConformanceUsage(os.Stdout)
		return 0
	}
	fmt.Fprintf(os.Stderr, "rimsky conformance: unknown subcommand %q\n", args[0])
	return 2
}

func printConformanceUsage(w *os.File) {
	fmt.Fprintln(w, "usage: rimsky conformance <executor|claim-producer|publisher|validation|data-processing|lifecycle-subscriber|host-daemon|probe> ...")
}

// @decision: conformance-suite-per-protocol
func runConformanceLifecycleSubscriber(args []string) int {
	fs := flag.NewFlagSet("rimsky conformance lifecycle-subscriber", flag.ContinueOnError)
	cli.SetUsage(fs, cli.UsageLine("conformance lifecycle-subscriber", "--endpoint <grpc://host:port> [flags]"))
	endpoint := fs.String("endpoint", "", "lifecycle-subscriber gRPC endpoint (e.g. grpc://localhost:9097)")
	transport := fs.String("transport", "grpc", "transport: grpc")
	timeout := fs.Duration("timeout", 30*time.Second, "suite timeout")
	tlsMode := fs.String("tls", "off", "off|required — dial the endpoint with verified TLS against system roots")
	if code, done := cli.ParseVerbFlags(fs, args); done {
		return code
	}
	if *endpoint == "" {
		fmt.Fprintln(os.Stderr, "rimsky conformance lifecycle-subscriber: --endpoint required")
		return 2
	}
	if *transport != "grpc" {
		fmt.Fprintf(os.Stderr, "rimsky conformance lifecycle-subscriber: --transport %q not supported; use grpc\n", *transport)
		return 2
	}
	if *tlsMode != "off" && *tlsMode != "required" {
		fmt.Fprintf(os.Stderr, "rimsky conformance lifecycle-subscriber: --tls must be off or required, got %q\n", *tlsMode)
		return 2
	}

	ctx := context.Background()
	if err := lifecyclesubscriber.RunLifecycleCheck(ctx, *endpoint, *tlsMode, *timeout); err != nil {
		fmt.Fprintf(os.Stderr, "lifecycle-subscriber: %v\n", err)
		return 1
	}
	fmt.Fprintln(os.Stdout, "lifecycle-subscriber: ok")
	return 0
}

// @decision: conformance-suite-per-protocol
// @concept: host-daemon-proxy
func runConformanceHostDaemon(args []string) int {
	fs := flag.NewFlagSet("rimsky conformance host-daemon", flag.ContinueOnError)
	cli.SetUsage(fs, cli.UsageLine("conformance host-daemon", "--endpoint <grpc://host:port> [flags]"))
	endpoint := fs.String("endpoint", "", "host-daemon-proxy daemon-facing gRPC endpoint (e.g. grpc://localhost:8090)")
	transport := fs.String("transport", "grpc", "transport: grpc")
	timeout := fs.Duration("timeout", 30*time.Second, "per-suite timeout")
	tlsMode := fs.String("tls", "off", "off|required — dial the endpoint with verified TLS against system roots")
	daemonLabel := fs.String("daemon-label", "rimsky-conformance", "daemon_label the suite registers under")
	routingLabel := fs.String("routing-label", "", "routing_label the suite asks for when it registers anonymously (default: let the server assign one)")
	callbackBaseURL := fs.String("callback-base-url", "http://127.0.0.1:0", "local_callback_base_url the suite registers")
	var apiKeyFlag string
	cli.RegisterAPIKeyFlag(fs, &apiKeyFlag)
	if code, done := cli.ParseVerbFlags(fs, args); done {
		return code
	}

	if *endpoint == "" {
		fmt.Fprintln(os.Stderr, "rimsky conformance host-daemon: --endpoint required")
		return 2
	}
	if *transport != "grpc" {
		fmt.Fprintf(os.Stderr, "rimsky conformance host-daemon: --transport %q not supported; use grpc\n", *transport)
		return 2
	}
	if *tlsMode != "off" && *tlsMode != "required" {
		fmt.Fprintf(os.Stderr, "rimsky conformance host-daemon: --tls must be off or required, got %q\n", *tlsMode)
		return 2
	}

	conn, err := grpc.NewClient(grpcdial.Target(*endpoint), grpc.WithTransportCredentials(grpcdial.TransportCredentials(*tlsMode)))
	if err != nil {
		fmt.Fprintf(os.Stderr, "rimsky conformance host-daemon: dial: %v\n", err)
		return 1
	}
	defer conn.Close()

	apiKey := cli.ResolveAPIKey(apiKeyFlag, os.Getenv("RIMSKY_API_KEY"))
	if apiKey == "" {
		apiKey = sillyname.AnonymousCredentialSentinel
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	results := hostdaemon.Run(ctx, genv1.NewHostDaemonClient(conn), hostdaemon.RunOpts{
		Registration: hostdaemon.Registration{
			APIKey:          apiKey,
			DaemonLabel:     *daemonLabel,
			DaemonVersion:   resolvedVersion(),
			RoutingLabel:    *routingLabel,
			CallbackBaseURL: *callbackBaseURL,
		},
	})
	return reportConformanceResults("rimsky conformance host-daemon", results,
		func(r hostdaemon.CheckResult) string { return r.Name },
		func(r hostdaemon.CheckResult) error { return r.Err })
}

func runConformanceExecutor(args []string) int {
	fs := flag.NewFlagSet("rimsky conformance executor", flag.ContinueOnError)
	cli.SetUsage(fs, cli.UsageLine("conformance executor", "--endpoint <grpc://host:port> [flags]"))
	endpoint := fs.String("endpoint", "", "endpoint URL (executor or lifecycle service)")
	transport := fs.String("transport", "grpc", "transport: grpc")
	allowLive := fs.Bool("allow-live", false, "run against a live (non-stub) endpoint; stub-requiring scenarios skip instead of the run refusing to start")
	only := fs.String("scenarios", "", "comma-list of scenario names to run (default: all)")
	skip := fs.String("skip", "", "comma-list of scenario names to skip")
	timeout := fs.Duration("timeout", 30*time.Second, "per-scenario timeout")
	checkObs := fs.Bool("check-observability", false, "additionally probe ExecutorObservability per spec §6")
	retentionWindow := fs.Duration("retention-test", 0, "if >0, drive a canned dispatch then sleep this long and verify GetTrace returns evicted=true (spec §6 retention check)")
	callbackBind := fs.String("callback-bind", "127.0.0.1", "interface for the conformance callback receiver to bind (use 0.0.0.0 when the executor runs in a container)")
	callbackHost := fs.String("callback-host", "", "host the executor should reach the callback receiver at (default: same as --callback-bind; for containerized executors set to host.docker.internal or a routable host IP)")
	tlsMode := fs.String("tls", "off", "off|required — dial the executor with verified TLS against system roots (gRPC transport only; applies to the scenario suite and the observability probe alike)")
	if code, done := cli.ParseVerbFlags(fs, args); done {
		return code
	}
	if *tlsMode != "off" && *tlsMode != "required" {
		fmt.Fprintf(os.Stderr, "rimsky conformance executor: --tls must be off or required, got %q\n", *tlsMode)
		return 2
	}

	if *endpoint == "" {
		fmt.Fprintln(os.Stderr, "rimsky conformance executor: --endpoint required")
		return 2
	}
	if *transport != "grpc" {
		fmt.Fprintf(os.Stderr, "rimsky conformance executor: --transport %q not supported; use grpc\n", *transport)
		return 2
	}

	ctx := context.Background()

	ep := conformance.Endpoint{Transport: *transport, URL: *endpoint, TLS: *tlsMode}

	onlyList := splitConformanceCSV(*only)
	skipList := splitConformanceCSV(*skip)

	results, err := conformance.Run(ctx, conformance.RunnerOpts{
		Endpoint:     ep,
		AllowLive:    *allowLive,
		Only:         onlyList,
		Skip:         skipList,
		Timeout:      *timeout,
		CallbackBind: *callbackBind,
		CallbackHost: *callbackHost,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "conformance: %v\n", err)
		return 1
	}

	conformance.Summary(results, os.Stdout)
	passed, skipped, stubGateSkips := 0, 0, 0
	for _, r := range results {
		if !r.Passed && !r.Skipped {
			return 1
		}
		if r.Passed {
			passed++
		}
		if r.Skipped {
			skipped++
			if r.StubGateSkip {
				stubGateSkips++
			}
		}
	}

	if *checkObs {
		err := conformance.RunObservabilityCheck(ctx, conformance.ObservabilityCheckOpts{
			Endpoint:      ep,
			RetentionTest: *retentionWindow,
		}, func(format string, args ...any) { fmt.Printf(format, args...) })
		if err != nil {
			fmt.Fprintf(os.Stderr, "observability: %v\n", err)
			return 1
		}
		fmt.Fprintln(os.Stdout, "observability: ok")
	}

	if passed == 0 && len(results) > 0 {
		if *allowLive && stubGateSkips > 0 && stubGateSkips == skipped {
			fmt.Fprintln(os.Stdout, "rimsky conformance executor: 0 scenarios ran — all skipped by the stub gate under --allow-live; the endpoint answered the probes but scenario coverage was not verified")
			return 0
		}
		fmt.Fprintln(os.Stderr, "rimsky conformance executor: 0 scenarios passed (all skipped); executor coverage was not verified")
		return 1
	}
	return 0
}

func splitConformanceCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func runConformanceClaimProducer(args []string) int {
	fs := flag.NewFlagSet("rimsky conformance claim-producer", flag.ContinueOnError)
	cli.SetUsage(fs, cli.UsageLine("conformance claim-producer", "--endpoint <endpoint> [flags]"))
	endpoint := fs.String("endpoint", "", "claim-producer-service endpoint (e.g. grpc://localhost:9101, or a bare http(s) base URL for --transport http)")
	transport := fs.String("transport", "grpc", "grpc|http")
	timeout := fs.Duration("timeout", 10*time.Second, "per-check timeout")
	checkObs := fs.Bool("check-observability", false, "additionally probe ClaimProducerObservability (gRPC only)")
	retentionWindow := fs.Duration("retention-test", 0, "if >0, drive a canned claim then sleep this long and verify GetClaim returns evicted")
	if code, done := cli.ParseVerbFlags(fs, args); done {
		return code
	}

	if *endpoint == "" {
		fmt.Fprintln(os.Stderr, "rimsky conformance claim-producer: --endpoint required")
		return 2
	}
	if *transport != "grpc" && *transport != "http" {
		fmt.Fprintf(os.Stderr, "rimsky conformance claim-producer: --transport must be grpc or http, got %q\n", *transport)
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	var target claimproducerproto.ClaimProducer
	if *transport == "http" {
		target = claimproducer.NewHTTPBridgeClaimProducer(*endpoint)
	} else {
		client, err := service.Dial(ctx, "conformance-target", *endpoint, service.TLSModeOff)
		if err != nil {
			fmt.Fprintf(os.Stderr, "rimsky conformance claim-producer: dial: %v\n", err)
			return 1
		}
		defer client.Close()
		target = client
	}

	results := claimproducer.Run(ctx, target)
	if code := reportConformanceResults("rimsky conformance claim-producer", results,
		func(r claimproducer.CheckResult) string { return r.Name },
		func(r claimproducer.CheckResult) error { return r.Err }); code != 0 {
		return code
	}

	if *checkObs {
		if *transport != "grpc" {
			fmt.Fprintf(os.Stderr, "rimsky conformance claim-producer: --check-observability requires --transport grpc (ClaimProducerObservability has no HTTP+JSON bridge), got %q\n", *transport)
			return 2
		}
		if err := claimproducer.RunObservabilityCheck(ctx, claimproducer.ObservabilityCheckOpts{
			Endpoint:      *endpoint,
			RetentionTest: *retentionWindow,
		}, func(format string, args ...any) { fmt.Printf(format, args...) }); err != nil {
			fmt.Fprintf(os.Stderr, "observability: %v\n", err)
			return 1
		}
		fmt.Fprintln(os.Stdout, "observability: ok")
	}
	return 0
}

const defaultConformancePublisherMessageType = "system/conformance"

func runConformancePublisher(args []string) int {
	fs := flag.NewFlagSet("rimsky conformance publisher", flag.ContinueOnError)
	cli.SetUsage(fs, cli.UsageLine("conformance publisher", "--endpoint <grpc://host:port> --kind <kind> [flags]"))
	endpoint := fs.String("endpoint", "", "publisher gRPC endpoint (e.g. grpc://localhost:9202)")
	transport := fs.String("transport", "grpc", "transport: grpc")
	kind := fs.String("kind", "", "publisher kind to exercise (e.g. cron, http, object-store, webhook)")
	resolvedConfig := fs.String("resolved-config", "", "JSON resolved_config to drive Subscribe (kind-specific)")
	timeout := fs.Duration("timeout", 30*time.Second, "per-suite timeout")
	instanceID := fs.String("instance-id", "", "instance_id passed to Subscribe; required when publisher pushes to /instances/{id}/messages")
	messageType := fs.String("message-type", "", "message type passed to Subscribe and watched for on the control API (default: "+defaultConformancePublisherMessageType+")")
	controlAPI := fs.String("control-api", "", "control-API base URL to poll for the publisher's pushed message (e.g. http://localhost:8080); enables the MessagePush check together with --instance-id (overrides $RIMSKY_CONTROL_API_URL)")
	var apiKeyFlag string
	cli.RegisterAPIKeyFlag(fs, &apiKeyFlag)
	if code, done := cli.ParseVerbFlags(fs, args); done {
		return code
	}

	if *endpoint == "" {
		fmt.Fprintln(os.Stderr, "rimsky conformance publisher: --endpoint required")
		return 2
	}
	if *kind == "" {
		fmt.Fprintln(os.Stderr, "rimsky conformance publisher: --kind required")
		return 2
	}
	if *transport != "grpc" {
		fmt.Fprintf(os.Stderr, "rimsky conformance publisher: --transport %q not supported; use grpc\n", *transport)
		return 2
	}

	target := strings.TrimPrefix(*endpoint, "grpc://")
	conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fmt.Fprintf(os.Stderr, "rimsky conformance publisher: dial: %v\n", err)
		return 1
	}
	defer conn.Close()
	client := genv1.NewPublisherClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	effectiveMessageType := *messageType
	if effectiveMessageType == "" {
		effectiveMessageType = defaultConformancePublisherMessageType
	}

	opts := publisher.RunOpts{
		Kind:           *kind,
		ResolvedConfig: []byte(*resolvedConfig),
		InstanceID:     *instanceID,
		MessageType:    effectiveMessageType,
	}

	controlAPIURL := *controlAPI
	if controlAPIURL == "" {
		controlAPIURL = os.Getenv("RIMSKY_CONTROL_API_URL")
	}
	if *instanceID != "" && controlAPIURL != "" {
		receiver := publisher.NewMessageReceiver()
		opts.MessageReceiver = receiver
		apiKey := cli.ResolveAPIKey(apiKeyFlag, os.Getenv("RIMSKY_API_KEY"))
		go pollForConformancePublisherMessage(ctx, controlAPIURL, apiKey, *instanceID, effectiveMessageType, receiver)
	}

	results := publisher.Run(ctx, client, opts)
	return reportConformanceResults("rimsky conformance publisher", results,
		func(r publisher.CheckResult) string { return r.Name },
		func(r publisher.CheckResult) error { return r.Err })
}

// @concept: rimsky
func pollForConformancePublisherMessage(ctx context.Context, controlAPIURL, apiKey, instanceID, messageType string, receiver *publisher.MessageReceiver) {
	c := cli.NewClient(controlAPIURL)
	c.SetAPIKey(apiKey)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		resp, err := c.ListInstanceMessages(ctx, instanceID, cli.ListMessagesQuery{Type: messageType})
		if err != nil || resp == nil {
			continue
		}
		if len(resp.Messages) > 0 {
			receiver.NoteMessage(instanceID)
			return
		}
	}
}

func runConformanceValidation(args []string) int {
	fs := flag.NewFlagSet("rimsky conformance validation", flag.ContinueOnError)
	cli.SetUsage(fs, cli.UsageLine("conformance validation", "--endpoint <grpc://host:port> [flags]"))
	endpoint := fs.String("endpoint", "", "validation-advertising service gRPC endpoint (e.g. grpc://localhost:9095)")
	transport := fs.String("transport", "grpc", "transport: grpc (the http+json bridge is not implemented for Validation)")
	role := fs.String("role", "executor", "role to validate against: executor | claim_producer | lifecycle_subscriber | publisher")
	timeout := fs.Duration("timeout", 30*time.Second, "per-suite timeout")
	if code, done := cli.ParseVerbFlags(fs, args); done {
		return code
	}

	if *endpoint == "" {
		fmt.Fprintln(os.Stderr, "rimsky conformance validation: --endpoint required")
		return 2
	}
	if *transport != "grpc" {
		fmt.Fprintf(os.Stderr, "rimsky conformance validation: --transport %q not supported; use grpc\n", *transport)
		return 2
	}

	target := strings.TrimPrefix(*endpoint, "grpc://")
	conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fmt.Fprintf(os.Stderr, "rimsky conformance validation: dial: %v\n", err)
		return 1
	}
	defer conn.Close()

	client := genv1.NewValidationClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	results := validation.Run(ctx, client, *role)
	return reportConformanceResults("rimsky conformance validation", results,
		func(r validation.CheckResult) string { return r.Name },
		func(r validation.CheckResult) error { return r.Err })
}

func runConformanceDataProcessing(args []string) int {
	fs := flag.NewFlagSet("rimsky conformance data-processing", flag.ContinueOnError)
	cli.SetUsage(fs, cli.UsageLine("conformance data-processing", "--endpoint <grpc://host:port> [flags]"))
	endpoint := fs.String("endpoint", "", "data-processing-service gRPC endpoint (e.g. grpc://localhost:9101)")
	transport := fs.String("transport", "grpc", "transport: grpc (the http+json bridge is not yet implemented for DataProcessing)")
	timeout := fs.Duration("timeout", 30*time.Second, "per-suite timeout")
	if code, done := cli.ParseVerbFlags(fs, args); done {
		return code
	}

	if *endpoint == "" {
		fmt.Fprintln(os.Stderr, "rimsky conformance data-processing: --endpoint required")
		return 2
	}
	if *transport != "grpc" {
		fmt.Fprintf(os.Stderr, "rimsky conformance data-processing: --transport %q not supported; use grpc\n", *transport)
		return 2
	}

	target := strings.TrimPrefix(*endpoint, "grpc://")
	conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fmt.Fprintf(os.Stderr, "rimsky conformance data-processing: dial: %v\n", err)
		return 1
	}
	defer conn.Close()

	client := genv1.NewDataProcessingClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	results := dataprocessing.Run(ctx, client)
	return reportConformanceResults("rimsky conformance data-processing", results,
		func(r dataprocessing.CheckResult) string { return r.Name },
		func(r dataprocessing.CheckResult) error { return r.Err })
}

func runConformanceProbe(args []string) int {
	fs := flag.NewFlagSet("rimsky conformance probe", flag.ContinueOnError)
	cli.SetUsage(fs, cli.UsageLine("conformance probe", "--endpoint <grpc://host:port> [flags]"))
	endpoint := fs.String("endpoint", "", "executor endpoint URL")
	transport := fs.String("transport", "grpc", "transport: grpc")
	timeout := fs.Duration("timeout", 15*time.Second, "request timeout")
	callbackBind := fs.String("callback-bind", "127.0.0.1", "interface for the callback receiver (use 0.0.0.0 with containerized executors)")
	callbackHost := fs.String("callback-host", "", "host the executor should reach the callback at (default: same as --callback-bind)")
	if code, done := cli.ParseVerbFlags(fs, args); done {
		return code
	}
	if *endpoint == "" {
		fmt.Fprintln(os.Stderr, "rimsky conformance probe: --endpoint required")
		return 2
	}
	if *transport != "grpc" {
		fmt.Fprintf(os.Stderr, "rimsky conformance probe: --transport %q not supported; use grpc\n", *transport)
		return 2
	}

	ep := conformance.Endpoint{Transport: *transport, URL: *endpoint}
	pool := conformance.NewClientPool()
	defer pool.Close()
	client, err := pool.GetOrCreate(ep)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dial: %v\n", err)
		return 1
	}

	receiver, err := conformance.StartCallbackReceiver(conformance.ReceiverOptions{
		BindHost:      *callbackBind,
		AdvertiseHost: *callbackHost,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "callback receiver: %v\n", err)
		return 1
	}
	defer func() { _ = receiver.Close() }()
	env := conformance.Env{Client: client, Callbacks: receiver}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	ok, err := conformance.ProbeStubMode(ctx, env, *timeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "conformance: %v\n", err)
		return 1
	}
	if !ok {
		fmt.Fprintln(os.Stderr, "conformance: stub-mode probe did not return {stub:true}")
		return 1
	}
	fmt.Println("conformance: stub-mode probe OK")
	return 0
}
