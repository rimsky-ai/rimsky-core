// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// Package claimproducer defines the ClaimProducer service protocol.
//
// A ClaimProducer is a service that produces claim handles for
// Rimsky's lock manager. The protocol surface is four runtime verbs
// (Open, Commit, Abandon, Release) plus one startup handshake
// (Capabilities). See docs/specs/2026-05-04-service-protocol-contract.md
// for the authoritative spec.
package claimproducer
