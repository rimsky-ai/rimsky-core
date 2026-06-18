// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/action"
	claimproducer "github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/lib/services/internal/ops"
	"github.com/rimsky-ai/rimsky-core/lib/services/stores/postgres/server"
	pgsstore "github.com/rimsky-ai/rimsky-core/lib/services/stores/postgres/store"
)

var itemsTableIdentRe = pgsstore.ItemsTableIdentRegex

const defaultConfigEnv = "STORE_POSTGRES_CONFIG"

type yamlConfig struct {
	Connection           string                    `yaml:"connection"`
	WriteSemantics       string                    `yaml:"write_semantics"`
	PickPolicies         map[string]yamlPickPolicy `yaml:"pick_policies"`
	Host                 string                    `yaml:"host"`
	GRPCPort             int                       `yaml:"grpc_port"`
	HTTPPort             int                       `yaml:"http_port"`
	HTTPBridgeURL        string                    `yaml:"http_bridge_url"`
	AdminPort            int                       `yaml:"admin_port"`
	SweepIntervalSeconds int                       `yaml:"sweep_interval_seconds"`
	EnableLifecycle      bool                      `yaml:"enable_lifecycle"`
	EnableExecutor       bool                      `yaml:"enable_executor"`
}

type yamlPickPolicy struct {
	ItemsTable               string        `yaml:"items_table"`
	OnCommit                 action.Action `yaml:"on_commit"`
	OnGiveUp                 action.Action `yaml:"on_give_up"`
	VisibilityTimeoutSeconds int           `yaml:"visibility_timeout_seconds"`
}

func main() {
	ops.Setup(slog.LevelInfo)

	cfgPath := os.Getenv(defaultConfigEnv)
	if cfgPath == "" {
		fmt.Fprintf(os.Stderr, "store-postgres: missing %s (path to YAML)\n", defaultConfigEnv)
		os.Exit(1)
	}
	cfg, err := loadYAML(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "store-postgres: %v\n", err)
		os.Exit(1)
	}
	if cfg.Connection == "" {
		fmt.Fprintf(os.Stderr, "store-postgres: connection is required\n")
		os.Exit(1)
	}
	host := cfg.Host
	if host == "" {
		host = "0.0.0.0"
	}
	ws := claimproducer.WriteSemantics(cfg.WriteSemantics)
	if ws == "" {
		ws = claimproducer.WriteSemanticsStagedAsync
	}
	policies := make(map[string]*pgsstore.PickPolicy, len(cfg.PickPolicies))
	for selector, pp := range cfg.PickPolicies {
		if !itemsTableIdentRe.MatchString(pp.ItemsTable) {
			fmt.Fprintf(os.Stderr,
				"store-postgres: pick_policies[%q]: items_table %q is not a valid SQL identifier (lowercase letters/digits/underscore; not starting with a digit)\n",
				selector, pp.ItemsTable)
			os.Exit(1)
		}
		policies[selector] = &pgsstore.PickPolicy{
			ItemsTable:        pp.ItemsTable,
			OnCommit:          pp.OnCommit,
			OnGiveUp:          pp.OnGiveUp,
			VisibilityTimeout: time.Duration(pp.VisibilityTimeoutSeconds) * time.Second,
		}
	}

	grpcLis, err := net.Listen("tcp", fmt.Sprintf("%s:%d", host, cfg.GRPCPort))
	if err != nil {
		fmt.Fprintf(os.Stderr, "store-postgres: grpc listen: %v\n", err)
		os.Exit(1)
	}
	httpLis, err := net.Listen("tcp", fmt.Sprintf("%s:%d", host, cfg.HTTPPort))
	if err != nil {
		fmt.Fprintf(os.Stderr, "store-postgres: http listen: %v\n", err)
		os.Exit(1)
	}
	var adminLis net.Listener
	if cfg.AdminPort > 0 {
		adminLis, err = net.Listen("tcp", fmt.Sprintf("%s:%d", host, cfg.AdminPort))
		if err != nil {
			fmt.Fprintf(os.Stderr, "store-postgres: admin listen: %v\n", err)
			os.Exit(1)
		}
	}

	sweep := time.Duration(cfg.SweepIntervalSeconds) * time.Second
	if sweep == 0 {
		sweep = 30 * time.Second
	}

	slog.Info("store-postgres started",
		"grpc_addr", grpcLis.Addr().String(),
		"http_addr", httpLis.Addr().String(),
		"admin_port", cfg.AdminPort,
		"pick_policies", len(policies),
		"sweep_interval", sweep)

	ctx, cancel := signalContext()
	defer cancel()

	if err := server.Run(ctx, server.Config{
		Connection:      cfg.Connection,
		WriteSemantics:  ws,
		PickPolicies:    policies,
		SweepInterval:   sweep,
		HTTPBridgeURL:   cfg.HTTPBridgeURL,
		EnableLifecycle: cfg.EnableLifecycle,
		EnableExecutor:  cfg.EnableExecutor,
	}, grpcLis, httpLis, adminLis); err != nil {
		fmt.Fprintf(os.Stderr, "store-postgres: server.Run: %v\n", err)
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
