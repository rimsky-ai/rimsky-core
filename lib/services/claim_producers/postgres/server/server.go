// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	claimproducer "github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	bridge "github.com/rimsky-ai/rimsky-core/lib/protocols/serverkit"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/serviceauth"
	pgsstore "github.com/rimsky-ai/rimsky-core/lib/services/claim_producers/postgres/store"
	"github.com/rimsky-ai/rimsky-core/lib/services/claim_producers/shared/lifecycle"
	"github.com/rimsky-ai/rimsky-core/lib/services/claim_producers/shared/listarray"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// @decision: graceful-shutdown
const gracefulStopBudget = bridge.BundledServiceGrace

type Config struct {
	Connection        string
	WriteSemantics    claimproducer.WriteSemantics
	PickPolicies      map[string]*pgsstore.PickPolicy
	PartitionPolicies map[string]*pgsstore.PartitionPolicy
	SweepInterval     time.Duration
	HTTPBridgeURL     string
	EnableLifecycle   bool
	// @concept: executor
	EnableExecutor   bool
	LedgerMaxRecords int
}

func New(ctx context.Context, cfg Config) (*Server, error) {
	st, err := pgsstore.New(ctx, pgsstore.Config{
		Connection:        cfg.Connection,
		WriteSemantics:    cfg.WriteSemantics,
		PickPolicies:      cfg.PickPolicies,
		PartitionPolicies: cfg.PartitionPolicies,
		LedgerMaxRecords:  cfg.LedgerMaxRecords,
	})
	if err != nil {
		return nil, err
	}
	return &Server{store: st, protocols: declaredProtocols(cfg)}, nil
}

func declaredProtocols(cfg Config) []string {
	var protocols []string
	if cfg.EnableLifecycle {
		protocols = append(protocols, claimproducer.ProtocolLifecycleSubscriber)
	}
	if cfg.EnableExecutor {
		protocols = append(protocols, claimproducer.ProtocolExecutor)
	}
	return protocols
}

func (s *Server) RunSweep(ctx context.Context, interval time.Duration) {
	s.store.RunSweep(ctx, interval)
}

func Run(ctx context.Context, cfg Config, grpcLis, httpLis, adminLis net.Listener) error {
	srv, err := New(ctx, cfg)
	if err != nil {
		return err
	}
	st := srv.store
	identity, err := serviceauth.LoadFromEnv(ctx, "claim-producer-postgres")
	if err != nil {
		return err
	}
	identity.StartMaintain(ctx, "claim-producer-postgres")
	grpcSrv := grpc.NewServer(identity.GRPCServerOptions()...)
	genv1.RegisterClaimProducerServer(grpcSrv, srv)
	if cfg.EnableLifecycle {
		genv1.RegisterLifecycleSubscriberServer(grpcSrv, lifecycle.NewServer())
	}
	if cfg.EnableExecutor {
		genv1.RegisterExecutorServer(grpcSrv, NewExecutorServer(st))
		genv1.RegisterExecutorObservabilityServer(grpcSrv, NewExecutorObservabilityServer())
	}
	obsSrv := srv.RegisterObservability(grpcSrv)
	obsSrv.SetHTTPBridgeURL(cfg.HTTPBridgeURL)
	go func() {
		if err := grpcSrv.Serve(grpcLis); err != nil {
			slog.Warn("POSTGRESSTORE.GRPC.SERVEFAILED", "error", err.Error())
		}
	}()

	httpMux := http.NewServeMux()
	bridge.Mount(httpMux, srv)
	if cfg.EnableLifecycle {
		bridge.MountLifecycle(httpMux, lifecycle.NewServer())
	}
	bridge.MountObservability(httpMux, obsSrv)
	httpSrv := identity.HTTPServer(httpMux)
	go func() {
		if err := identity.RunHTTP(httpSrv, httpLis); err != nil && err != http.ErrServerClosed {
			slog.Warn("POSTGRESSTORE.HTTP.SERVEFAILED", "error", err.Error())
		}
	}()

	var adminSrv *http.Server
	if adminLis != nil {
		adminSrv = &http.Server{Handler: st.AdminHandler()}
		go func() {
			if err := adminSrv.Serve(adminLis); err != nil && err != http.ErrServerClosed {
				slog.Warn("POSTGRESSTORE.ADMINHTTP.SERVEFAILED", "error", err.Error())
			}
		}()
	}

	go srv.RunSweep(ctx, cfg.SweepInterval)

	<-ctx.Done()
	bridge.GracefulStop(grpcSrv, gracefulStopBudget)
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), gracefulStopBudget)
	defer cancelShutdown()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		slog.Warn("POSTGRESSTORE.HTTP.SHUTDOWNFAILED", "error", err.Error())
		_ = httpSrv.Close()
	}
	if adminSrv != nil {
		if err := adminSrv.Shutdown(shutdownCtx); err != nil {
			slog.Warn("POSTGRESSTORE.ADMINHTTP.SHUTDOWNFAILED", "error", err.Error())
			_ = adminSrv.Close()
		}
	}
	st.Close()
	return nil
}

type Server struct {
	genv1.UnimplementedClaimProducerServer
	store     *pgsstore.Store
	protocols []string
}

func producerDeclaredErrorClasses() []string {
	return []string{
		pgsstore.ClaimUnavailableClass,
		pgsstore.SwapFailedClass,
		pgsstore.NotReplaceableClass,
	}
}

func (s *Server) Capabilities(_ context.Context, _ *genv1.CapabilitiesRequest) (*genv1.CapabilitiesResponse, error) {
	c := s.store.Capabilities()
	out := make([]genv1.WriteSemantics, 0, len(c.WriteSemanticsAllowed))
	for _, ws := range c.WriteSemanticsAllowed {
		out = append(out, bridge.WriteSemanticsToProto(string(ws)))
	}
	return &genv1.CapabilitiesResponse{
		WriteSemanticsAllowed: out,
		DeclaredErrorClasses:  producerDeclaredErrorClasses(),
		SupportsSplitScope:    true,
		Protocols:             s.protocols,
	}, nil
}

func (s *Server) Open(ctx context.Context, req *genv1.OpenRequest) (*genv1.OpenResponse, error) {
	if intent := req.GetIntent(); intent != "r" && intent != "rw" {
		return nil, status.Errorf(codes.InvalidArgument, "postgres.Open: intent must be \"r\" or \"rw\", got %q", intent)
	}
	outcome, err := s.store.Open(ctx, req.GetClaimId(), req.GetSelector(), claimproducer.Intent(req.GetIntent()))
	if err != nil {
		return nil, classedStatus(err)
	}
	if !outcome.Available {
		return &genv1.OpenResponse{
			Result: &genv1.OpenResponse_Unavailable{Unavailable: &genv1.Unavailable{
				ErrorClass: outcome.UnavailableClass,
			}},
		}, nil
	}
	return &genv1.OpenResponse{
		Result: &genv1.OpenResponse_Acquired{Acquired: &genv1.Acquired{
			Address:                outcome.Result.Address,
			Payload:                outcome.Result.Payload,
			ClaimScope:             outcome.Result.ClaimScope,
			RealizedWriteSemantics: bridge.WriteSemanticsToProto(string(outcome.Result.RealizedWriteSemantics)),
		}},
	}, nil
}

func (s *Server) Commit(ctx context.Context, req *genv1.CommitRequest) (*genv1.CommitResponse, error) {
	if err := s.store.Commit(ctx, req.GetClaimId(), req.GetClaimScope(), req.GetAddress()); err != nil {
		return nil, classedStatus(err)
	}
	return &genv1.CommitResponse{}, nil
}

func (s *Server) Abandon(ctx context.Context, req *genv1.AbandonRequest) (*genv1.AbandonResponse, error) {
	if err := s.store.Abandon(ctx, req.GetClaimId(), req.GetClaimScope(), req.GetAddress()); err != nil {
		return nil, classedStatus(err)
	}
	return &genv1.AbandonResponse{}, nil
}

func (s *Server) Release(ctx context.Context, req *genv1.ReleaseRequest) (*genv1.ReleaseResponse, error) {
	if err := s.store.Release(ctx, req.GetClaimId(), req.GetClaimScope(), req.GetAddress()); err != nil {
		return nil, classedStatus(err)
	}
	return &genv1.ReleaseResponse{}, nil
}

type parentClaimInfo struct {
	ClaimID  string
	Selector string
	RowID    string
}

// @concept: fan-out
func (s *Server) SplitScope(ctx context.Context, req *genv1.SplitScopeRequest) (*genv1.SplitScopeResponse, error) {
	claimID := req.GetClaimHandleId()
	if claimID == "" {
		return nil, status.Errorf(codes.InvalidArgument, "postgres store: SplitScope: claim_handle_id is required")
	}
	lookup, ok := s.store.LookupClaim(claimID)
	if !ok {
		return nil, status.Errorf(codes.NotFound, "postgres store: SplitScope: unknown claim_handle_id %q", claimID)
	}
	if !lookup.IsOpen {
		return nil, status.Errorf(codes.FailedPrecondition, "postgres store: SplitScope: claim %q is not OPEN", claimID)
	}
	rowID, err := decodeRowID(lookup.Scope)
	if err != nil {
		return nil, status.Errorf(codes.Internal,
			"postgres store: SplitScope: decode parent claim_scope for %q: %v", claimID, err)
	}
	parent := parentClaimInfo{ClaimID: claimID, Selector: lookup.Selector, RowID: rowID}

	partitionRequest := req.GetPartitionRequest()
	if len(partitionRequest) == 0 {
		return nil, status.Errorf(codes.InvalidArgument,
			"postgres store: SplitScope: partition_request must specify list or partition_policy")
	}
	var probe struct {
		List            json.RawMessage `json:"list"`
		PartitionPolicy json.RawMessage `json:"partition_policy"`
		Params          json.RawMessage `json:"params"`
	}
	probeDec := json.NewDecoder(bytes.NewReader(partitionRequest))
	probeDec.DisallowUnknownFields()
	if err := probeDec.Decode(&probe); err != nil {
		return nil, status.Errorf(codes.InvalidArgument,
			"postgres store: SplitScope: partition_request is not a JSON object or contains unknown top-level keys (only list / partition_policy / params are permitted): %v", err)
	}

	var (
		descs []*genv1.SubScopeDescriptor
		hErr  error
	)
	switch {
	case probe.List != nil:
		if probe.PartitionPolicy != nil || probe.Params != nil {
			return nil, status.Errorf(codes.InvalidArgument,
				"postgres store: SplitScope: partition_request must specify exactly one of list or partition_policy; got both")
		}
		descs, hErr = s.splitListArrayPg(ctx, parent, partitionRequest)
	case probe.PartitionPolicy != nil && probe.Params != nil:
		descs, hErr = s.splitPartitionPolicy(ctx, parent, probe.PartitionPolicy, probe.Params)
	case probe.PartitionPolicy != nil && probe.Params == nil:
		return nil, status.Errorf(codes.InvalidArgument,
			"postgres store: SplitScope: partition_policy shape requires a params field (use {} if no params)")
	case probe.Params != nil && probe.PartitionPolicy == nil:
		return nil, status.Errorf(codes.InvalidArgument,
			"postgres store: SplitScope: params field requires a partition_policy field")
	default:
		return nil, status.Errorf(codes.InvalidArgument,
			"postgres store: SplitScope: partition_request must specify list or partition_policy")
	}
	if hErr != nil {
		return nil, hErr
	}
	return &genv1.SplitScopeResponse{SubScopes: descs}, nil
}

// @decision: fanout-list-array-store-agnostic
func (s *Server) splitListArrayPg(_ context.Context, parent parentClaimInfo, partitionRequest []byte) ([]*genv1.SubScopeDescriptor, error) {
	req, err := listarray.Unmarshal(partitionRequest)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument,
			"postgres store: SplitScope list: %v", err)
	}
	subs, err := listarray.ToSubScopes(req, func(key string) ([]byte, error) {
		return json.Marshal(map[string]string{
			"parent_row_id": parent.RowID,
			"key":           key,
		})
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal,
			"postgres store: SplitScope list: synthesize claim_scope: %v", err)
	}
	out := make([]*genv1.SubScopeDescriptor, 0, len(subs))
	for _, sub := range subs {
		out = append(out, &genv1.SubScopeDescriptor{
			PartitionKey:   sub.PartitionKey,
			ClaimScopeData: sub.ClaimScopeData,
			Payload:        sub.Payload,
			Address:        sub.Address,
		})
	}
	return out, nil
}

func (s *Server) splitPartitionPolicy(ctx context.Context, _ parentClaimInfo, policyJSON, paramsJSON json.RawMessage) ([]*genv1.SubScopeDescriptor, error) {
	var policyName string
	if err := json.Unmarshal(policyJSON, &policyName); err != nil {
		return nil, status.Errorf(codes.InvalidArgument,
			"postgres store: SplitScope partition_policy: name must be a JSON string: %v", err)
	}
	if policyName == "" {
		return nil, status.Errorf(codes.InvalidArgument,
			"postgres store: SplitScope partition_policy: name must be non-empty")
	}
	pp, ok := s.store.PartitionPolicy(policyName)
	if !ok {
		return nil, status.Errorf(codes.InvalidArgument,
			"postgres store: SplitScope partition_policy: %q is not declared in partition_policies", policyName)
	}
	params := map[string]any{}
	if len(paramsJSON) > 0 && string(paramsJSON) != "null" {
		if err := json.Unmarshal(paramsJSON, &params); err != nil {
			return nil, status.Errorf(codes.InvalidArgument,
				"postgres store: SplitScope partition_policy %q: params must be a JSON object: %v", policyName, err)
		}
	}
	rows, err := s.store.RunPartitionPolicy(ctx, pp, params)
	if err != nil {
		return nil, classifyPartitionPolicyError(err, policyName)
	}
	out := make([]*genv1.SubScopeDescriptor, 0, len(rows))
	for _, r := range rows {
		scope, mErr := json.Marshal(r.ID)
		if mErr != nil {
			return nil, status.Errorf(codes.Internal,
				"postgres store: SplitScope partition_policy %q: marshal claim_scope for row id=%q: %v",
				policyName, r.ID, mErr)
		}
		out = append(out, &genv1.SubScopeDescriptor{
			PartitionKey:   r.ID,
			ClaimScopeData: scope,
			Payload:        r.RowJSON,
			Address:        nil,
		})
	}
	return out, nil
}

func classifyPartitionPolicyError(err error, policyName string) error {
	var ce *pgsstore.ClassedError
	if errors.As(err, &ce) && ce.Class == pgsstore.PartitionPolicyInvalidRequestClass {
		return status.Errorf(codes.InvalidArgument,
			"postgres store: SplitScope partition_policy %q: %v", policyName, err)
	}
	return status.Errorf(codes.Internal,
		"postgres store: SplitScope partition_policy %q: %v", policyName, err)
}

func decodeRowID(scope []byte) (string, error) {
	if len(scope) == 0 {
		return "", nil
	}
	var s string
	if err := json.Unmarshal(scope, &s); err != nil {
		return "", fmt.Errorf("decodeRowID: claim_scope is not a JSON-encoded string: %w", err)
	}
	return s, nil
}

func classedStatus(err error) error {
	if err == nil {
		return nil
	}
	var ce *pgsstore.ClassedError
	if !errors.As(err, &ce) || ce.Class == "" {
		return err
	}
	st := status.New(codes.Internal, ce.Error())
	withInfo, derr := st.WithDetails(&errdetails.ErrorInfo{
		Reason: ce.Class,
		Domain: "rimsky.claim-producer-postgres",
	})
	if derr != nil {
		return st.Err()
	}
	return withInfo.Err()
}
