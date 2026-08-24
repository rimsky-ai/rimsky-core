// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package service

import (
	"context"
	"fmt"

	"google.golang.org/grpc"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/clientiface"
)

type DataProcessingClient struct {
	name string
	conn *grpc.ClientConn
	rpc  genv1.DataProcessingClient
}

var _ clientiface.DataProcessingClient = (*DataProcessingClient)(nil)

func (c *DataProcessingClient) Name() string { return c.name }

func (c *DataProcessingClient) BeginCandidate(ctx context.Context, in clientiface.BeginCandidateInput) (clientiface.BeginCandidateOutput, error) {
	resp, err := c.rpc.BeginCandidate(ctx, &genv1.BeginCandidateRequest{
		ClaimHandleId:      in.ClaimHandleID,
		SubScopeDescriptor: in.SubScopeDescriptor,
		IdempotencyKey:     in.IdempotencyKey,
	})
	if err != nil {
		return clientiface.BeginCandidateOutput{}, NewProducerCallError(c.name, "DataProcessing.BeginCandidate", err)
	}
	return clientiface.BeginCandidateOutput{CandidateHandle: resp.GetCandidateHandle()}, nil
}

func (c *DataProcessingClient) CommitCandidate(ctx context.Context, in clientiface.CommitCandidateInput) (clientiface.CommitCandidateOutput, error) {
	resp, err := c.rpc.CommitCandidate(ctx, &genv1.CommitCandidateRequest{
		CandidateHandle: in.CandidateHandle,
	})
	if err != nil {
		return clientiface.CommitCandidateOutput{}, NewProducerCallError(c.name, "DataProcessing.CommitCandidate", err)
	}
	return clientiface.CommitCandidateOutput{
		CandidateMetadata: resp.GetCandidateMetadata(),
	}, nil
}

func (c *DataProcessingClient) AbandonCandidate(ctx context.Context, in clientiface.AbandonCandidateInput) error {
	_, err := c.rpc.AbandonCandidate(ctx, &genv1.AbandonCandidateRequest{
		CandidateHandle: in.CandidateHandle,
	})
	if err != nil {
		return NewProducerCallError(c.name, "DataProcessing.AbandonCandidate", err)
	}
	return nil
}

func (c *DataProcessingClient) ListVersions(ctx context.Context, in clientiface.ListVersionsInput) (clientiface.ListVersionsOutput, error) {
	resp, err := c.rpc.ListVersions(ctx, &genv1.ListVersionsRequest{
		ClaimHandleId: in.ClaimHandleID,
	})
	if err != nil {
		return clientiface.ListVersionsOutput{}, NewProducerCallError(c.name, "DataProcessing.ListVersions", err)
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

func (c *DataProcessingClient) ListPartitions(ctx context.Context, in clientiface.ListPartitionsInput) (clientiface.ListPartitionsOutput, error) {
	resp, err := c.rpc.ListPartitions(ctx, &genv1.ListPartitionsRequest{
		ClaimHandleId: in.ClaimHandleID,
		VersionId:     in.VersionID,
	})
	if err != nil {
		return clientiface.ListPartitionsOutput{}, NewProducerCallError(c.name, "DataProcessing.ListPartitions", err)
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

func (c *DataProcessingClient) GetVersionSchema(ctx context.Context, in clientiface.GetVersionSchemaInput) (clientiface.GetVersionSchemaOutput, error) {
	resp, err := c.rpc.GetVersionSchema(ctx, &genv1.GetVersionSchemaRequest{
		ClaimHandleId: in.ClaimHandleID,
		VersionId:     in.VersionID,
	})
	if err != nil {
		return clientiface.GetVersionSchemaOutput{}, NewProducerCallError(c.name, "DataProcessing.GetVersionSchema", err)
	}
	return clientiface.GetVersionSchemaOutput{Schema: resp.GetSchema()}, nil
}

func (c *DataProcessingClient) Close() {
	if c.conn != nil {
		_ = c.conn.Close()
	}
}

func DialDataProcessing(_ context.Context, name, endpoint, tlsMode string) (*DataProcessingClient, error) {
	target, err := StripScheme(name, endpoint)
	if err != nil {
		return nil, err
	}
	conn, err := grpc.NewClient(target, dialOptions(name, tlsMode)...)
	if err != nil {
		return nil, fmt.Errorf("remote data_processing %q: dial %q: %w", name, endpoint, err)
	}
	return &DataProcessingClient{
		name: name,
		conn: conn,
		rpc:  genv1.NewDataProcessingClient(conn),
	}, nil
}
