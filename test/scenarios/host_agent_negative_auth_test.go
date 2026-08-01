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

func registerWithProxy(t *testing.T, proxyAddr, apiKey string) (*genv1.RegisterAck, error) {
	t.Helper()
	conn, err := grpc.NewClient(proxyAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	stream, err := genv1.NewHostAgentClient(conn).Connect(ctx)
	require.NoError(t, err)
	require.NoError(t, stream.Send(&genv1.ClientFrame{Body: &genv1.ClientFrame_Register{Register: &genv1.Register{
		ApiKey:               apiKey,
		AgentLabel:           "negative-auth-probe",
		LocalCallbackBaseUrl: "http://127.0.0.1:1",
	}}}))

	frame, recvErr := stream.Recv()
	if recvErr != nil {
		return nil, recvErr
	}
	return frame.GetRegisterAck(), nil
}

func TestHostAgentProxyRejectsUnknownAPIKey(t *testing.T) {
	fx := newHostAgentFixture(t, fixtureOpts{})

	ack, err := registerWithProxy(t, fx.proxyAddr, "rk_bogus-not-a-real-owner-key")
	require.Nil(t, ack, "an unknown api-key must not receive a RegisterAck")
	require.Error(t, err, "an unknown api-key must be refused registration")
	require.Equal(t, codes.Unauthenticated, status.Code(err),
		"the proxy must reject an unverifiable api-key with Unauthenticated so the agent is never routable")

	ack, err = registerWithProxy(t, fx.proxyAddr, fx.adminKey)
	require.NoError(t, err, "the minted owner key is a valid identity and must register")
	require.NotNil(t, ack, "a verified api-key must receive a RegisterAck")
}
