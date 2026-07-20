// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/postgres"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/conformance/blobbackend"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/conformance/claimproducer"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/conformance/dataprocessing"
	conformance "github.com/rimsky-ai/rimsky-core/lib/protocols/conformance/executor"
	_ "github.com/rimsky-ai/rimsky-core/lib/protocols/conformance/executor/scenarios"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/conformance/publisher"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/conformance/validation"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	peer "github.com/rimsky-ai/rimsky-core/lib/runtime/peer"
)

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
	case "blob-backend":
		return runConformanceBlobBackend(rest)
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
	fmt.Fprintln(w, "usage: rimsky conformance <executor|claim-producer|publisher|validation|data-processing|blob-backend|probe> ...")
}

func runConformanceExecutor(args []string) int {
	fs := flag.NewFlagSet("rimsky conformance executor", flag.ContinueOnError)
	endpoint := fs.String("endpoint", "", "endpoint URL (executor or lifecycle peer)")
	transport := fs.String("transport", "grpc", "grpc|http")
	requireStub := fs.Bool("require-stub-mode", false, "fail if executor not in stub mode")
	only := fs.String("scenarios", "", "comma-list of scenario names to run (default: all)")
	skip := fs.String("skip", "", "comma-list of scenario names to skip")
	timeout := fs.Duration("timeout", 30*time.Second, "per-scenario timeout")
	checkObs := fs.Bool("check-observability", false, "additionally probe ExecutorObservability per spec §6")
	retentionSec := fs.Int("retention-test-seconds", 0, "if >0, drive a canned dispatch then sleep this long and verify GetTrace returns evicted=true (spec §6 retention check)")
	checkLifecycle := fs.Bool("check-lifecycle", false, "probe LifecycleSubscriber six-RPC sanity instead of running executor scenarios")
	callbackBind := fs.String("callback-bind", "127.0.0.1", "interface for the conformance callback receiver to bind (use 0.0.0.0 when the executor runs in a container)")
	callbackHost := fs.String("callback-host", "", "host the executor should reach the callback receiver at (default: same as --callback-bind; for containerized executors set to host.docker.internal or a routable host IP)")
	tlsMode := fs.String("tls", "off", "off|required — dial the executor with verified TLS against system roots (gRPC transport only; applies to the scenario suite, the observability probe, and the lifecycle probe alike)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *tlsMode != "off" && *tlsMode != "required" {
		fmt.Fprintf(os.Stderr, "rimsky conformance executor: --tls must be off or required, got %q\n", *tlsMode)
		return 2
	}

	if *endpoint == "" {
		fmt.Fprintln(os.Stderr, "rimsky conformance executor: --endpoint required")
		return 2
	}

	ctx := context.Background()

	if *checkLifecycle {
		if err := conformance.RunLifecycleCheck(ctx, *endpoint, *tlsMode, *timeout); err != nil {
			fmt.Fprintf(os.Stderr, "lifecycle: %v\n", err)
			return 1
		}
		fmt.Fprintln(os.Stdout, "lifecycle: ok")
		return 0
	}

	ep := conformance.Endpoint{Transport: *transport, URL: *endpoint, TLS: *tlsMode}

	onlyList := splitConformanceCSV(*only)
	skipList := splitConformanceCSV(*skip)

	results, err := conformance.Run(ctx, conformance.RunnerOpts{
		Endpoint:        ep,
		RequireStubMode: *requireStub,
		Only:            onlyList,
		Skip:            skipList,
		Timeout:         *timeout,
		CallbackBind:    *callbackBind,
		CallbackHost:    *callbackHost,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "conformance: %v\n", err)
		return 1
	}

	conformance.Summary(results, os.Stdout)
	for _, r := range results {
		if !r.Passed && !r.Skipped {
			return 1
		}
	}

	if *checkObs {
		err := conformance.RunObservabilityCheck(ctx, conformance.ObservabilityCheckOpts{
			Endpoint:             ep,
			RetentionTestSeconds: *retentionSec,
		}, func(format string, args ...any) { fmt.Printf(format, args...) })
		if err != nil {
			fmt.Fprintf(os.Stderr, "observability: %v\n", err)
			return 1
		}
		fmt.Fprintln(os.Stdout, "observability: ok")
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
	endpoint := fs.String("endpoint", "", "claim-producer-service gRPC endpoint (e.g. grpc://localhost:9101)")
	timeout := fs.Duration("timeout", 10*time.Second, "per-check timeout")
	checkObs := fs.Bool("check-observability", false, "additionally probe ClaimProducerObservability")
	retentionSec := fs.Int("retention-test-seconds", 0, "if >0, drive a canned claim then sleep this long and verify GetClaim returns evicted")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *endpoint == "" {
		fmt.Fprintln(os.Stderr, "rimsky conformance claim-producer: --endpoint required")
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	client, err := peer.Dial(ctx, "conformance-target", *endpoint, peer.TLSModeOff)
	if err != nil {
		fmt.Fprintf(os.Stderr, "rimsky conformance claim-producer: dial: %v\n", err)
		return 1
	}
	defer client.Close()

	results := claimproducer.Run(ctx, client)
	failed := 0
	for _, r := range results {
		if r.Err != nil {
			failed++
			fmt.Printf("FAIL  %s: %v\n", r.Name, r.Err)
			continue
		}
		fmt.Printf("ok    %s\n", r.Name)
	}
	if failed > 0 {
		fmt.Fprintf(os.Stderr, "rimsky conformance claim-producer: %d/%d checks failed\n", failed, len(results))
		return 1
	}

	if *checkObs {
		if err := claimproducer.RunObservabilityCheck(ctx, claimproducer.ObservabilityCheckOpts{
			Endpoint:             *endpoint,
			RetentionTestSeconds: *retentionSec,
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
	endpoint := fs.String("endpoint", "", "publisher gRPC endpoint (e.g. grpc://localhost:9202)")
	transport := fs.String("transport", "grpc", "transport: grpc")
	kind := fs.String("kind", "", "publisher kind to exercise (e.g. cron, http, object-store, webhook)")
	resolvedConfig := fs.String("resolved-config", "", "JSON resolved_config to drive Subscribe (kind-specific)")
	timeout := fs.Duration("timeout", 30*time.Second, "per-suite timeout")
	instanceID := fs.String("instance-id", "", "instance_id passed to Subscribe; required when publisher pushes to /instances/{id}/messages")
	messageType := fs.String("message-type", "", "message type passed to Subscribe and watched for on the control API (default: "+defaultConformancePublisherMessageType+")")
	controlAPI := fs.String("control-api", "", "control-API base URL to poll for the publisher's pushed message (e.g. http://localhost:8080); enables the MessagePush check together with --instance-id (overrides $RIMSKY_CONTROL_API)")
	if err := fs.Parse(args); err != nil {
		return 2
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
		controlAPIURL = os.Getenv("RIMSKY_CONTROL_API")
	}
	if *instanceID != "" && controlAPIURL != "" {
		receiver := publisher.NewMessageReceiver()
		opts.MessageReceiver = receiver
		go pollForConformancePublisherMessage(ctx, controlAPIURL, *instanceID, effectiveMessageType, receiver)
	}

	results := publisher.Run(ctx, client, opts)
	failed := 0
	for _, r := range results {
		if r.Err != nil {
			failed++
			fmt.Printf("FAIL  %s: %v\n", r.Name, r.Err)
			continue
		}
		fmt.Printf("ok    %s\n", r.Name)
	}
	if failed > 0 {
		fmt.Fprintf(os.Stderr, "rimsky conformance publisher: %d/%d checks failed\n", failed, len(results))
		return 1
	}
	return 0
}

func pollForConformancePublisherMessage(ctx context.Context, controlAPIURL, instanceID, messageType string, receiver *publisher.MessageReceiver) {
	c := cli.NewClient(controlAPIURL)
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
	endpoint := fs.String("endpoint", "", "validation-advertising service gRPC endpoint (e.g. grpc://localhost:9095)")
	transport := fs.String("transport", "grpc", "transport: grpc (the http+json bridge is not implemented for Validation)")
	role := fs.String("role", "executor", "role to validate against: executor | claim_producer | lifecycle_subscriber | sensor")
	timeout := fs.Duration("timeout", 30*time.Second, "per-suite timeout")
	if err := fs.Parse(args); err != nil {
		return 2
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
	failed := 0
	for _, r := range results {
		if r.Err != nil {
			failed++
			fmt.Printf("FAIL  %s: %v\n", r.Name, r.Err)
			continue
		}
		fmt.Printf("ok    %s\n", r.Name)
	}
	if failed > 0 {
		fmt.Fprintf(os.Stderr, "rimsky conformance validation: %d/%d checks failed\n", failed, len(results))
		return 1
	}
	return 0
}

func runConformanceDataProcessing(args []string) int {
	fs := flag.NewFlagSet("rimsky conformance data-processing", flag.ContinueOnError)
	endpoint := fs.String("endpoint", "", "data-processing-service gRPC endpoint (e.g. grpc://localhost:9101)")
	transport := fs.String("transport", "grpc", "transport: grpc (the http+json bridge is not yet implemented for DataProcessing)")
	timeout := fs.Duration("timeout", 30*time.Second, "per-suite timeout")
	if err := fs.Parse(args); err != nil {
		return 2
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
	failed := 0
	for _, r := range results {
		if r.Err != nil {
			failed++
			fmt.Printf("FAIL  %s: %v\n", r.Name, r.Err)
			continue
		}
		fmt.Printf("ok    %s\n", r.Name)
	}
	if failed > 0 {
		fmt.Fprintf(os.Stderr, "rimsky conformance data-processing: %d/%d checks failed\n", failed, len(results))
		return 1
	}
	return 0
}

func runConformanceBlobBackend(args []string) int {
	fs := flag.NewFlagSet("rimsky conformance blob-backend", flag.ContinueOnError)
	backend := fs.String("backend", "", "blob backend name: memory | filesystem | pg-largeobject")
	root := fs.String("root", "", "filesystem root (filesystem backend only)")
	pgDSN := fs.String("pg-conn-string", "", "Postgres DSN (pg-largeobject backend only)")
	timeout := fs.Duration("timeout", 60*time.Second, "per-check timeout")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *backend == "" {
		fmt.Fprintln(os.Stderr, "rimsky conformance blob-backend: --backend required")
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	be, cleanup, err := openBlobBackend(ctx, *backend, *root, *pgDSN)
	if err != nil {
		fmt.Fprintf(os.Stderr, "rimsky conformance blob-backend: open %s: %v\n", *backend, err)
		return 1
	}
	defer cleanup()

	results := blobbackend.Run(ctx, &blobBackendAdapter{be: be})
	failed := 0
	for _, r := range results {
		status := "PASS"
		if r.Err != nil {
			status = "FAIL"
			failed++
		}
		fmt.Printf("[%s] %s", status, r.Name)
		if r.Err != nil {
			fmt.Printf(": %v", r.Err)
		}
		fmt.Println()
	}
	if failed > 0 {
		fmt.Fprintf(os.Stderr, "rimsky conformance blob-backend: %d failure(s)\n", failed)
		return 1
	}
	return 0
}

type blobBackendAdapter struct {
	be persistence.BlobBackend
}

func (a *blobBackendAdapter) Write(ctx context.Context, hint string, bytes []byte) (blobbackend.Handle, error) {
	h, err := a.be.Write(ctx, persistence.BlobKey{Hint: hint}, bytes)
	if err != nil {
		return "", err
	}
	return blobbackend.Handle(h), nil
}

func (a *blobBackendAdapter) Read(ctx context.Context, handle blobbackend.Handle) ([]byte, error) {
	b, err := a.be.Read(ctx, persistence.Handle(handle))
	if errors.Is(err, persistence.ErrBlobNotFound) {
		return nil, blobbackend.ErrBlobNotFound
	}
	return b, err
}

func (a *blobBackendAdapter) ReadRange(ctx context.Context, handle blobbackend.Handle, offset, length int64) ([]byte, error) {
	b, err := a.be.ReadRange(ctx, persistence.Handle(handle), offset, length)
	if errors.Is(err, persistence.ErrBlobNotFound) {
		return nil, blobbackend.ErrBlobNotFound
	}
	return b, err
}

func (a *blobBackendAdapter) Delete(ctx context.Context, handle blobbackend.Handle) error {
	return a.be.Delete(ctx, persistence.Handle(handle))
}

func openBlobBackend(ctx context.Context, name, root, dsn string) (persistence.BlobBackend, func(), error) {
	switch name {
	case "memory":
		_ = os.Setenv(persistence.ProcessRoleEnv, "unified")
		return persistence.NewMemoryBackend(), func() {}, nil
	case "filesystem":
		if root == "" {
			return nil, nil, errors.New("--root required for filesystem backend")
		}
		be, err := persistence.NewFilesystemBackend(root)
		return be, func() {}, err
	case "pg-largeobject":
		if dsn == "" {
			return nil, nil, errors.New("--pg-conn-string required for pg-largeobject backend")
		}
		pcfg, err := pgxpool.ParseConfig(dsn)
		if err != nil {
			return nil, nil, fmt.Errorf("parse dsn: %w", err)
		}
		pool, err := pgxpool.NewWithConfig(ctx, pcfg)
		if err != nil {
			return nil, nil, fmt.Errorf("connect: %w", err)
		}
		be := postgres.NewPgLargeObjectBackend(pool)
		return be, func() { pool.Close() }, nil
	default:
		return nil, nil, fmt.Errorf("unknown backend %q (want memory | filesystem | pg-largeobject)", name)
	}
}

func runConformanceProbe(args []string) int {
	fs := flag.NewFlagSet("rimsky conformance probe", flag.ContinueOnError)
	endpoint := fs.String("endpoint", "", "executor endpoint URL")
	transport := fs.String("transport", "grpc", "grpc | http")
	timeout := fs.Duration("timeout", 15*time.Second, "request timeout")
	callbackBind := fs.String("callback-bind", "127.0.0.1", "interface for the callback receiver (use 0.0.0.0 with containerized executors)")
	callbackHost := fs.String("callback-host", "", "host the executor should reach the callback at (default: same as --callback-bind)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *endpoint == "" {
		fmt.Fprintln(os.Stderr, "rimsky conformance probe: --endpoint required")
		return 1
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

	ud, _ := structpb.NewStruct(map[string]any{"stub_probe": true})
	req := &genv1.ExecuteRequest{
		NodeId:      "conformance-probe",
		InstanceId:  "conformance-probe",
		NodeType:    "conformance-probe",
		Attributes:  ud,
		CallbackUrl: receiver.URL(),
	}
	outcome, err := client.Execute(ctx, req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "execute: %v\n", err)
		return 1
	}

	settled, err := conformance.AwaitTerminal(ctx, outcome, env)
	if err != nil {
		fmt.Fprintf(os.Stderr, "conformance: %v\n", err)
		return 1
	}
	switch oc := settled.GetOutcome().(type) {
	case *genv1.Outcome_Success:
		m := oc.Success.GetAttributesDelta().AsMap()
		if v, ok := m["stub"].(bool); !ok || !v {
			fmt.Fprintf(os.Stderr, "conformance: stub-mode probe did not return {stub:true}, got %+v\n", m)
			return 1
		}
		fmt.Println("conformance: stub-mode probe OK")
		return 0
	case *genv1.Outcome_Error:
		fmt.Fprintf(os.Stderr, "conformance: got Error %s (%v)\n", oc.Error.ErrorClass, oc.Error.GetPayload().AsMap())
		return 1
	case *genv1.Outcome_AwaitAsync:
		fmt.Fprintln(os.Stderr, "conformance: stub-mode probe ended at AwaitAsyncCallback but no callback arrived")
		return 1
	}
	fmt.Fprintf(os.Stderr, "conformance: unexpected Outcome variant %T\n", settled.GetOutcome())
	return 1
}
