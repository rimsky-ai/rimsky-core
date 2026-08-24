// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @decision: host-daemon-proxy-enrollment
// @concept: host-daemon-proxy
package main

import (
	"net/http"

	"google.golang.org/grpc"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/serviceauth"
)

type proxyServers struct {
	daemon  *grpc.Server
	service *grpc.Server
}

func buildProxyServers(
	cfg Config, state *proxyState, identity *serviceauth.Identity,
	controlAPIClient *http.Client, daemonCreds []grpc.ServerOption,
) *proxyServers {
	daemonSrv := grpc.NewServer(daemonCreds...)
	verifyIdentity := newControlAPIRegisterIdentityVerifier(controlAPIClient, cfg.ControlAPIURL)
	genv1.RegisterHostDaemonServer(daemonSrv, newDaemonServer(state, verifyIdentity))

	// @decision: host-daemon-proxy-tls
	// @concept: service-auth
	serviceSrv := grpc.NewServer(identity.GRPCServerOptions()...)
	registerServiceProtocols(serviceSrv, state, cfg, controlAPIClient)
	return &proxyServers{daemon: daemonSrv, service: serviceSrv}
}

func registerServiceProtocols(srv *grpc.Server, state *proxyState, cfg Config, controlAPIClient *http.Client) {
	genv1.RegisterExecutorServer(srv, newExecutorHandler(state, cfg, controlAPIClient))
	genv1.RegisterExecutorObservabilityServer(srv, newExecutorObsHandler())
	genv1.RegisterClaimProducerServer(srv, newClaimProducerHandler(state, cfg, controlAPIClient))
	genv1.RegisterClaimProducerObservabilityServer(srv, newClaimProducerObsHandler())
	genv1.RegisterLifecycleSubscriberServer(srv, newLifecycleHandler(state, cfg))
}
