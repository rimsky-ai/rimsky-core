// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package remote

import (
	"context"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	genv1 "github.com/fallguyconsulting/rimsky/protocols/proto/v1/gen"
	"github.com/fallguyconsulting/rimsky/runtime/clientiface"
)

// DataProcessingClient is a remote-gRPC implementation of the
// rimsky-side clientiface.DataProcessingClient interface. One client per
// producer that advertises the `data_processing` protocol.
type DataProcessingClient struct {
	name string
	conn *grpc.ClientConn
	rpc  genv1.DataProcessingClient
}

// Compile-time interface check.
var _ clientiface.DataProcessingClient = (*DataProcessingClient)(nil)

// Name returns the operator-configured producer name.
func (c *DataProcessingClient) Name() string { return c.name }

// BeginCandidate RPCs to the remote producer.
func (c *DataProcessingClient) BeginCandidate(ctx context.Context, in clientiface.BeginCandidateInput) (clientiface.BeginCandidateOutput, error) {
	resp, err := c.rpc.BeginCandidate(ctx, &genv1.BeginCandidateRequest{
		ClaimHandleId:      in.ClaimHandleID,
		SubScopeDescriptor: in.SubScopeDescriptor,
		IdempotencyKey:     in.IdempotencyKey,
	})
	if err != nil {
		return clientiface.BeginCandidateOutput{}, fmt.Errorf("remote data_processing %q: BeginCandidate: %w", c.name, err)
	}
	return clientiface.BeginCandidateOutput{CandidateHandle: resp.GetCandidateHandle()}, nil
}

// CommitCandidate RPCs to the remote producer. The proto carries only
// the opaque candidate_handle; the per-version id (when produced)
// flows back inside the producer-supplied CandidateMetadata bytes,
// which rimsky stores opaque per @blessed-invariant 20-class.
func (c *DataProcessingClient) CommitCandidate(ctx context.Context, in clientiface.CommitCandidateInput) (clientiface.CommitCandidateOutput, error) {
	resp, err := c.rpc.CommitCandidate(ctx, &genv1.CommitCandidateRequest{
		CandidateHandle: in.CandidateHandle,
	})
	if err != nil {
		return clientiface.CommitCandidateOutput{}, fmt.Errorf("remote data_processing %q: CommitCandidate: %w", c.name, err)
	}
	return clientiface.CommitCandidateOutput{
		CandidateMetadata: resp.GetCandidateMetadata(),
	}, nil
}

// AbandonCandidate RPCs to the remote producer.
func (c *DataProcessingClient) AbandonCandidate(ctx context.Context, in clientiface.AbandonCandidateInput) error {
	_, err := c.rpc.AbandonCandidate(ctx, &genv1.AbandonCandidateRequest{
		CandidateHandle: in.CandidateHandle,
	})
	if err != nil {
		return fmt.Errorf("remote data_processing %q: AbandonCandidate: %w", c.name, err)
	}
	return nil
}

// ListVersions RPCs to the remote producer.
func (c *DataProcessingClient) ListVersions(ctx context.Context, in clientiface.ListVersionsInput) (clientiface.ListVersionsOutput, error) {
	resp, err := c.rpc.ListVersions(ctx, &genv1.ListVersionsRequest{
		ClaimHandleId: in.ClaimHandleID,
	})
	if err != nil {
		return clientiface.ListVersionsOutput{}, fmt.Errorf("remote data_processing %q: ListVersions: %w", c.name, err)
	}
	out := clientiface.ListVersionsOutput{}
	for _, v := range resp.GetVersions() {
		var ts int64
		if t := v.GetCommittedAt(); t != nil {
			ts = t.GetSeconds()
		}
		out.Versions = append(out.Versions, clientiface.DataProcessingVersion{
			VersionID:        v.GetVersionId(),
			CommittedAtUnixS: ts,
			ProducerMetadata: v.GetProducerMetadata(),
		})
	}
	return out, nil
}

// ListPartitions RPCs to the remote producer.
func (c *DataProcessingClient) ListPartitions(ctx context.Context, in clientiface.ListPartitionsInput) (clientiface.ListPartitionsOutput, error) {
	resp, err := c.rpc.ListPartitions(ctx, &genv1.ListPartitionsRequest{
		ClaimHandleId: in.ClaimHandleID,
		VersionId:     in.VersionID,
	})
	if err != nil {
		return clientiface.ListPartitionsOutput{}, fmt.Errorf("remote data_processing %q: ListPartitions: %w", c.name, err)
	}
	out := clientiface.ListPartitionsOutput{}
	for _, p := range resp.GetPartitions() {
		out.Partitions = append(out.Partitions, clientiface.DataProcessingPartition{
			PartitionKey:      p.GetPartitionKey(),
			PartitionMetadata: p.GetPartitionMetadata(),
		})
	}
	return out, nil
}

// GetVersionSchema RPCs to the remote producer.
func (c *DataProcessingClient) GetVersionSchema(ctx context.Context, in clientiface.GetVersionSchemaInput) (clientiface.GetVersionSchemaOutput, error) {
	resp, err := c.rpc.GetVersionSchema(ctx, &genv1.GetVersionSchemaRequest{
		ClaimHandleId: in.ClaimHandleID,
		VersionId:     in.VersionID,
	})
	if err != nil {
		return clientiface.GetVersionSchemaOutput{}, fmt.Errorf("remote data_processing %q: GetVersionSchema: %w", c.name, err)
	}
	return clientiface.GetVersionSchemaOutput{Schema: resp.GetSchema()}, nil
}

// Close releases the gRPC connection.
func (c *DataProcessingClient) Close() {
	if c.conn != nil {
		_ = c.conn.Close()
	}
}

// DialDataProcessing connects to a peer that implements the
// DataProcessing service. Insecure credentials by default; mTLS is a
// deployment-layer follow-up.
func DialDataProcessing(_ context.Context, name, endpoint string) (*DataProcessingClient, error) {
	target, err := stripScheme(name, endpoint)
	if err != nil {
		return nil, err
	}
	conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("remote data_processing %q: dial %q: %w", name, endpoint, err)
	}
	return &DataProcessingClient{
		name: name,
		conn: conn,
		rpc:  genv1.NewDataProcessingClient(conn),
	}, nil
}
