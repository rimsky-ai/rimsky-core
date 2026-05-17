// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package remote

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/fallguy/rimsky/foundation/shared"
	genv1 "github.com/fallguy/rimsky/protocols/proto/v1/gen"
	"github.com/fallguy/rimsky/runtime"
)

// SensorClient is a remote-gRPC implementation of the rimsky-side
// runtime.SensorClient interface. One client per sensor service that
// advertises the `sensor` protocol in rimsky.yml.
type SensorClient struct {
	name string
	conn *grpc.ClientConn
	rpc  genv1.SensorClient
}

// Compile-time interface check.
var _ runtime.SensorClient = (*SensorClient)(nil)

// Name returns the operator-configured sensor service name.
func (c *SensorClient) Name() string { return c.name }

// StartWatch RPCs to the remote sensor.
func (c *SensorClient) StartWatch(ctx context.Context, req runtime.StartWatchRequest) error {
	_, err := c.rpc.StartWatch(ctx, &genv1.StartWatchRequest{
		WatchId:        req.WatchID.String(),
		InstanceId:     req.InstanceID.String(),
		Kind:           req.Kind,
		ResolvedConfig: req.ResolvedConfig,
	})
	if err != nil {
		return fmt.Errorf("remote sensor %q: StartWatch: %w", c.name, err)
	}
	return nil
}

// StopWatch RPCs to the remote sensor.
func (c *SensorClient) StopWatch(ctx context.Context, watchID shared.UUID) error {
	_, err := c.rpc.StopWatch(ctx, &genv1.StopWatchRequest{
		WatchId: watchID.String(),
	})
	if err != nil {
		return fmt.Errorf("remote sensor %q: StopWatch: %w", c.name, err)
	}
	return nil
}

// ListWatches RPCs to the remote sensor.
func (c *SensorClient) ListWatches(ctx context.Context) ([]runtime.ListedSensorWatch, error) {
	resp, err := c.rpc.ListWatches(ctx, &emptypb.Empty{})
	if err != nil {
		return nil, fmt.Errorf("remote sensor %q: ListWatches: %w", c.name, err)
	}
	out := make([]runtime.ListedSensorWatch, 0, len(resp.GetWatches()))
	for _, w := range resp.GetWatches() {
		wid, err := uuid.Parse(w.GetWatchId())
		if err != nil {
			return nil, fmt.Errorf("remote sensor %q: ListWatches: bad watch_id %q: %w", c.name, w.GetWatchId(), err)
		}
		iid, err := uuid.Parse(w.GetInstanceId())
		if err != nil {
			return nil, fmt.Errorf("remote sensor %q: ListWatches: bad instance_id %q: %w", c.name, w.GetInstanceId(), err)
		}
		out = append(out, runtime.ListedSensorWatch{
			WatchID:    shared.UUID(wid),
			InstanceID: shared.UUID(iid),
			Kind:       w.GetKind(),
		})
	}
	return out, nil
}

// Close releases the gRPC connection.
func (c *SensorClient) Close() {
	if c.conn != nil {
		_ = c.conn.Close()
	}
}

// DialSensor connects to a peer that implements the Sensor service.
func DialSensor(_ context.Context, name, endpoint string) (*SensorClient, error) {
	target, err := stripScheme(name, endpoint)
	if err != nil {
		return nil, err
	}
	conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("remote sensor %q: dial %q: %w", name, endpoint, err)
	}
	return &SensorClient{
		name: name,
		conn: conn,
		rpc:  genv1.NewSensorClient(conn),
	}, nil
}
