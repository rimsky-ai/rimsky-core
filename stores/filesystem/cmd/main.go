// store-filesystem is the standard direct-mode filesystem store-service.
// Per spec docs/specs/2026-04-27-stores-redesign-v3-design.md §8.1.
//
// Loads its YAML config from STORE_FILESYSTEM_CONFIG, opens listeners
// on configured gRPC + HTTP ports, and calls server.Run.
//
// YAML shape (see config-example.yml):
//
//	root: /var/lib/rimsky-store/content
//	grpc_port: 9100
//	http_port: 9101
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	"gopkg.in/yaml.v3"

	"github.com/fallguy/rimsky/stores/filesystem/server"
)

const defaultConfigEnv = "STORE_FILESYSTEM_CONFIG"

type yamlConfig struct {
	Root     string `yaml:"root"`
	Host     string `yaml:"host"`
	GRPCPort int    `yaml:"grpc_port"`
	HTTPPort int    `yaml:"http_port"`
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))

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
	slog.Info("store-filesystem started",
		"root", cfg.Root,
		"grpc_addr", grpcLis.Addr().String(),
		"http_addr", httpLis.Addr().String())

	ctx, cancel := signalContext()
	defer cancel()
	if err := server.Run(ctx, server.Config{Root: cfg.Root}, grpcLis, httpLis); err != nil {
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
