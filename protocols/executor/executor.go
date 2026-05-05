// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package executor

import "context"

// Executor is the Go-level interface the protocol surface presents.
// Implementations are typically full processes binding a gRPC server;
// the in-process Go interface is useful for test fixtures and the
// HTTP+JSON bridge.
//
// The Execute call returns a stream that yields zero or more Heartbeat
// events followed by exactly one terminal event (Complete, Blocked,
// Errored, or AsyncAccepted). Closing the stream without a terminal
// event is treated by the supervisor as an infrastructure error.
type Executor interface {
	Execute(ctx context.Context, req ExecuteRequest) (Stream, error)
}

// Stream abstracts the bidirectional event channel. Recv yields the
// next event or io.EOF on clean close; Close releases resources.
type Stream interface {
	Recv() (ExecuteEvent, error)
	Close() error
}
