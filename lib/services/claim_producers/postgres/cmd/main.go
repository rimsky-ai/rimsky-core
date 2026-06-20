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
	"github.com/rimsky-ai/rimsky-core/lib/services/claim_producers/postgres/server"
	pgsstore "github.com/rimsky-ai/rimsky-core/lib/services/claim_producers/postgres/store"
	"github.com/rimsky-ai/rimsky-core/lib/services/internal/ops"
)

var itemsTableIdentRe = pgsstore.ItemsTableIdentRegex

const defaultConfigEnv = "STORE_POSTGRES_CONFIG"

type yamlConfig struct {
	Connection           string                         `yaml:"connection"`
	WriteSemantics       string                         `yaml:"write_semantics"`
	PickPolicies         map[string]yamlPickPolicy      `yaml:"pick_policies"`
	PartitionPolicies    map[string]yamlPartitionPolicy `yaml:"partition_policies"`
	Host                 string                         `yaml:"host"`
	GRPCPort             int                            `yaml:"grpc_port"`
	HTTPPort             int                            `yaml:"http_port"`
	HTTPBridgeURL        string                         `yaml:"http_bridge_url"`
	AdminPort            int                            `yaml:"admin_port"`
	SweepIntervalSeconds int                            `yaml:"sweep_interval_seconds"`
	EnableLifecycle      bool                           `yaml:"enable_lifecycle"`
	EnableExecutor       bool                           `yaml:"enable_executor"`
}

type yamlPickPolicy struct {
	ItemsTable               string        `yaml:"items_table"`
	OnCommit                 action.Action `yaml:"on_commit"`
	OnGiveUp                 action.Action `yaml:"on_give_up"`
	VisibilityTimeoutSeconds int           `yaml:"visibility_timeout_seconds"`
}

type yamlPartitionPolicy struct {
	ItemsTable   string           `yaml:"items_table"`
	Select       string           `yaml:"select"`
	Where        string           `yaml:"where"`
	ParamsSchema yamlParamsSchema `yaml:"params_schema"`
	Limit        int              `yaml:"limit"`
}

type yamlParamsSchema struct {
	Properties yaml.Node `yaml:"properties"`
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

	partitionPolicies := make(map[string]*pgsstore.PartitionPolicy, len(cfg.PartitionPolicies))
	for name, pp := range cfg.PartitionPolicies {
		if !itemsTableIdentRe.MatchString(pp.ItemsTable) {
			fmt.Fprintf(os.Stderr,
				"store-postgres: partition_policies[%q]: items_table %q is not a valid SQL identifier (lowercase letters/digits/underscore; not starting with a digit)\n",
				name, pp.ItemsTable)
			os.Exit(1)
		}
		paramOrder, perr := extractParamOrder(pp.ParamsSchema)
		if perr != nil {
			fmt.Fprintf(os.Stderr,
				"store-postgres: partition_policies[%q]: params_schema: %v\n", name, perr)
			os.Exit(1)
		}
		partitionPolicies[name] = &pgsstore.PartitionPolicy{
			ItemsTable: pp.ItemsTable,
			Select:     pp.Select,
			Where:      pp.Where,
			ParamOrder: paramOrder,
			Limit:      pp.Limit,
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
		Connection:        cfg.Connection,
		WriteSemantics:    ws,
		PickPolicies:      policies,
		PartitionPolicies: partitionPolicies,
		SweepInterval:     sweep,
		HTTPBridgeURL:     cfg.HTTPBridgeURL,
		EnableLifecycle:   cfg.EnableLifecycle,
		EnableExecutor:    cfg.EnableExecutor,
	}, grpcLis, httpLis, adminLis); err != nil {
		fmt.Fprintf(os.Stderr, "store-postgres: server.Run: %v\n", err)
		os.Exit(1)
	}
}

func extractParamOrder(schema yamlParamsSchema) ([]string, error) {
	if schema.Properties.Kind == 0 {
		return nil, nil
	}
	if schema.Properties.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("properties must be a YAML mapping (got kind=%d)", schema.Properties.Kind)
	}
	content := schema.Properties.Content
	if len(content)%2 != 0 {
		return nil, fmt.Errorf("properties mapping has odd content count %d", len(content))
	}
	out := make([]string, 0, len(content)/2)
	for i := 0; i < len(content); i += 2 {
		key := content[i]
		if key.Kind != yaml.ScalarNode {
			return nil, fmt.Errorf("properties: key at index %d is not a scalar", i/2)
		}
		out = append(out, key.Value)
	}
	return out, nil
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
