// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: host-agent-proxy
// @concept: api-key

package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type registerIdentityVerifier func(ctx context.Context, presentedAPIKey string) (routingIdentity string, err error)

const whoamiPath = "/v1/auth/whoami"

const whoamiKindAnonymous = "anonymous"

type whoamiResponse struct {
	Kind  string `json:"kind"`
	KeyID string `json:"key_id"`
}

func newControlAPIRegisterIdentityVerifier(client *http.Client, baseURL string) registerIdentityVerifier {
	return func(ctx context.Context, presentedAPIKey string) (string, error) {
		if baseURL == "" {
			return "", status.Error(codes.FailedPrecondition,
				"proxy cannot verify Register.api_key without RIMSKY_CONTROL_API_URL; refusing registration")
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+whoamiPath, nil)
		if err != nil {
			return "", status.Errorf(codes.Internal, "build control-api whoami request: %v", err)
		}
		if presentedAPIKey != anonymousRoutingIdentity {
			req.Header.Set("Authorization", "Bearer "+presentedAPIKey)
		}
		resp, err := client.Do(req)
		if err != nil {
			return "", status.Errorf(codes.Unavailable, "control-api whoami: %v", err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", status.Errorf(codes.Unavailable, "control-api whoami read: %v", err)
		}
		if resp.StatusCode == http.StatusUnauthorized {
			return "", status.Error(codes.Unauthenticated, "Register.api_key rejected by control-api")
		}
		if resp.StatusCode != http.StatusOK {
			return "", status.Errorf(codes.Unavailable, "control-api whoami: status %d", resp.StatusCode)
		}
		var who whoamiResponse
		if err := json.Unmarshal(body, &who); err != nil {
			return "", status.Errorf(codes.Unavailable, "control-api whoami decode: %v", err)
		}
		if who.KeyID != "" {
			return who.KeyID, nil
		}
		if who.Kind == whoamiKindAnonymous {
			return anonymousRoutingIdentity, nil
		}
		return "", status.Error(codes.Unauthenticated, "control-api whoami reported no routable identity")
	}
}
