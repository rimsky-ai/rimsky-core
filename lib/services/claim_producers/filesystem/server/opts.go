// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package server

import (
	"fmt"
	"os"
	"regexp"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/action"
	configload "github.com/rimsky-ai/rimsky-core/lib/protocols/config"
	fsstore "github.com/rimsky-ai/rimsky-core/lib/services/claim_producers/filesystem/store"
	"github.com/rimsky-ai/rimsky-core/lib/services/internal/agentport"
)

const ConfigEnv = "RIMSKY_CLAIM_PRODUCER_FILESYSTEM_CONFIG"

// @decision: default-port-allocation
const (
	defaultGRPCPort = 9100
	defaultHTTPPort = 9110
)

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

	if cfg.AdminPort == 0 {
		for selector, pp := range policies {
			if pp.SyncStrategy == "explicit" {
				return Opts{}, fmt.Errorf("admin_port is required when pick_policies[%q].sync_strategy is %q", selector, "explicit")
			}
		}
	}

	grpcPortCfg := cfg.GRPCPort
	if grpcPortCfg == 0 {
		grpcPortCfg = defaultGRPCPort
	}
	httpPortCfg := cfg.HTTPPort
	if httpPortCfg == 0 {
		httpPortCfg = defaultHTTPPort
	}
	grpcPort, err := agentport.Override(grpcPortCfg)
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
		HTTPPort:         httpPortCfg,
		AdminPort:        cfg.AdminPort,
	}, nil
}

func loadYAML(path string) (yamlConfig, error) {
	var cfg yamlConfig
	if err := configload.LoadFile(path, &cfg); err != nil {
		return yamlConfig{}, err
	}
	return cfg, nil
}
