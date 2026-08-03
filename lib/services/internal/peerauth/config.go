// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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
	EnvControlAPICA  = enroll.EnvControlAPICA
)

const DefaultRenewCheckInterval = time.Minute

type Config struct {
	Mode             string
	ControlAPIURL    string
	APIKey           string
	Label            string
	ControlAPICARoot string
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
		Mode:             mode,
		ControlAPIURL:    strings.TrimSpace(controlURL),
		APIKey:           strings.TrimSpace(os.Getenv(EnvAPIKey)),
		Label:            label,
		ControlAPICARoot: strings.TrimSpace(os.Getenv(EnvControlAPICA)),
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
		var err error
		httpClient, err = enrollHTTPClient(cfg)
		if err != nil {
			return nil, err
		}
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

const enrollHTTPTimeout = 30 * time.Second

// @concept: peer-auth
func enrollHTTPClient(cfg Config) (*http.Client, error) {
	if !strings.HasPrefix(cfg.ControlAPIURL, "https://") {
		if cfg.ControlAPICARoot != "" {
			return nil, fmt.Errorf("peerauth: %s is set but %s=%q is not https — a pinned CA root cannot secure a plaintext enrollment hop",
				EnvControlAPICA, EnvControlAPIURL, cfg.ControlAPIURL)
		}
		return &http.Client{Timeout: enrollHTTPTimeout}, nil
	}
	if cfg.ControlAPICARoot == "" {
		return nil, fmt.Errorf("peerauth: %s=%q is https under %s=%s, so %s must name the deployment CA root PEM the control API's certificate is verified against — the api-key crosses this hop and an unverified server would receive it",
			EnvControlAPIURL, cfg.ControlAPIURL, EnvPeerAuth, enroll.PeerAuthMTLS, EnvControlAPICA)
	}
	pool, err := enroll.CAPoolFromFile(cfg.ControlAPICARoot)
	if err != nil {
		return nil, fmt.Errorf("peerauth: %s: %w", EnvControlAPICA, err)
	}
	return &http.Client{
		Timeout:   enrollHTTPTimeout,
		Transport: &http.Transport{TLSClientConfig: enroll.PinnedTLSConfig(pool)},
	}, nil
}

func LoadFromEnv(ctx context.Context, label string) (*Identity, error) {
	cfg, err := LoadConfigFromEnv(label)
	if err != nil {
		return nil, err
	}
	return Load(ctx, cfg, nil, time.Now)
}
