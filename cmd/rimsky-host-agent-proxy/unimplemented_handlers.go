// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// unimplemented_handlers.go — registered-but-UNIMPLEMENTED supervisor-
// facing protocols. The proxy registers Publisher, Validation, and
// DataProcessing so a supervisor that dials any of them gets a clean
// codes.Unimplemented (rather than an unimplemented-service error),
// reserving the surface for the future generalization where the proxy
// fronts dev-machine bindings for these protocols too. Each handler
// embeds its generated Unimplemented*Server, whose methods already return
// codes.Unimplemented.
//
// @concept: host-agent-proxy

package main

import (
	genv1 "github.com/fallguyconsulting/rimsky/protocols/proto/v1/gen"
)

type unimplementedPublisher struct {
	genv1.UnimplementedPublisherServer
}

func newUnimplementedPublisher() *unimplementedPublisher { return &unimplementedPublisher{} }

type unimplementedValidation struct {
	genv1.UnimplementedValidationServer
}

func newUnimplementedValidation() *unimplementedValidation { return &unimplementedValidation{} }

type unimplementedDataProcessing struct {
	genv1.UnimplementedDataProcessingServer
}

func newUnimplementedDataProcessing() *unimplementedDataProcessing {
	return &unimplementedDataProcessing{}
}
