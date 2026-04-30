// Stores config + remote-dialing helpers for the three rimsky processes.
// Per spec docs/specs/2026-04-27-stores-redesign-v3-design.md §6.
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

// StoreEntry is the per-store config from stores.yml: an endpoint to
// dial plus the operator-declared capability requirements that the
// store-service must advertise back at the Capabilities() handshake.
type StoreEntry struct {
	Endpoint     string
	Capabilities store.Capabilities
}

// RemoteStoresConfig is the parsed `stores:` block from stores.yml.
// Keys are operator-chosen store names; values carry the endpoint URL
// and declared capabilities. Per spec §6.1 — no `kind`, no
// `connection`, no `pick_policies`. Substrate-specific keys live in
// the store-service's own config.
type RemoteStoresConfig struct {
	Stores map[string]StoreEntry
}

// LoadStoresConfigYAML reads stores.yml under the v3 schema (per spec
// §6.1). The YAML shape is `stores: name → {endpoint, capabilities}`
// plus `named_locks: name → {limit}`. Returns the parsed pair.
//
// A missing file is treated as an error so operators get a clear
// "stores config file not found at ..." at startup; an empty registry
// is rarely intentional and silent fall-through made misconfiguration
// hard to spot.
func LoadStoresConfigYAML(path string) (RemoteStoresConfig, store.NamedLocksConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return RemoteStoresConfig{}, store.NamedLocksConfig{}, fmt.Errorf("stores config file not found at %q", path)
		}
		return RemoteStoresConfig{}, store.NamedLocksConfig{}, fmt.Errorf("read stores config %q: %w", path, err)
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
	}
	if err := yaml.Unmarshal([]byte(expanded), &wrapper); err != nil {
		return RemoteStoresConfig{}, store.NamedLocksConfig{}, fmt.Errorf("parse stores config %q: %w", path, err)
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
	return stores, store.NamedLocksConfig{Locks: wrapper.NamedLocks}, nil
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
