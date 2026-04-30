// store-stub is the stub store-service: in-memory state, deterministic
// behavior. Per spec §8.3.
//
// Loads its YAML config from STORE_STUB_CONFIG, opens listeners on
// configured gRPC + HTTP ports, and calls server.Run.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	"gopkg.in/yaml.v3"

	corestore "github.com/fallguy/rimsky/core/store"
	"github.com/fallguy/rimsky/stores/stub/server"
	stubstore "github.com/fallguy/rimsky/stores/stub/store"
)

const defaultConfigEnv = "STORE_STUB_CONFIG"

type yamlConfig struct {
	WriteSemantics string                    `yaml:"write_semantics"`
	PickPolicies   map[string]yamlPickPolicy `yaml:"pick_policies"`
	Host           string                    `yaml:"host"`
	GRPCPort       int                       `yaml:"grpc_port"`
	HTTPPort       int                       `yaml:"http_port"`
}

type yamlPickPolicy struct {
	OnCommitDefault string   `yaml:"on_commit_default"`
	OnGiveUpDefault string   `yaml:"on_give_up_default"`
	InitialItems    []string `yaml:"initial_items"`
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))

	cfgPath := os.Getenv(defaultConfigEnv)
	if cfgPath == "" {
		fmt.Fprintf(os.Stderr, "store-stub: missing %s (path to YAML)\n", defaultConfigEnv)
		os.Exit(1)
	}
	cfg, err := loadYAML(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "store-stub: %v\n", err)
		os.Exit(1)
	}
	host := cfg.Host
	if host == "" {
		host = "0.0.0.0"
	}
	ws := corestore.WriteSemantics(cfg.WriteSemantics)
	if ws == "" {
		ws = corestore.WriteSemanticsDirect
	}
	policies := make(map[string]stubstore.PickPolicyConfig, len(cfg.PickPolicies))
	for selector, p := range cfg.PickPolicies {
		var initial []json.RawMessage
		for _, raw := range p.InitialItems {
			initial = append(initial, json.RawMessage(raw))
		}
		policies[selector] = stubstore.PickPolicyConfig{
			OnCommitDefault: p.OnCommitDefault,
			OnGiveUpDefault: p.OnGiveUpDefault,
			InitialItems:    initial,
		}
	}

	grpcLis, err := net.Listen("tcp", fmt.Sprintf("%s:%d", host, cfg.GRPCPort))
	if err != nil {
		fmt.Fprintf(os.Stderr, "store-stub: grpc listen: %v\n", err)
		os.Exit(1)
	}
	httpLis, err := net.Listen("tcp", fmt.Sprintf("%s:%d", host, cfg.HTTPPort))
	if err != nil {
		fmt.Fprintf(os.Stderr, "store-stub: http listen: %v\n", err)
		os.Exit(1)
	}
	slog.Info("store-stub started",
		"grpc_addr", grpcLis.Addr().String(),
		"http_addr", httpLis.Addr().String(),
		"write_semantics", ws)

	ctx, cancel := signalContext()
	defer cancel()
	if err := server.Run(ctx, server.Config{
		Substrate: stubstore.Config{
			Capabilities: corestore.Capabilities{WriteSemantics: ws},
			PickPolicies: policies,
		},
	}, grpcLis, httpLis); err != nil {
		fmt.Fprintf(os.Stderr, "store-stub: server.Run: %v\n", err)
		os.Exit(1)
	}
}

func loadYAML(path string) (yamlConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return yamlConfig{}, fmt.Errorf("read config %q: %w", path, err)
	}
	expanded := os.ExpandEnv(string(raw))
	var cfg yamlConfig
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return yamlConfig{}, fmt.Errorf("parse config %q: %w", path, err)
	}
	return cfg, nil
}

func signalContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()
	return ctx, cancel
}
