// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package server

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/action"
	claimproducer "github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	pgsstore "github.com/rimsky-ai/rimsky-core/lib/services/claim_producers/postgres/store"
	"github.com/rimsky-ai/rimsky-core/lib/services/internal/agentport"
)

const ConfigEnv = "STORE_POSTGRES_CONFIG"

const (
	defaultGRPCPort = 9101
	defaultHTTPPort = 9111
)

type Opts struct {
	Configured        bool
	Connection        string
	WriteSemantics    claimproducer.WriteSemantics
	PickPolicies      map[string]*pgsstore.PickPolicy
	PartitionPolicies map[string]*pgsstore.PartitionPolicy
	SweepInterval     time.Duration
	HTTPBridgeURL     string
	EnableLifecycle   bool
	EnableExecutor    bool
	LedgerMaxRecords  int
	Host              string
	GRPCPort          int
	HTTPPort          int
	AdminPort         int
}

func (o Opts) ServerConfig() Config {
	return Config{
		Connection:        o.Connection,
		WriteSemantics:    o.WriteSemantics,
		PickPolicies:      o.PickPolicies,
		PartitionPolicies: o.PartitionPolicies,
		SweepInterval:     o.SweepInterval,
		HTTPBridgeURL:     o.HTTPBridgeURL,
		EnableLifecycle:   o.EnableLifecycle,
		EnableExecutor:    o.EnableExecutor,
		LedgerMaxRecords:  o.LedgerMaxRecords,
	}
}

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
	LedgerMaxRecords     int                            `yaml:"ledger_max_records"`
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

func LoadOptsFromEnv() (Opts, error) {
	cfgPath := os.Getenv(ConfigEnv)
	if cfgPath == "" {
		return Opts{}, nil
	}
	cfg, err := loadYAML(cfgPath)
	if err != nil {
		return Opts{}, err
	}
	if cfg.Connection == "" {
		return Opts{}, errors.New("connection is required")
	}
	host := cfg.Host
	if host == "" {
		host = "0.0.0.0"
	}
	ws := claimproducer.WriteSemanticsStagedAsync
	if cfg.WriteSemantics != "" {
		parsed, ok := claimproducer.ParseWriteSemantics(cfg.WriteSemantics)
		if !ok {
			return Opts{}, fmt.Errorf("write_semantics %q is not a recognized value (sync | staged_async | blocking_async | read_only)", cfg.WriteSemantics)
		}
		ws = parsed
	}
	policies := make(map[string]*pgsstore.PickPolicy, len(cfg.PickPolicies))
	for selector, pp := range cfg.PickPolicies {
		if !pgsstore.ItemsTableIdentRegex.MatchString(pp.ItemsTable) {
			return Opts{}, fmt.Errorf(
				"pick_policies[%q]: items_table %q is not a valid SQL identifier (lowercase letters/digits/underscore; not starting with a digit)",
				selector, pp.ItemsTable)
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
		if !pgsstore.ItemsTableIdentRegex.MatchString(pp.ItemsTable) {
			return Opts{}, fmt.Errorf(
				"partition_policies[%q]: items_table %q is not a valid SQL identifier (lowercase letters/digits/underscore; not starting with a digit)",
				name, pp.ItemsTable)
		}
		paramOrder, perr := extractParamOrder(pp.ParamsSchema)
		if perr != nil {
			return Opts{}, fmt.Errorf("partition_policies[%q]: params_schema: %w", name, perr)
		}
		partitionPolicies[name] = &pgsstore.PartitionPolicy{
			ItemsTable: pp.ItemsTable,
			Select:     pp.Select,
			Where:      pp.Where,
			ParamOrder: paramOrder,
			Limit:      pp.Limit,
		}
	}

	sweep := time.Duration(cfg.SweepIntervalSeconds) * time.Second
	if sweep == 0 {
		sweep = 30 * time.Second
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
		Configured:        true,
		Connection:        cfg.Connection,
		WriteSemantics:    ws,
		PickPolicies:      policies,
		PartitionPolicies: partitionPolicies,
		SweepInterval:     sweep,
		HTTPBridgeURL:     cfg.HTTPBridgeURL,
		EnableLifecycle:   cfg.EnableLifecycle,
		EnableExecutor:    cfg.EnableExecutor,
		LedgerMaxRecords:  cfg.LedgerMaxRecords,
		Host:              host,
		GRPCPort:          grpcPort,
		HTTPPort:          httpPortCfg,
		AdminPort:         cfg.AdminPort,
	}, nil
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
	expanded, err := expandConfigEnv(string(raw))
	if err != nil {
		return yamlConfig{}, fmt.Errorf("expand config %q: %w", path, err)
	}
	var cfg yamlConfig
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return yamlConfig{}, fmt.Errorf("parse config %q: %w", path, err)
	}
	return cfg, nil
}

func expandConfigEnv(raw string) (string, error) {
	var missing []string
	expanded := os.Expand(raw, func(name string) string {
		v, ok := os.LookupEnv(name)
		if !ok {
			missing = append(missing, name)
			return ""
		}
		return v
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("references undefined environment variable(s): %s (a literal '$' followed by an identifier is parsed as a reference; if this was a literal dollar sign, remove the ambiguity)", strings.Join(missing, ", "))
	}
	return expanded, nil
}
