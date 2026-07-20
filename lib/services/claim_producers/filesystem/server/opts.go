// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package server

import (
	"fmt"
	"os"
	"regexp"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/action"
	fsstore "github.com/rimsky-ai/rimsky-core/lib/services/claim_producers/filesystem/store"
	"github.com/rimsky-ai/rimsky-core/lib/services/internal/agentport"
)

const ConfigEnv = "STORE_FILESYSTEM_CONFIG"

type Opts struct {
	Configured       bool
	Root             string
	PickPolicies     map[string]*fsstore.PickPolicy
	SweepInterval    time.Duration
	HTTPBridgeURL    string
	EnableLifecycle  bool
	LedgerMaxRecords int
	Host             string
	GRPCPort         int
	HTTPPort         int
	AdminPort        int
}

func (o Opts) ServerConfig() Config {
	return Config{
		Root:             o.Root,
		PickPolicies:     o.PickPolicies,
		SweepInterval:    o.SweepInterval,
		HTTPBridgeURL:    o.HTTPBridgeURL,
		EnableLifecycle:  o.EnableLifecycle,
		LedgerMaxRecords: o.LedgerMaxRecords,
	}
}

type yamlConfig struct {
	Root                 string                    `yaml:"root"`
	Host                 string                    `yaml:"host"`
	GRPCPort             int                       `yaml:"grpc_port"`
	HTTPPort             int                       `yaml:"http_port"`
	HTTPBridgeURL        string                    `yaml:"http_bridge_url"`
	AdminPort            int                       `yaml:"admin_port"`
	PickPolicies         map[string]yamlPickPolicy `yaml:"pick_policies"`
	SweepIntervalSeconds int                       `yaml:"sweep_interval_seconds"`
	EnableLifecycle      bool                      `yaml:"enable_lifecycle"`
	LedgerMaxRecords     int                       `yaml:"ledger_max_records"`
}

type yamlPickPolicy struct {
	Root                     string        `yaml:"root"`
	FolderPattern            string        `yaml:"folder_pattern"`
	OnCommit                 action.Action `yaml:"on_commit"`
	OnGiveUp                 action.Action `yaml:"on_give_up"`
	VisibilityTimeoutSeconds int           `yaml:"visibility_timeout_seconds"`
	SyncStrategy             string        `yaml:"sync_strategy"`
}

func LoadOptsFromEnv() (Opts, error) {
	cfgPath := os.Getenv(ConfigEnv)
	if cfgPath == "" {
		return Opts{}, nil
	}
	cfg, err := loadYAML(cfgPath)
	if err != nil {
		return Opts{}, err
	}
	if cfg.Root == "" {
		return Opts{}, fmt.Errorf("root is required")
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
				return Opts{}, fmt.Errorf("pick_policies[%q].folder_pattern: %w", selector, err)
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
		return Opts{}, fmt.Errorf("admin_port is required when pick_policies is configured")
	}

	grpcPort, err := agentport.Override(cfg.GRPCPort)
	if err != nil {
		return Opts{}, err
	}

	return Opts{
		Configured:       true,
		Root:             cfg.Root,
		PickPolicies:     policies,
		SweepInterval:    sweepInterval,
		HTTPBridgeURL:    cfg.HTTPBridgeURL,
		EnableLifecycle:  cfg.EnableLifecycle,
		LedgerMaxRecords: cfg.LedgerMaxRecords,
		Host:             host,
		GRPCPort:         grpcPort,
		HTTPPort:         cfg.HTTPPort,
		AdminPort:        cfg.AdminPort,
	}, nil
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
