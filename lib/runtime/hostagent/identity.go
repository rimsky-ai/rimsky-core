// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: host-agent
// @concept: anonymous-mode

package hostagent

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/sillyname"
)

type PersistedIdentity struct {
	RoutingIdentity string `json:"routing_identity"`
}

func DefaultIdentityFile() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		home, herr := os.UserHomeDir()
		if herr != nil {
			return "", fmt.Errorf("hostagent: cannot determine identity file path: %w", err)
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "rimsky", "host-agent", "identity.json"), nil
}

func LoadIdentity(path string) (PersistedIdentity, error) {
	if path == "" {
		return PersistedIdentity{}, nil
	}
	body, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return PersistedIdentity{}, nil
		}
		return PersistedIdentity{}, fmt.Errorf("hostagent: read identity file %s: %w", path, err)
	}
	var id PersistedIdentity
	if len(body) == 0 {
		return PersistedIdentity{}, nil
	}
	if err := json.Unmarshal(body, &id); err != nil {
		return PersistedIdentity{}, fmt.Errorf("hostagent: parse identity file %s: %w", path, err)
	}
	return id, nil
}

func EnsureIdentity(path string) (PersistedIdentity, error) {
	id, err := LoadIdentity(path)
	if err != nil {
		return PersistedIdentity{}, err
	}
	if id.RoutingIdentity != "" {
		return id, nil
	}
	id.RoutingIdentity = sillyname.Generate()
	if err := SaveIdentity(path, id); err != nil {
		return PersistedIdentity{}, err
	}
	return id, nil
}

func SaveIdentity(path string, id PersistedIdentity) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("hostagent: create identity dir: %w", err)
	}
	body, err := json.Marshal(id)
	if err != nil {
		return fmt.Errorf("hostagent: marshal identity: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return fmt.Errorf("hostagent: write identity: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("hostagent: rename identity: %w", err)
	}
	return nil
}
