// Package claimproducer defines the ClaimProducer service protocol.
//
// A ClaimProducer is a service that produces claim handles for
// Rimsky's lock manager. The protocol surface is four runtime verbs
// (Open, Commit, Abandon, Release) plus one startup handshake
// (Capabilities). See docs/specs/2026-05-04-service-protocol-contract.md
// for the authoritative spec.
package claimproducer
