// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package peerauth

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/enroll"
)

const (
	EnvPeerAuth      = enroll.EnvPeerAuth
	EnvControlAPIURL = enroll.EnvControlAPIURL
	EnvAPIKey        = enroll.EnvAPIKey
)

const DefaultRenewCheckInterval = time.Minute

type Config struct {
	Mode          string
	ControlAPIURL string
	APIKey        string
	Label         string
}

func LoadConfigFromEnv(label string) (Config, error) {
	mode := os.Getenv(EnvPeerAuth)
	if mode == "" {
		mode = enroll.PeerAuthNone
	}
	if mode != enroll.PeerAuthNone && mode != enroll.PeerAuthMTLS {
		return Config{}, fmt.Errorf("peerauth: %s=%q is invalid (want %q or %q)", EnvPeerAuth, mode, enroll.PeerAuthNone, enroll.PeerAuthMTLS)
	}
	controlURL := os.Getenv(EnvControlAPIURL)
	return Config{
		Mode:          mode,
		ControlAPIURL: strings.TrimSpace(controlURL),
		APIKey:        strings.TrimSpace(os.Getenv(EnvAPIKey)),
		Label:         label,
	}, nil
}

func Load(ctx context.Context, cfg Config, httpClient *http.Client, now func() time.Time) (*Identity, error) {
	if now == nil {
		now = time.Now
	}
	if cfg.Mode == enroll.PeerAuthNone {
		return &Identity{mode: enroll.PeerAuthNone, now: now}, nil
	}
	if cfg.ControlAPIURL == "" || cfg.APIKey == "" {
		return nil, fmt.Errorf("peerauth: %s=%s requires %s and %s to be set", EnvPeerAuth, enroll.PeerAuthMTLS, EnvControlAPIURL, EnvAPIKey)
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	id := &Identity{
		mode: enroll.PeerAuthMTLS,
		now:  now,
		enroll: func(ctx context.Context) (enroll.Response, error) {
			return enroll.Enroll(ctx, httpClient, cfg.ControlAPIURL, cfg.APIKey, cfg.Label)
		},
	}
	if err := id.refresh(ctx); err != nil {
		return nil, fmt.Errorf("peerauth: initial enroll for %q failed (fail-closed): %w", cfg.Label, err)
	}
	return id, nil
}

func LoadFromEnv(ctx context.Context, label string) (*Identity, error) {
	cfg, err := LoadConfigFromEnv(label)
	if err != nil {
		return nil, err
	}
	return Load(ctx, cfg, nil, time.Now)
}
