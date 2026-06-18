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
	"regexp"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/action"
	"github.com/rimsky-ai/rimsky-core/lib/services/internal/ops"
	"github.com/rimsky-ai/rimsky-core/lib/services/stores/filesystem/server"
	fsstore "github.com/rimsky-ai/rimsky-core/lib/services/stores/filesystem/store"
)

const defaultConfigEnv = "STORE_FILESYSTEM_CONFIG"

type yamlConfig struct {
	Root                 string                    `yaml:"root"`
	Host                 string                    `yaml:"host"`
	GRPCPort             int                       `yaml:"grpc_port"`
	HTTPPort             int                       `yaml:"http_port"`
	HTTPBridgeURL        string                    `yaml:"http_bridge_url"`
	AdminPort            int                       `yaml:"admin_port"`
	PickPolicies         map[string]yamlPickPolicy `yaml:"pick_policies"`
	SweepIntervalSeconds int                       `yaml:"sweep_interval_seconds"`
}

type yamlPickPolicy struct {
	Root                     string        `yaml:"root"`
	FolderPattern            string        `yaml:"folder_pattern"`
	OnCommit                 action.Action `yaml:"on_commit"`
	OnGiveUp                 action.Action `yaml:"on_give_up"`
	VisibilityTimeoutSeconds int           `yaml:"visibility_timeout_seconds"`
	SyncStrategy             string        `yaml:"sync_strategy"`
}

func main() {
	ops.Setup(slog.LevelInfo)

	cfgPath := os.Getenv(defaultConfigEnv)
	if cfgPath == "" {
		fmt.Fprintf(os.Stderr, "store-filesystem: missing %s (path to YAML)\n", defaultConfigEnv)
		os.Exit(1)
	}
	cfg, err := loadYAML(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "store-filesystem: %v\n", err)
		os.Exit(1)
	}
	if cfg.Root == "" {
		fmt.Fprintf(os.Stderr, "store-filesystem: root is required\n")
		os.Exit(1)
	}
	host := cfg.Host
	if host == "" {
		host = "0.0.0.0"
	}

	policies := make(map[string]*fsstore.PickPolicy, len(cfg.PickPolicies))
	for selector, pp := range cfg.PickPolicies {
		var pat *regexp.Regexp
		if pp.FolderPattern != "" {
			p, err := regexp.Compile(pp.FolderPattern)
			if err != nil {
				fmt.Fprintf(os.Stderr, "store-filesystem: pick_policies[%q].folder_pattern: %v\n", selector, err)
				os.Exit(1)
			}
			pat = p
		}
		policies[selector] = &fsstore.PickPolicy{
			Root:              pp.Root,
			FolderPattern:     pat,
			OnCommit:          pp.OnCommit,
			OnGiveUp:          pp.OnGiveUp,
			VisibilityTimeout: time.Duration(pp.VisibilityTimeoutSeconds) * time.Second,
			SyncStrategy:      pp.SyncStrategy,
		}
	}
	sweepInterval := time.Duration(cfg.SweepIntervalSeconds) * time.Second
	if sweepInterval == 0 {
		sweepInterval = 60 * time.Second
	}

	if len(policies) > 0 && cfg.AdminPort == 0 {
		fmt.Fprintf(os.Stderr, "store-filesystem: admin_port is required when pick_policies is configured\n")
		os.Exit(1)
	}

	grpcLis, err := net.Listen("tcp", fmt.Sprintf("%s:%d", host, cfg.GRPCPort))
	if err != nil {
		fmt.Fprintf(os.Stderr, "store-filesystem: grpc listen: %v\n", err)
		os.Exit(1)
	}
	httpLis, err := net.Listen("tcp", fmt.Sprintf("%s:%d", host, cfg.HTTPPort))
	if err != nil {
		fmt.Fprintf(os.Stderr, "store-filesystem: http listen: %v\n", err)
		os.Exit(1)
	}
	var adminLis net.Listener
	if cfg.AdminPort > 0 {
		adminLis, err = net.Listen("tcp", fmt.Sprintf("%s:%d", host, cfg.AdminPort))
		if err != nil {
			fmt.Fprintf(os.Stderr, "store-filesystem: admin listen: %v\n", err)
			os.Exit(1)
		}
	}
	adminAddr := ""
	if adminLis != nil {
		adminAddr = adminLis.Addr().String()
	}
	slog.Info("store-filesystem started",
		"root", cfg.Root,
		"grpc_addr", grpcLis.Addr().String(),
		"http_addr", httpLis.Addr().String(),
		"admin_addr", adminAddr,
		"pick_policies", len(policies))

	ctx, cancel := signalContext()
	defer cancel()
	if err := server.Run(ctx, server.Config{
		Root:          cfg.Root,
		PickPolicies:  policies,
		SweepInterval: sweepInterval,
		HTTPBridgeURL: cfg.HTTPBridgeURL,
	}, grpcLis, httpLis, adminLis); err != nil {
		fmt.Fprintf(os.Stderr, "store-filesystem: server.Run: %v\n", err)
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
