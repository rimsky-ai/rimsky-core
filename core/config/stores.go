// Stores + executors config + remote-dialing helpers for the three
// rimsky processes. Per spec docs/specs/2026-04-27-stores-redesign-v3-
// design.md §6 (stores) and docs/specs/2026-05-01-control-plane-and-
// store-lifecycle-design.md §3.1 / §6.6 (unified rimsky.yml shape:
// stores + named_locks + executors loaded together by all three
// rimsky processes from $RIMSKY_CONFIG).
package config

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/fallguy/rimsky/core/store"
	"github.com/fallguy/rimsky/core/store/remote"
)

// capabilitiesHandshakeTimeout bounds the per-store Capabilities() RPC
// at startup. Without it a store-service that accepts the connection
// but never replies blocks the rimsky process forever.
const capabilitiesHandshakeTimeout = 30 * time.Second

// StoreEntry is the per-store config from rimsky.yml: an endpoint to
// dial plus the operator-declared capability requirements that the
// store-service must advertise back at the Capabilities() handshake.
type StoreEntry struct {
	Endpoint     string
	Capabilities store.Capabilities
}

// RemoteStoresConfig is the parsed `stores:` block from rimsky.yml.
// Keys are operator-chosen store names; values carry the endpoint URL
// and declared capabilities. Per spec §6.1 — no `kind`, no
// `connection`, no `pick_policies`. Substrate-specific keys live in
// the store-service's own config.
type RemoteStoresConfig struct {
	Stores map[string]StoreEntry
}

// ExecutorEntry is the per-executor config from rimsky.yml's
// `executors:` block. Per docs/specs/2026-05-01-control-plane-and-
// store-lifecycle-design.md §3.1.
type ExecutorEntry struct {
	Transport string // "grpc" | "http"
	Endpoint  string // e.g. "claude-agent:9090"
	TLS       string // "off" | "optional" | "required" (matches executor.Endpoint)
}

// ExecutorsConfig is the parsed `executors:` block from rimsky.yml.
// Keys are operator-chosen executor names referenced from template
// node defs (`executor:` field). Validated at template registration
// via the ExecutorDeclared hook.
type ExecutorsConfig struct {
	Executors map[string]ExecutorEntry
}

// Validate rejects empty transport or endpoint per spec §6.6 step 3
// (syntactic only; no DNS / dial).
func (c ExecutorsConfig) Validate() error {
	for name, e := range c.Executors {
		if e.Transport == "" {
			return fmt.Errorf("executor %q: transport required", name)
		}
		if e.Endpoint == "" {
			return fmt.Errorf("executor %q: endpoint required", name)
		}
	}
	return nil
}

// ExecutorDeclared returns true when name appears in the executors
// block. Used by the control-api template-validation hook (per spec
// §1.4 invariant: a node referencing an undeclared executor fails
// registration validation).
func (c ExecutorsConfig) ExecutorDeclared(name string) bool {
	_, ok := c.Executors[name]
	return ok
}

// RimskyConfig is the parsed rimsky.yml: the unified deployment-shape
// config loaded by all three rimsky processes from $RIMSKY_CONFIG. Per
// spec §3.1.
type RimskyConfig struct {
	Stores     RemoteStoresConfig
	NamedLocks store.NamedLocksConfig
	Executors  ExecutorsConfig
}

// LoadRimskyConfigYAML reads rimsky.yml: the unified deployment-shape
// config (stores + named_locks + executors). Per spec §3.1 / §6.6 a
// missing file is a startup error — operators get a clear
// "rimsky config file not found at ..." rather than a silent
// empty-registry fall-through.
func LoadRimskyConfigYAML(path string) (RimskyConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return RimskyConfig{}, fmt.Errorf("rimsky config file not found at %q", path)
		}
		return RimskyConfig{}, fmt.Errorf("read rimsky config %q: %w", path, err)
	}
	expanded := os.ExpandEnv(string(raw))
	var wrapper struct {
		Stores map[string]struct {
			Endpoint     string `yaml:"endpoint"`
			Capabilities struct {
				WriteSemantics string `yaml:"write_semantics"`
			} `yaml:"capabilities"`
		} `yaml:"stores"`
		NamedLocks map[string]store.NamedLockConfig `yaml:"named_locks"`
		Executors  map[string]struct {
			Transport string `yaml:"transport"`
			Endpoint  string `yaml:"endpoint"`
			TLS       string `yaml:"tls"`
		} `yaml:"executors"`
	}
	if err := yaml.Unmarshal([]byte(expanded), &wrapper); err != nil {
		return RimskyConfig{}, fmt.Errorf("parse rimsky config %q: %w", path, err)
	}
	stores := RemoteStoresConfig{Stores: make(map[string]StoreEntry, len(wrapper.Stores))}
	for name, e := range wrapper.Stores {
		stores.Stores[name] = StoreEntry{
			Endpoint: e.Endpoint,
			Capabilities: store.Capabilities{
				WriteSemantics: store.WriteSemantics(e.Capabilities.WriteSemantics),
			},
		}
	}
	executors := ExecutorsConfig{Executors: make(map[string]ExecutorEntry, len(wrapper.Executors))}
	for name, e := range wrapper.Executors {
		executors.Executors[name] = ExecutorEntry{
			Transport: e.Transport,
			Endpoint:  e.Endpoint,
			TLS:       e.TLS,
		}
	}
	return RimskyConfig{
		Stores:     stores,
		NamedLocks: store.NamedLocksConfig{Locks: wrapper.NamedLocks},
		Executors:  executors,
	}, nil
}

// dialRemoteStores walks each entry, dials the gRPC endpoint, runs the
// Capabilities() handshake under a bounded timeout, validates strict
// equality against the operator's declared block, and registers the
// resulting Client in reg. On any failure (unreachable, mismatch,
// timeout), already-dialed clients are closed and the error is
// returned for the caller to propagate as a startup failure.
//
// Returns a non-nil *Registry even on the empty-config path (matches
// the supervisor's contract that a non-nil StoreRegistry is required).
func dialRemoteStores(ctx context.Context, cfg RemoteStoresConfig) (*store.Registry, error) {
	reg := store.NewRegistry()
	for name, entry := range cfg.Stores {
		if err := validateStoreEntry(name, entry); err != nil {
			reg.Close()
			return nil, fmt.Errorf("dialRemoteStores: %w", err)
		}
		dialCtx, cancel := context.WithTimeout(ctx, capabilitiesHandshakeTimeout)
		client, err := remote.Dial(dialCtx, name, entry.Endpoint)
		cancel()
		if err != nil {
			reg.Close()
			return nil, fmt.Errorf("dialRemoteStores: store %q: %w", name, err)
		}
		if err := client.ValidateCapabilities(entry.Capabilities); err != nil {
			client.Close()
			reg.Close()
			return nil, fmt.Errorf("dialRemoteStores: %w", err)
		}
		reg.Add(name, client)
	}
	return reg, nil
}

// validateStoreEntry rejects malformed endpoint URLs at startup so
// operators get a clear error rather than a downstream gRPC dial
// failure. Acceptable: empty scheme (host:port form) or "grpc://"
// prefix; anything else (http://, https://, …) is rejected with a
// pointer to the expected form.
func validateStoreEntry(name string, entry StoreEntry) error {
	if entry.Endpoint == "" {
		return fmt.Errorf("store %q: endpoint is required", name)
	}
	for _, badScheme := range []string{"http://", "https://", "tcp://", "unix://"} {
		if len(entry.Endpoint) >= len(badScheme) && entry.Endpoint[:len(badScheme)] == badScheme {
			return fmt.Errorf("store %q: endpoint scheme must be grpc:// (got %s)", name, badScheme)
		}
	}
	return nil
}
