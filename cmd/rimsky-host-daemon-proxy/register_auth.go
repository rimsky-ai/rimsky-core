// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: host-daemon-proxy
// @concept: api-key

package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/auth"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/sillyname"
)

type registerIdentityKind int

const (
	registerIdentityAPIKey registerIdentityKind = iota
	registerIdentityAnonymous
)

type registerIdentityVerdict struct {
	kind  registerIdentityKind
	keyID string
}

type registerIdentityVerifier func(ctx context.Context, presentedAPIKey string) (registerIdentityVerdict, error)

const whoamiPath = "/v1/auth/whoami"

type whoamiResponse struct {
	Kind  string `json:"kind"`
	KeyID string `json:"key_id"`
}

func newControlAPIRegisterIdentityVerifier(client *http.Client, baseURL string) registerIdentityVerifier {
	return func(ctx context.Context, presentedAPIKey string) (registerIdentityVerdict, error) {
		if baseURL == "" {
			return registerIdentityVerdict{}, status.Error(codes.FailedPrecondition,
				"proxy cannot verify Register.api_key without RIMSKY_CONTROL_API_URL; refusing registration")
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+whoamiPath, nil)
		if err != nil {
			return registerIdentityVerdict{}, status.Errorf(codes.Internal, "build control-api whoami request: %v", err)
		}
		if presentedAPIKey != sillyname.AnonymousCredentialSentinel {
			req.Header.Set("Authorization", "Bearer "+presentedAPIKey)
		}
		resp, err := client.Do(req)
		if err != nil {
			return registerIdentityVerdict{}, status.Errorf(codes.Unavailable, "control-api whoami: %v", err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return registerIdentityVerdict{}, status.Errorf(codes.Unavailable, "control-api whoami read: %v", err)
		}
		if resp.StatusCode == http.StatusUnauthorized {
			return registerIdentityVerdict{}, status.Error(codes.Unauthenticated, "Register.api_key rejected by control-api")
		}
		if resp.StatusCode != http.StatusOK {
			return registerIdentityVerdict{}, status.Errorf(codes.Unavailable, "control-api whoami: status %d", resp.StatusCode)
		}
		var who whoamiResponse
		if err := json.Unmarshal(body, &who); err != nil {
			return registerIdentityVerdict{}, status.Errorf(codes.Unavailable, "control-api whoami decode: %v", err)
		}
		if who.KeyID != "" {
			return registerIdentityVerdict{kind: registerIdentityAPIKey, keyID: who.KeyID}, nil
		}
		if who.Kind == string(auth.IdentityAnonymous) {
			return registerIdentityVerdict{kind: registerIdentityAnonymous}, nil
		}
		return registerIdentityVerdict{}, status.Error(codes.Unauthenticated, "control-api whoami reported no routable identity")
	}
}
