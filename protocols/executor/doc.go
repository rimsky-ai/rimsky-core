// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// Package executor defines the Executor service protocol.
//
// An Executor is a service that runs node bodies dispatched by Rimsky's
// supervisor. The protocol surface is one streaming RPC (Execute) plus
// an HTTP+JSON bridge for executors that prefer not to speak gRPC. See
// docs/specs/2026-05-04-service-protocol-contract.md §4 for the
// authoritative spec.
//
// This package carries the lightweight Go-level interface and value
// types that mirror the proto messages. The supervisor's gRPC + HTTP
// client wrapper lives in modeling/executor/ (it caches connections
// and provides a transport-agnostic stream abstraction).
package executor
