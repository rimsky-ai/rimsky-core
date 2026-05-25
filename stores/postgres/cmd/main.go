// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// store-postgres is the standard direct-mode postgres store-service
// with store-side pick-policy support. Per spec §8.2.
//
// Loads its YAML config from STORE_POSTGRES_CONFIG, opens listeners
// on configured gRPC + HTTP + admin ports, and calls server.Run.
//
// YAML shape (see config-example.yml):
//
//	connection: "postgres://..."
//	write_semantics: direct
//	pick_policies:
//	  "@queue":
//	    items_table: items_inbound
//	    on_commit: pop
//	    on_give_up: recycle
//	    visibility_timeout_seconds: 300
//	host: 0.0.0.0
//	grpc_port: 9101
//	http_port: 9111
//	admin_port: 9121
//	sweep_interval_seconds: 30
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

	corestore "github.com/fallguyconsulting/rimsky/foundation/locks"
	"github.com/fallguyconsulting/rimsky/stores/common/action"
	"github.com/fallguyconsulting/rimsky/stores/postgres/server"
	pgsstore "github.com/fallguyconsulting/rimsky/stores/postgres/store"
)

// itemsTableIdentRe is the shared strict SQL identifier shape (see
// pgsstore.ItemsTableIdentRegex). All three layers — this startup
// check, server/observability.go, and Store.New — apply the same
// regex so an items_table value that passes one layer passes all
// three. Lowercase only: postgres folds unquoted identifiers to
// lowercase, so a mixed-case match would silently mismatch
// verifyItemsTable at runtime.
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
	// EnableExecutor registers the Executor protocol alongside the
	// store's ClaimProducer so the same binary plays both roles for
	// the atomic-staging-with-verifier pattern. Per spec
	// .ok-planner/specs/2026-05-19-multi-instance-template-ergonomics-design.md
	// §Item 6.
	EnableExecutor bool `yaml:"enable_executor"`
}

type yamlPickPolicy struct {
	ItemsTable               string        `yaml:"items_table"`
	OnCommit                 action.Action `yaml:"on_commit"`
	OnGiveUp                 action.Action `yaml:"on_give_up"`
	VisibilityTimeoutSeconds int           `yaml:"visibility_timeout_seconds"`
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))

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
	ws := corestore.WriteSemantics(cfg.WriteSemantics)
	if ws == "" {
		ws = corestore.WriteSemanticsStagedAsync
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
