// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package bundled

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

type fakeExecReg struct {
	urls    []string
	failURL string
}

func (r *fakeExecReg) RegisterExecutor(url string, h ExecutorHandler) error {
	if h == nil {
		return errors.New("nil handler")
	}
	if url == r.failURL {
		return fmt.Errorf("duplicate registration for %q", url)
	}
	r.urls = append(r.urls, url)
	return nil
}

type fakeAliasSink struct {
	byName map[string]string
}

func (s *fakeAliasSink) RegisterExecutorAlias(name, inprocURL string) error {
	if s.byName == nil {
		s.byName = map[string]string{}
	}
	s.byName[name] = inprocURL
	return nil
}

type fakeCPReg struct {
	names    []string
	handlers map[string]claimproducer.ClaimProducer
	caps     map[string]claimproducer.Capabilities
}

func (r *fakeCPReg) RegisterClaimProducer(name string, handler claimproducer.ClaimProducer, caps claimproducer.Capabilities) error {
	if r.handlers == nil {
		r.handlers = map[string]claimproducer.ClaimProducer{}
		r.caps = map[string]claimproducer.Capabilities{}
	}
	r.names = append(r.names, name)
	r.handlers[name] = handler
	r.caps[name] = caps
	return nil
}

type fakeDiscovery struct {
	executors map[string][]string
	schemas   map[string][]byte
	producers map[string]claimproducer.Capabilities
}

func (d *fakeDiscovery) AdvertiseExecutor(name string, schema []byte, tags, errorClasses []string) {
	if d.executors == nil {
		d.executors = map[string][]string{}
		d.schemas = map[string][]byte{}
	}
	d.executors[name] = errorClasses
	d.schemas[name] = schema
}

func (d *fakeDiscovery) AdvertiseClaimProducer(name string, caps claimproducer.Capabilities) {
	if d.producers == nil {
		d.producers = map[string]claimproducer.Capabilities{}
	}
	d.producers[name] = caps
}

func clearBundledEnv(t *testing.T) {
	for _, name := range []string{
		"RIMSKY_CLAIM_PRODUCER_FILESYSTEM_CONFIG",
		"RIMSKY_CLAIM_PRODUCER_POSTGRES_CONFIG",
		"ANTHROPIC_API_KEY",
		"CLAUDE_CODE_OAUTH_TOKEN",
		"RIMSKY_EXECUTOR_STUB_MODE",
	} {
		t.Setenv(name, "")
		os.Unsetenv(name)
	}
}

func TestRegisterAllZeroConfigSkipsClaudeAgentWithoutCredentials(t *testing.T) {
	clearBundledEnv(t)
	execReg := &fakeExecReg{}
	aliases := &fakeAliasSink{}
	if err := RegisterAll(context.Background(), execReg, &fakeCPReg{}, aliases, &fakeDiscovery{}, Opts{}); err != nil {
		t.Fatalf("RegisterAll: %v", err)
	}
	if len(execReg.urls) != 3 {
		t.Fatalf("credential-less boot must register the three credential-free executors, got %v", execReg.urls)
	}
	if _, ok := aliases.byName["claude-agent"]; ok {
		t.Fatal("claude-agent must be skipped when no credentials and no stub mode are set")
	}
}

func TestRegisterAllZeroConfigRegistersExecutorsSkipsProducers(t *testing.T) {
	clearBundledEnv(t)
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	execReg := &fakeExecReg{}
	cpReg := &fakeCPReg{}
	aliases := &fakeAliasSink{}
	disc := &fakeDiscovery{}
	if err := RegisterAll(context.Background(), execReg, cpReg, aliases, disc, Opts{}); err != nil {
		t.Fatalf("RegisterAll: %v", err)
	}
	wantAliases := map[string]string{
		"claude-agent":          "inproc://claude-agent",
		"http-node":             "inproc://http-node",
		"verifier-http":         "inproc://verifier-http",
		"verifier-shape-checks": "inproc://verifier-shape-checks",
	}
	if len(execReg.urls) != len(wantAliases) {
		t.Fatalf("registered urls: %v", execReg.urls)
	}
	for name, url := range wantAliases {
		if got := aliases.byName[name]; got != url {
			t.Fatalf("alias %q: got %q, want %q", name, got, url)
		}
		if _, ok := disc.executors[name]; !ok {
			t.Fatalf("executor %q not advertised into discovery", name)
		}
		if len(disc.schemas[name]) == 0 {
			t.Fatalf("executor %q advertised without a schema", name)
		}
	}
	if len(disc.executors["claude-agent"]) == 0 {
		t.Fatal("claude-agent must advertise declared error classes")
	}
	if len(cpReg.names) != 0 {
		t.Fatalf("unconfigured claim producers must be skipped, got %v", cpReg.names)
	}
	if len(disc.producers) != 0 {
		t.Fatalf("skipped producers must not be advertised, got %v", disc.producers)
	}
}

func TestRegisterAllConfiguredFilesystemProducer(t *testing.T) {
	clearBundledEnv(t)
	root := t.TempDir()
	cfgPath := filepath.Join(t.TempDir(), "fs.yml")
	cfg := fmt.Sprintf("root: %s\ngrpc_port: 0\nhttp_port: 0\n", root)
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("RIMSKY_CLAIM_PRODUCER_FILESYSTEM_CONFIG", cfgPath)

	execReg := &fakeExecReg{}
	cpReg := &fakeCPReg{}
	disc := &fakeDiscovery{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := RegisterAll(ctx, execReg, cpReg, &fakeAliasSink{}, disc, Opts{}); err != nil {
		t.Fatalf("RegisterAll: %v", err)
	}
	if len(cpReg.names) != 1 || cpReg.names[0] != ProducerNameFilesystem {
		t.Fatalf("producers: %v", cpReg.names)
	}
	caps := cpReg.caps[ProducerNameFilesystem]
	if len(caps.WriteSemanticsAllowed) == 0 {
		t.Fatalf("registered capabilities carry no write-semantics envelope: %+v", caps)
	}
	discCaps, ok := disc.producers[ProducerNameFilesystem]
	if !ok {
		t.Fatal("configured producer must be advertised into discovery")
	}
	if len(discCaps.WriteSemanticsAllowed) != len(caps.WriteSemanticsAllowed) {
		t.Fatalf("discovery caps diverge from registered caps: %+v vs %+v", discCaps, caps)
	}
	handler := cpReg.handlers[ProducerNameFilesystem]
	if handler.Name() != ProducerNameFilesystem {
		t.Fatalf("handler name: %q", handler.Name())
	}
}

func TestRegisterAllFilesystemExplicitSyncStrategyRejectedInBundledMode(t *testing.T) {
	clearBundledEnv(t)
	root := t.TempDir()
	cfgPath := filepath.Join(t.TempDir(), "fs.yml")
	cfg := fmt.Sprintf("root: %s\nadmin_port: 9199\npick_policies:\n  \"queue-a\":\n    root: %s\n    on_commit: recycle\n    on_give_up: recycle\n    sync_strategy: explicit\n", root, root)
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("RIMSKY_CLAIM_PRODUCER_FILESYSTEM_CONFIG", cfgPath)

	err := RegisterAll(context.Background(), &fakeExecReg{}, &fakeCPReg{}, &fakeAliasSink{}, &fakeDiscovery{}, Opts{})
	if err == nil {
		t.Fatal("expected an error: bundled in-process mode never binds an admin listener, so sync_strategy: explicit can never be triggered")
	}
	if !strings.Contains(err.Error(), "queue-a") || !strings.Contains(err.Error(), "explicit") {
		t.Fatalf("got %v, want error naming the selector and sync_strategy: explicit", err)
	}
}

func TestRegisterAllInvalidFilesystemConfigAbortsNamingProducer(t *testing.T) {
	clearBundledEnv(t)
	cfgPath := filepath.Join(t.TempDir(), "fs.yml")
	if err := os.WriteFile(cfgPath, []byte("root: ''\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("RIMSKY_CLAIM_PRODUCER_FILESYSTEM_CONFIG", cfgPath)
	err := RegisterAll(context.Background(), &fakeExecReg{}, &fakeCPReg{}, &fakeAliasSink{}, &fakeDiscovery{}, Opts{})
	if err == nil || !strings.Contains(err.Error(), ProducerNameFilesystem) {
		t.Fatalf("got %v, want error naming %q", err, ProducerNameFilesystem)
	}
}

func TestRegisterAllExecutorRegistrationFailureNamesExecutor(t *testing.T) {
	clearBundledEnv(t)
	execReg := &fakeExecReg{failURL: "inproc://http-node"}
	err := RegisterAll(context.Background(), execReg, &fakeCPReg{}, &fakeAliasSink{}, &fakeDiscovery{}, Opts{})
	if err == nil || !strings.Contains(err.Error(), `register executor "http-node"`) {
		t.Fatalf("got %v, want error naming http-node", err)
	}
}

func TestRegisterAllRequiresSinks(t *testing.T) {
	clearBundledEnv(t)
	ctx := context.Background()
	if err := RegisterAll(ctx, nil, &fakeCPReg{}, &fakeAliasSink{}, &fakeDiscovery{}, Opts{}); err == nil {
		t.Fatal("nil executor registry must fail")
	}
	if err := RegisterAll(ctx, &fakeExecReg{}, nil, &fakeAliasSink{}, &fakeDiscovery{}, Opts{}); err == nil {
		t.Fatal("nil claim-producer registry must fail")
	}
	if err := RegisterAll(ctx, &fakeExecReg{}, &fakeCPReg{}, nil, &fakeDiscovery{}, Opts{}); err == nil {
		t.Fatal("nil alias sink must fail")
	}
	if err := RegisterAll(ctx, &fakeExecReg{}, &fakeCPReg{}, &fakeAliasSink{}, nil, Opts{}); err == nil {
		t.Fatal("nil discovery sink must fail")
	}
}
