// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: sensor
package sensorauth

import (
	"errors"
	"fmt"
	"net/http"
)

const (
	ModeHMAC         = "hmac"
	ModeSecretHeader = "secret_header"
	ModeNone         = "none"
)

const (
	DefaultSignatureHeader     = "X-Rimsky-Signature"
	DefaultReplayWindowSeconds = 300
)

type AuthConfig struct {
	Mode                string `json:"mode"`
	Secret              string `json:"secret,omitempty"`
	SignatureHeader     string `json:"signature_header,omitempty"`
	TimestampHeader     string `json:"timestamp_header,omitempty"`
	ReplayWindowSeconds int    `json:"replay_window_seconds,omitempty"`
	Header              string `json:"header,omitempty"`
}

// @decision: webhook-auth-required
func ValidateInbound(auth *AuthConfig) error {
	if auth == nil {
		return errors.New("resolved_config.auth required (set mode to hmac, secret_header, or none)")
	}
	switch auth.Mode {
	case ModeNone:
		return nil
	case ModeHMAC:
		if auth.Secret == "" {
			return errors.New("resolved_config.auth.secret required for hmac mode")
		}
		if auth.SignatureHeader == "" {
			auth.SignatureHeader = DefaultSignatureHeader
		}
		if auth.TimestampHeader == "" {
			return errors.New("resolved_config.auth.timestamp_header required for hmac mode (replay protection is mandatory: the timestamp is part of the signed material)")
		}
		if auth.ReplayWindowSeconds < 0 {
			return errors.New("resolved_config.auth.replay_window_seconds must not be negative")
		}
		return nil
	case ModeSecretHeader:
		if auth.Header == "" {
			return errors.New("resolved_config.auth.header required for secret_header mode")
		}
		if auth.Secret == "" {
			return errors.New("resolved_config.auth.secret required for secret_header mode")
		}
		return nil
	case "":
		return errors.New("resolved_config.auth.mode required (hmac, secret_header, or none)")
	default:
		return fmt.Errorf("resolved_config.auth.mode %q invalid (want hmac, secret_header, or none)", auth.Mode)
	}
}

// @decision: http-poll-sensor-auth-outbound
func ValidateOutbound(auth *AuthConfig) error {
	if auth == nil {
		return nil
	}
	switch auth.Mode {
	case ModeNone:
		return nil
	case ModeSecretHeader:
		if auth.Header == "" {
			return errors.New("resolved_config.auth.header required for secret_header mode")
		}
		if auth.Secret == "" {
			return errors.New("resolved_config.auth.secret required for secret_header mode")
		}
		return nil
	case ModeHMAC:
		return errors.New("resolved_config.auth.mode \"hmac\" does not apply to an outbound poll, which is a GET with no body to sign (want secret_header or none)")
	case "":
		return errors.New("resolved_config.auth.mode required when an auth block is present (secret_header or none)")
	default:
		return fmt.Errorf("resolved_config.auth.mode %q invalid on an outbound poll (want secret_header or none)", auth.Mode)
	}
}

// @decision: http-poll-sensor-auth-outbound
func ApplyOutbound(auth *AuthConfig, req *http.Request) {
	if auth == nil || auth.Mode != ModeSecretHeader {
		return
	}
	req.Header.Set(auth.Header, auth.Secret)
}
