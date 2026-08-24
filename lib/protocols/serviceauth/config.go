// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

package serviceauth

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
	EnvServiceAuth   = enroll.EnvServiceAuth
	EnvControlAPIURL = enroll.EnvControlAPIURL
	EnvAPIKey        = enroll.EnvAPIKey
	EnvControlAPICA  = enroll.EnvControlAPICA
)

// @concept: service-auth
const EnvAllowPlaintextEnrollment = "RIMSKY_ALLOW_PLAINTEXT_ENROLLMENT"

const DefaultRenewCheckInterval = time.Minute

type Config struct {
	Mode             string
	ControlAPIURL    string
	APIKey           string
	Label            string
	ControlAPICARoot string

	// @concept: service-auth
	AllowPlaintextEnrollment bool
}

func LoadConfigFromEnv(label string) (Config, error) {
	mode := os.Getenv(EnvServiceAuth)
	if mode == "" {
		mode = enroll.ServiceAuthNone
	}
	if mode != enroll.ServiceAuthNone && mode != enroll.ServiceAuthMTLS {
		return Config{}, fmt.Errorf("serviceauth: %s=%q is invalid (want %q or %q)", EnvServiceAuth, mode, enroll.ServiceAuthNone, enroll.ServiceAuthMTLS)
	}
	controlURL := os.Getenv(EnvControlAPIURL)
	return Config{
		Mode:             mode,
		ControlAPIURL:    strings.TrimSpace(controlURL),
		APIKey:           strings.TrimSpace(os.Getenv(EnvAPIKey)),
		Label:            label,
		ControlAPICARoot: strings.TrimSpace(os.Getenv(EnvControlAPICA)),
		// @concept: service-auth
		AllowPlaintextEnrollment: plaintextEnrollmentAllowed(),
	}, nil
}

// @concept: service-auth
func plaintextEnrollmentAllowed() bool {
	switch strings.TrimSpace(os.Getenv(EnvAllowPlaintextEnrollment)) {
	case "", "0", "false", "no":
		return false
	default:
		return true
	}
}

func Load(ctx context.Context, cfg Config, httpClient *http.Client, now func() time.Time) (*Identity, error) {
	if now == nil {
		now = time.Now
	}
	if cfg.Mode == enroll.ServiceAuthNone {
		return &Identity{mode: enroll.ServiceAuthNone, now: now}, nil
	}
	if cfg.ControlAPIURL == "" || cfg.APIKey == "" {
		return nil, fmt.Errorf("serviceauth: %s=%s requires %s and %s to be set", EnvServiceAuth, enroll.ServiceAuthMTLS, EnvControlAPIURL, EnvAPIKey)
	}
	if httpClient == nil {
		var err error
		httpClient, err = enrollHTTPClient(cfg)
		if err != nil {
			return nil, err
		}
	}
	id := &Identity{
		mode: enroll.ServiceAuthMTLS,
		now:  now,
		enroll: func(ctx context.Context) (enroll.Response, error) {
			return enroll.Enroll(ctx, httpClient, cfg.ControlAPIURL, cfg.APIKey, cfg.Label)
		},
	}
	if err := id.refresh(ctx); err != nil {
		return nil, fmt.Errorf("serviceauth: initial enroll for %q failed (fail-closed): %w", cfg.Label, err)
	}
	return id, nil
}

const enrollHTTPTimeout = 30 * time.Second

// @concept: service-auth
func enrollHTTPClient(cfg Config) (*http.Client, error) {
	if !strings.HasPrefix(cfg.ControlAPIURL, "https://") {
		if cfg.ControlAPICARoot != "" {
			return nil, fmt.Errorf("serviceauth: %s is set but %s=%q is not https — a pinned CA root cannot secure a plaintext enrollment hop",
				EnvControlAPICA, EnvControlAPIURL, cfg.ControlAPIURL)
		}
		if cfg.AllowPlaintextEnrollment {
			return &http.Client{Timeout: enrollHTTPTimeout}, nil
		}
		return nil, fmt.Errorf("serviceauth: %s=%q is not https under %s=%s — the api-key travels this hop in the clear and the server is unverified, so enrollment is refused; use an https control-API URL with %s naming the deployment CA root, or set %s=1 to accept the exposure on a trusted local network",
			EnvControlAPIURL, cfg.ControlAPIURL, EnvServiceAuth, enroll.ServiceAuthMTLS, EnvControlAPICA, EnvAllowPlaintextEnrollment)
	}
	if cfg.ControlAPICARoot == "" {
		return nil, fmt.Errorf("serviceauth: %s=%q is https under %s=%s, so %s must name the deployment CA root PEM the control API's certificate is verified against — the api-key crosses this hop and an unverified server would receive it",
			EnvControlAPIURL, cfg.ControlAPIURL, EnvServiceAuth, enroll.ServiceAuthMTLS, EnvControlAPICA)
	}
	pool, err := enroll.CAPoolFromFile(cfg.ControlAPICARoot)
	if err != nil {
		return nil, fmt.Errorf("serviceauth: %s: %w", EnvControlAPICA, err)
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
