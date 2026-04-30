// Package remote is the gRPC client that satisfies the rimsky-side
// store.Store interface by translating each verb to a wire RPC against
// a standard store-service binary.
//
// Per spec docs/specs/2026-04-27-stores-redesign-v3-design.md §5 (wire
// format) and §3.1 (protocol-only). This is the only concrete Store
// implementation that ships in the rimsky module — every store-service
// runs in its own process and rimsky reaches it via this client.
package remote
