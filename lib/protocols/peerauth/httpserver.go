// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

package peerauth

import (
	"net"
	"net/http"
	"time"
)

func (id *Identity) OutboundHTTPClient(timeout time.Duration) *http.Client {
	if !id.Enabled() {
		return nil
	}
	return &http.Client{Timeout: timeout, Transport: &http.Transport{TLSClientConfig: id.ClientTLSConfig()}}
}

func (id *Identity) HTTPServer(handler http.Handler) *http.Server {
	srv := &http.Server{Handler: handler}
	if cfg := id.ServerTLSConfig(); cfg != nil {
		srv.TLSConfig = cfg
	}
	return srv
}

func (id *Identity) RunHTTP(srv *http.Server, lis net.Listener) error {
	if id.Enabled() {
		return srv.ServeTLS(lis, "", "")
	}
	return srv.Serve(lis)
}
