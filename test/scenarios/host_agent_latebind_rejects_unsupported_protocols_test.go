// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package scenarios

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

func TestHostAgentProxyRejectsUnsupportedLateBindProtocols(t *testing.T) {
	fx := newHostAgentFixture(t, fixtureOpts{})

	conn, err := grpc.NewClient(fx.proxyAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err, "dial proxy")
	t.Cleanup(func() { _ = conn.Close() })

	t.Run("validation", func(t *testing.T) {
		_, callErr := genv1.NewValidationClient(conn).Validate(context.Background(), &genv1.ValidateRequest{})
		require.Equal(t, codes.Unimplemented, status.Code(callErr),
			"the proxy's sanctioned late-bind surface is executor and claim-producer; validation must be rejected loudly, not silently accepted or half-forwarded")
	})
	t.Run("publisher", func(t *testing.T) {
		_, callErr := genv1.NewPublisherClient(conn).Subscribe(context.Background(), &genv1.SubscribeRequest{})
		require.Equal(t, codes.Unimplemented, status.Code(callErr),
			"the proxy's sanctioned late-bind surface is executor and claim-producer; publisher must be rejected loudly, not silently accepted or half-forwarded")
	})
	t.Run("data_processing", func(t *testing.T) {
		_, callErr := genv1.NewDataProcessingClient(conn).BeginCandidate(context.Background(), &genv1.BeginCandidateRequest{})
		require.Equal(t, codes.Unimplemented, status.Code(callErr),
			"the proxy's sanctioned late-bind surface is executor and claim-producer; data-processing must be rejected loudly, not silently accepted or half-forwarded")
	})
}
