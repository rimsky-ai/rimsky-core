// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @decision: host-agent-proxy-enrollment
// @concept: host-agent-proxy
package main

import (
	"net/http"

	"google.golang.org/grpc"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/peerauth"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

type proxyServers struct {
	agent *grpc.Server
	peer  *grpc.Server
}

func buildProxyServers(
	cfg Config, state *proxyState, identity *peerauth.Identity,
	controlAPIClient *http.Client, agentCreds []grpc.ServerOption,
) *proxyServers {
	agentSrv := grpc.NewServer(agentCreds...)
	verifyIdentity := newControlAPIRegisterIdentityVerifier(controlAPIClient, cfg.ControlAPIURL)
	genv1.RegisterHostAgentServer(agentSrv, newAgentServer(state, verifyIdentity))

	// @decision: host-agent-proxy-tls
	// @concept: peer-auth
	peerSrv := grpc.NewServer(identity.GRPCServerOptions()...)
	registerPeerProtocols(peerSrv, state, cfg, controlAPIClient)
	return &proxyServers{agent: agentSrv, peer: peerSrv}
}

func registerPeerProtocols(srv *grpc.Server, state *proxyState, cfg Config, controlAPIClient *http.Client) {
	genv1.RegisterExecutorServer(srv, newExecutorHandler(state, cfg, controlAPIClient))
	genv1.RegisterExecutorObservabilityServer(srv, newExecutorObsHandler())
	genv1.RegisterClaimProducerServer(srv, newClaimProducerHandler(state, cfg, controlAPIClient))
	genv1.RegisterClaimProducerObservabilityServer(srv, newClaimProducerObsHandler())
	genv1.RegisterLifecycleSubscriberServer(srv, newLifecycleHandler(state, cfg))
}
