// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package scenarios

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

func registerWithProxy(t *testing.T, proxyAddr, caPath, apiKey string) (*genv1.RegisterAck, error) {
	t.Helper()
	conn := dialDaemonFacing(t, proxyAddr, caPath)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	stream, err := genv1.NewHostDaemonClient(conn).Connect(ctx)
	require.NoError(t, err)
	require.NoError(t, stream.Send(&genv1.ClientFrame{Body: &genv1.ClientFrame_Register{Register: &genv1.Register{
		ApiKey:               apiKey,
		DaemonLabel:          "negative-auth-probe",
		LocalCallbackBaseUrl: "http://127.0.0.1:1",
	}}}))

	frame, recvErr := stream.Recv()
	if recvErr != nil {
		return nil, recvErr
	}
	return frame.GetRegisterAck(), nil
}

func TestHostDaemonProxyRejectsUnknownAPIKey(t *testing.T) {
	fx := newHostDaemonFixture(t, fixtureOpts{})

	ack, err := registerWithProxy(t, fx.proxyAddr, fx.proxyCAPath, "rk_bogus-not-a-real-owner-key")
	require.Nil(t, ack, "an unknown api-key must not receive a RegisterAck")
	require.Error(t, err, "an unknown api-key must be refused registration")
	require.Equal(t, codes.Unauthenticated, status.Code(err),
		"the proxy must reject an unverifiable api-key with Unauthenticated so the daemon is never routable")

	ack, err = registerWithProxy(t, fx.proxyAddr, fx.proxyCAPath, fx.adminKey)
	require.NoError(t, err, "the minted owner key is a valid identity and must register")
	require.NotNil(t, ack, "a verified api-key must receive a RegisterAck")
}
