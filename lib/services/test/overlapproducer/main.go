// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Command overlapproducer is a test-only ClaimProducer that advertises a
// NON-TRIVIAL ScopesConflict predicate (prefix-containment) plus
// SplitScope, so the S-claimproducer-scopesconflict-wired scenario can
// drive a real rimsky stack against a producer whose overlapping scopes
// are NOT byte-equal. The integration harness builds it on demand
// (testcontainers FromDockerfile) and registers it as a peer
// claim-producer on the shared docker network — a stable in-network
// endpoint that is up BEFORE rimsky boots, so rimsky's eager startup
// Capabilities handshake reaches it with no host-port-tunnel race.
//
// It is never published as a product image.
//
// The overlap predicate is prefix-containment over the selector strings:
// two claim-scopes conflict when either's selector string is a prefix of
// the other. So `tenant/a` and `tenant/a/x` overlap (the parent prefix
// `tenant/a` is a prefix of the child `tenant/a/x`), even though their
// byte-encodings are NOT byte-equal — the exact shape rimsky's
// byte-equal-only conflict check cannot detect.
package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"os"
	"strings"

	"google.golang.org/grpc"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// defaultBind is the gRPC listen address when OVERLAP_PRODUCER_BIND is
// unset. The harness exposes/declares this port.
const defaultBind = "0.0.0.0:9400"

// overlapProducer is a ClaimProducer whose ScopesConflict returns true
// when either scope's selector is a prefix of the other (prefix-
// containment). It also implements SplitScope: a partition request of
// `{"partition_keys":[...]}` yields one sub-scope per key, where the keys
// are chosen so two sibling sub-scopes overlap by the prefix predicate
// (e.g. keys `a` and `a/x` → selectors `tenant/a` and `tenant/a/x`).
//
// Open returns an EMPTY ClaimScope so the rimsky acquisition path keeps
// the canonical `json.Marshal(selector)` scope bytes (it only overwrites
// claim_scope_data when the producer returns a non-empty ClaimScope).
// That keeps the candidate scope and every persisted holder's
// claim_scope_data in ONE encoding (JSON-quoted selector string), so
// ScopesConflict compares like with like.
type overlapProducer struct {
	genv1.UnimplementedClaimProducerServer
}

// overlapCaps advertises sync write-semantics plus BOTH optional
// capabilities the scenario exercises: ScopesConflict (top-level
// acquisition overlap) and SplitScope (fan-out sub-claim overlap).
func overlapCaps() *genv1.CapabilitiesResponse {
	return &genv1.CapabilitiesResponse{
		WriteSemanticsAllowed: []genv1.WriteSemantics{
			genv1.WriteSemantics_WRITE_SEMANTICS_SYNC,
		},
		SupportsScopesConflict: true,
		SupportsSplitScope:     true,
	}
}

func (p *overlapProducer) Capabilities(_ context.Context, _ *genv1.CapabilitiesRequest) (*genv1.CapabilitiesResponse, error) {
	return overlapCaps(), nil
}

// Open acquires unconditionally — the producer never serializes; the
// single-writer-per-overlapping-scope discipline (invariant 4b) is
// rimsky's job via the producer's ScopesConflict predicate, which is
// exactly what the scenario proves rimsky consults.
//
// Address is the claim id encoded as a JSON string: rimsky persists the
// returned address bytes verbatim into the JSONB `address` column, so the
// bytes MUST be valid JSON (a bare claim-id string is rejected with
// `invalid input syntax for type json`). ClaimScope is left EMPTY so
// rimsky keeps its canonical json.Marshal(selector) scope bytes.
func (p *overlapProducer) Open(_ context.Context, req *genv1.OpenRequest) (*genv1.OpenResponse, error) {
	addr, _ := json.Marshal(req.GetClaimId())
	return &genv1.OpenResponse{
		Result: &genv1.OpenResponse_Acquired{
			Acquired: &genv1.Acquired{
				Address:                addr,
				ClaimScope:             nil,
				RealizedWriteSemantics: genv1.WriteSemantics_WRITE_SEMANTICS_SYNC,
			},
		},
	}, nil
}

func (p *overlapProducer) Commit(_ context.Context, _ *genv1.CommitRequest) (*genv1.CommitResponse, error) {
	return &genv1.CommitResponse{}, nil
}

func (p *overlapProducer) Abandon(_ context.Context, _ *genv1.AbandonRequest) (*genv1.AbandonResponse, error) {
	return &genv1.AbandonResponse{}, nil
}

func (p *overlapProducer) Release(_ context.Context, _ *genv1.ReleaseRequest) (*genv1.ReleaseResponse, error) {
	return &genv1.ReleaseResponse{}, nil
}

// ScopesConflict is the non-trivial overlap predicate: true when either
// selector string is a prefix of the other. The two scope-byte arguments
// are whatever rimsky persisted as claim_scope_data — the canonical
// `json.Marshal(selector)` form (a JSON-quoted string), since Open
// returns an empty ClaimScope. We decode both back to their raw selector
// strings before the prefix check so the comparison is over the operator-
// authored selectors (`tenant/a/x`), not their JSON encodings.
func (p *overlapProducer) ScopesConflict(_ context.Context, req *genv1.ClaimScopesConflictRequest) (*genv1.ScopesConflictResponse, error) {
	a := decodeSelector(req.GetClaimScopeA())
	b := decodeSelector(req.GetClaimScopeB())
	return &genv1.ScopesConflictResponse{Conflicts: prefixOverlap(a, b)}, nil
}

// SplitScope partitions the parent scope into one sub-scope per partition
// key in the request (`{"partition_keys":[...]}`). Each sub-scope's
// claim_scope_data is the canonical json.Marshal of a `tenant/<key>`-
// shaped selector. The fan-out case passes keys chosen so two sibling
// sub-scopes overlap by the prefix predicate (e.g. keys `a` and `a/x` →
// selectors `tenant/a` and `tenant/a/x`, where the parent prefix
// `tenant/a` is a prefix of the child `tenant/a/x`).
func (p *overlapProducer) SplitScope(_ context.Context, req *genv1.SplitScopeRequest) (*genv1.SplitScopeResponse, error) {
	keys := decodePartitionKeys(req.GetPartitionRequest())
	subs := make([]*genv1.SubScopeDescriptor, 0, len(keys))
	for _, k := range keys {
		selector := "tenant/" + k
		scopeBytes, _ := json.Marshal(selector)
		subs = append(subs, &genv1.SubScopeDescriptor{
			ClaimScopeData: scopeBytes,
			PartitionKey:   k,
		})
	}
	return &genv1.SplitScopeResponse{SubScopes: subs}, nil
}

// prefixOverlap reports whether either string is a prefix of the other
// (prefix-containment). Equal strings overlap; disjoint strings do not.
// The scenario picks selectors where one is a strict parent prefix of the
// other (`tenant/a` ⊏ `tenant/a/x`) so the predicate fires on a NON-byte-
// equal pair — the case byte-equal-only conflict detection misses.
func prefixOverlap(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return strings.HasPrefix(a, b) || strings.HasPrefix(b, a)
}

// decodeSelector unwraps a claim_scope_data value back to its raw
// selector string. rimsky persists claim_scope_data as json.Marshal of
// the selector (a JSON-quoted string) when the producer returns an empty
// ClaimScope at Open; we json.Unmarshal it back. A value that is not a
// JSON string is returned verbatim (defensive — the scenario never
// produces that form).
func decodeSelector(raw []byte) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return string(raw)
}

// decodePartitionKeys reads the `{"partition_keys":[...]}` SplitScope
// request shape rimsky forwards verbatim. A request that does not parse
// yields no keys (the fan-out then splits to zero sub-scopes — a visible
// failure, never a silent partial).
func decodePartitionKeys(raw []byte) []string {
	var req struct {
		PartitionKeys []string `json:"partition_keys"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil
	}
	return req.PartitionKeys
}

func main() {
	bind := os.Getenv("OVERLAP_PRODUCER_BIND")
	if bind == "" {
		bind = defaultBind
	}
	lis, err := net.Listen("tcp", bind)
	if err != nil {
		slog.Error("overlapproducer: listen", "bind", bind, "error", err)
		os.Exit(1)
	}
	srv := grpc.NewServer()
	genv1.RegisterClaimProducerServer(srv, &overlapProducer{})
	slog.Info("overlapproducer: serving", "bind", bind)
	if err := srv.Serve(lis); err != nil {
		slog.Error("overlapproducer: serve", "error", err)
		os.Exit(1)
	}
}
